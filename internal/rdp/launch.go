package rdp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

var (
	ErrClientNotFound     = errors.New("rdp client not found")
	ShortSessionThreshold = 3 * time.Second
)

// Credential writes one password line to the child stdin pipe.
type Credential interface {
	WriteLine(w io.Writer) error
}

// Clock is used by Classify / session duration.
type Clock interface {
	Now() time.Time
}

type sysClock struct{}

func (sysClock) Now() time.Time { return time.Now() }

// Runner starts a child process.
type Runner interface {
	LookPath(file string) (string, error)
	Command(name string, arg ...string) *exec.Cmd
}

// OSRunner is the production runner.
type OSRunner struct{}

func (OSRunner) LookPath(file string) (string, error) { return exec.LookPath(file) }
func (OSRunner) Command(name string, arg ...string) *exec.Cmd {
	return exec.Command(name, arg...)
}

// Launcher starts a FreeRDP child.
type Launcher struct {
	Runner Runner
	Clock  Clock
	Stdout io.Writer
	Stderr io.Writer
	// OwnSignals says the caller handles signals itself. Otherwise, while
	// the client runs, Start passes SIGINT on to its process group, as Ctrl+C
	// in a terminal the client owned would have done, and turns SIGTERM or
	// SIGHUP into a stop of the whole group: SIGTERM, then SIGKILL after
	// StopGrace. The client runs in a process group of its own, so without
	// that a launcher script ending `wicket connect`, or a closed terminal,
	// would end Wicket and leave FreeRDP and its helpers running. The TUI
	// keeps the terminal in raw mode while a session runs and stops the
	// client through the Session itself; a second forwarder would count one
	// Ctrl+C twice.
	OwnSignals bool
	// StopGrace is how long a client stopped on SIGTERM or SIGHUP gets
	// before it is killed, when the launcher forwards those signals (see
	// OwnSignals). Zero means DefaultStopGrace.
	StopGrace time.Duration
	// WaitDelay bounds how long Wait keeps copying the client's output
	// after it exits. Output written to anything but a file is copied by a
	// goroutine that only ends when every holder of the pipe has closed it,
	// so a helper the client left running would otherwise hold Wait open.
	// Zero waits for as long as it takes, as exec.Cmd does.
	WaitDelay time.Duration
}

// DefaultStopGrace is how long a forwarded SIGTERM or SIGHUP gives the client
// before it is killed.
const DefaultStopGrace = 3 * time.Second

func (l *Launcher) stopGrace() time.Duration {
	if l.StopGrace > 0 {
		return l.StopGrace
	}
	return DefaultStopGrace
}

func (l *Launcher) clock() Clock {
	if l.Clock != nil {
		return l.Clock
	}
	return sysClock{}
}

func (l *Launcher) runner() Runner {
	if l.Runner != nil {
		return l.Runner
	}
	return OSRunner{}
}

// Session is a started child. Wait returns after the process exits; it may
// be called from any number of goroutines, as may the signalling methods.
type Session struct {
	cmd     *exec.Cmd
	pgid    int
	started time.Time
	clock   Clock
	// client is the client's basename, for Outcome.Client.
	client string
	// interrupt forwards Wicket's own SIGINT, SIGTERM and SIGHUP to the
	// client. It is nil when the launcher's caller handles signals itself.
	interrupt chan os.Signal

	done chan struct{}
	out  Outcome

	mu sync.Mutex
	// reaped is set once the client's pid may no longer be ours to signal:
	// after Wait has collected it, the kernel is free to hand the number to
	// an unrelated process, and a kill aimed at the old group could land on
	// a stranger.
	reaped bool
	// stage is how far a stop has escalated, as an index into stopSignals:
	// 0 until Interrupt, Escalate or Terminate has sent anything.
	stage int
	// stopped is the first SIGTERM or SIGHUP Wicket received and passed on.
	stopped os.Signal
}

// stopSignals are the signals a stop sends, in order. They are indexed
// rather than compared, since SIGKILL's number is lower than SIGTERM's.
var stopSignals = [...]syscall.Signal{0, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL}

const (
	stageInt  = 1
	stageTerm = 2
	stageKill = 3
)

type Class int

const (
	ClassStartError Class = iota
	// ClassShortSession is a clean exit so soon after the start that the
	// session probably never happened: a window closed at once, say.
	ClassShortSession
	// ClassEnded is a session that ended the way sessions do: the window
	// was closed, the user logged off, the server disconnected it.
	ClassEnded
	// ClassFailed is a client that exited with an error, or was killed by a
	// signal, however long it ran. FreeRDP can take a quarter of a minute to
	// give up on a host it cannot reach, and a failure that slow used to read
	// as a session that had simply ended. For FreeRDP's own clients the exit
	// code says which endings are errors (see exitcode.go); for any other
	// client every non-zero status is one.
	ClassFailed
)

type Outcome struct {
	StartErr error
	ExitCode int
	Signaled bool
	Duration time.Duration
	// Stopped is the signal Wicket itself received, and passed on, that
	// ended the session: SIGTERM or SIGHUP, when the launcher's caller does
	// not handle signals itself. It is nil when nothing stopped Wicket.
	Stopped os.Signal
	// Client is the basename of the client that ran, which says how to read
	// its exit code.
	Client string
}

// Classify says how a session ended. A caller that stopped the client itself
// knows better than the exit status whether that was a failure, and should
// say so over this.
func Classify(o Outcome) Class {
	if o.StartErr != nil {
		return ClassStartError
	}
	if o.Signaled {
		return ClassFailed
	}
	// A plain end -- status 0, or 1 from FreeRDP, whose SDL client exits 1
	// when its window is closed -- so soon after the start that the session
	// probably never happened is offered a retry. A logoff, a timeout or a
	// cancel is something someone chose, however quick.
	plain := o.ExitCode == 0
	if e, ok := o.meaning(); ok {
		if !e.ended {
			return ClassFailed
		}
		plain = o.ExitCode <= 1
		if !plain {
			return ClassEnded
		}
	} else if o.ExitCode != 0 {
		return ClassFailed
	}
	if plain && o.Duration < ShortSessionThreshold {
		return ClassShortSession
	}
	return ClassEnded
}

// ExitStatus is Wicket's process exit code after a successful start: the
// child's code, or 128+signal when it was killed. A session ended because
// Wicket itself was sent SIGTERM or SIGHUP exits as a program killed by that
// signal conventionally does, 128 plus its number, whatever the client made
// of it: the caller asked Wicket to stop, and should be able to tell.
func (o Outcome) ExitStatus() int {
	if o.StartErr != nil {
		return 2
	}
	if sig, ok := o.Stopped.(syscall.Signal); ok {
		return 128 + int(sig)
	}
	return o.ExitCode
}

func illegalBasename(client string) bool {
	if client == "" {
		return true
	}
	if strings.ContainsRune(client, '/') {
		return true
	}
	return strings.ContainsFunc(client, unicode.IsSpace)
}

// Start looks up the client, spawns it in its own process group, writes the credential to stdin, and returns.
func (l *Launcher) Start(plan Plan, cred Credential) (*Session, error) {
	if illegalBasename(plan.Client) {
		return nil, fmt.Errorf("%w: %s", ErrClientNotFound, plan.Client)
	}
	path, err := l.runner().LookPath(plan.Client)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrClientNotFound, plan.Client)
	}
	cmd := l.runner().Command(path, plan.Args...)
	cmd.Stdout = l.Stdout
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = l.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = l.WaitDelay
	// The handler goes in before the client starts. Installed after, a
	// SIGTERM or SIGHUP that landed while the client was starting, or while
	// its password was being written, took Wicket's default action: Wicket
	// died, and the client, in a group of its own, kept running. A signal
	// caught before the group exists waits in the channel, and stops the
	// group as soon as there is one.
	var sigs chan os.Signal
	if !l.OwnSignals {
		sigs = make(chan os.Signal, 4)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		if sigs != nil {
			// Nothing was started, so there is nothing to stop.
			signal.Stop(sigs)
		}
		return nil, err
	}
	sess := &Session{
		cmd:       cmd,
		pgid:      cmd.Process.Pid,
		started:   l.clock().Now(),
		clock:     l.clock(),
		client:    filepath.Base(plan.Client),
		done:      make(chan struct{}),
		interrupt: sigs,
	}
	if sigs != nil {
		grace := l.stopGrace()
		go func(ch <-chan os.Signal) {
			for sig := range ch {
				if sig == os.Interrupt {
					sess.signal(syscall.SIGINT)
					continue
				}
				// Terminate waits, and a second signal must still be
				// drained from ch, so the stop runs on its own.
				if sess.noteStopped(sig) {
					go sess.Terminate(grace)
				}
			}
		}(sigs)
	}
	// The reaper runs from here on, so a stop the handler starts while the
	// password is being written sees the client exit, and nothing signals
	// its group once it has been collected.
	go sess.reap()
	if cred != nil {
		// A credential that cannot reach the child is fatal: the client would
		// otherwise sit at a prompt it can never satisfy, and the caller would
		// blame the password.
		if err := cred.WriteLine(stdin); err != nil {
			_ = stdin.Close()
			sess.signal(syscall.SIGKILL)
			out := sess.Wait()
			if out.Stopped != nil {
				// Wicket was told to stop while the password was on its
				// way, and stopping the client is what broke the pipe: the
				// session says so, and the caller exits as told.
				return sess, nil
			}
			return nil, fmt.Errorf("could not send the password to %s: %w", plan.Client, err)
		}
	}
	_ = stdin.Close()
	// The reaper is the only caller of cmd.Wait, so Wait, Done and a stop
	// racing the exit all see one outcome.
	return sess, nil
}

// Wait blocks until the client has exited and returns how it ended.
func (s *Session) Wait() Outcome {
	if s == nil || s.cmd == nil {
		return Outcome{StartErr: errors.New("no session")}
	}
	<-s.done
	return s.out
}

// Done is closed once the client has exited and Wait would not block.
func (s *Session) Done() <-chan struct{} { return s.done }

// Interrupt sends SIGINT to the client's process group: what Ctrl+C would
// have done in a terminal the client owned. It reports whether the signal
// was sent; a client that has already exited is left alone.
func (s *Session) Interrupt() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stage = max(s.stage, stageInt)
	return s.signalLocked(syscall.SIGINT)
}

// Escalate sends the next signal a stop has not tried yet: SIGINT, then
// SIGTERM, then SIGKILL, which it repeats. It returns the signal sent, or 0
// when the client has already exited.
func (s *Session) Escalate() syscall.Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reaped {
		return 0
	}
	s.stage = min(s.stage+1, stageKill)
	sig := stopSignals[s.stage]
	if !s.signalLocked(sig) {
		return 0
	}
	return sig
}

// Terminate stops the client for good: SIGTERM to its group, then SIGKILL
// if it has not exited within grace. It returns once the client has exited,
// or after a further grace if even SIGKILL has not ended it. Wicket calls it
// on the way out so a session is never left running without the window
// that started it.
func (s *Session) Terminate(grace time.Duration) {
	if s == nil || s.cmd == nil {
		return
	}
	s.mu.Lock()
	s.stage = max(s.stage, stageTerm)
	s.signalLocked(syscall.SIGTERM)
	s.mu.Unlock()
	select {
	case <-s.done:
		return
	case <-time.After(grace):
	}
	s.mu.Lock()
	s.stage = stageKill
	s.signalLocked(syscall.SIGKILL)
	s.mu.Unlock()
	select {
	case <-s.done:
	case <-time.After(grace):
	}
}

// noteStopped records sig as what ended the session, and reports whether it
// is the first such signal: a second SIGTERM needs no second stop.
func (s *Session) noteStopped(sig os.Signal) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped != nil {
		return false
	}
	s.stopped = sig
	return true
}

func (s *Session) signal(sig syscall.Signal) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signalLocked(sig)
}

// signalLocked signals the whole process group, so a helper the client
// started stops with it. The caller holds s.mu.
func (s *Session) signalLocked(sig syscall.Signal) bool {
	if s.reaped {
		return false
	}
	return syscall.Kill(-s.pgid, sig) == nil
}

func (s *Session) reap() {
	// Wait for the exit without collecting it. Until the zombie is
	// collected its pid, and so the group id, cannot be reused, which is
	// what makes it safe to signal the group right up to this point.
	// The session ends when the client does. cmd.Wait can return up to
	// WaitDelay later, while a helper holds the output open, and a session
	// timed from then would be reported longer than it was: long enough,
	// near the threshold, to hide a failed connect from the retry offer.
	var end time.Time
	if waitExited(s.pgid) {
		end = s.clock.Now()
		s.mu.Lock()
		// Start put the client in a group of its own, so anything still in
		// it is a helper the client left behind. It goes with the client,
		// while the group id is still ours. Without this a helper would
		// outlive the session, and wicket connect with it, with nothing left
		// to stop it.
		_ = syscall.Kill(-s.pgid, syscall.SIGKILL)
		s.reaped = true
		s.mu.Unlock()
	}
	err := s.cmd.Wait()
	if end.IsZero() {
		end = s.clock.Now()
	}
	s.mu.Lock()
	s.reaped = true
	s.mu.Unlock()
	// signal.Stop removes only this session's channel. signal.Reset must not be
	// used here: it is process-global and would also tear down any other
	// SIGINT handler in the process, such as the TUI's.
	if s.interrupt != nil {
		signal.Stop(s.interrupt)
		close(s.interrupt)
	}
	s.out = outcomeOf(s.cmd.ProcessState, err, end.Sub(s.started))
	s.out.Client = s.client
	s.mu.Lock()
	s.out.Stopped = s.stopped
	s.mu.Unlock()
	close(s.done)
}

// outcomeOf reads how the client ended from its process state, which Wait
// sets whenever it collected the client, whatever else it returns: an error
// such as ErrWaitDelay, from a helper that kept the client's output open past
// the launcher's WaitDelay, says nothing about the client's own exit. Only
// with no state at all did the client fail to run.
func outcomeOf(ps *os.ProcessState, err error, dur time.Duration) Outcome {
	o := Outcome{Duration: dur}
	if ps == nil {
		if err == nil {
			err = errors.New("client exited without a status")
		}
		o.StartErr = err
		return o
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		o.Signaled = true
		o.ExitCode = 128 + int(ws.Signal())
		return o
	}
	o.ExitCode = ps.ExitCode()
	return o
}
