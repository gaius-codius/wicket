package rdp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
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

// Session is a started child. Wait returns after the process exits.
type Session struct {
	cmd       *exec.Cmd
	started   time.Time
	clock     Clock
	interrupt chan os.Signal
}

type Class int

const (
	ClassStartError Class = iota
	ClassShortSession
	ClassEnded
)

type Outcome struct {
	StartErr error
	ExitCode int
	Signaled bool
	Duration time.Duration
}

func Classify(o Outcome) Class {
	if o.StartErr != nil {
		return ClassStartError
	}
	if o.Duration < ShortSessionThreshold {
		return ClassShortSession
	}
	return ClassEnded
}

// ExitStatus is Wicket's process exit code after a successful start: child's code, or 128+signal.
func (o Outcome) ExitStatus() int {
	if o.StartErr != nil {
		return 2
	}
	if o.Signaled {
		return o.ExitCode
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
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if cred != nil {
		_ = cred.WriteLine(stdin)
	}
	_ = stdin.Close()

	intCh := make(chan os.Signal, 1)
	signal.Notify(intCh, os.Interrupt)
	go func(proc *os.Process) {
		for range intCh {
			if proc != nil {
				_ = syscall.Kill(-proc.Pid, syscall.SIGINT)
			}
		}
	}(cmd.Process)

	return &Session{
		cmd:       cmd,
		started:   l.clock().Now(),
		clock:     l.clock(),
		interrupt: intCh,
	}, nil
}

func (s *Session) Wait() Outcome {
	if s == nil || s.cmd == nil {
		return Outcome{StartErr: errors.New("no session")}
	}
	defer func() {
		signal.Stop(s.interrupt)
		close(s.interrupt)
		signal.Reset(os.Interrupt)
	}()
	err := s.cmd.Wait()
	dur := s.clock.Now().Sub(s.started)
	o := Outcome{Duration: dur}
	if err == nil {
		return o
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		ws, ok := ee.Sys().(syscall.WaitStatus)
		if ok && ws.Signaled() {
			o.Signaled = true
			o.ExitCode = 128 + int(ws.Signal())
			return o
		}
		o.ExitCode = ee.ExitCode()
		return o
	}
	o.StartErr = err
	return o
}
