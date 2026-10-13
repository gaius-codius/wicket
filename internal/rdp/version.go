package rdp

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// An unsuffixed client name says nothing about its version: a Linux
// distribution's xfreerdp can be FreeRDP 2, which Wicket does not support,
// while Homebrew's is FreeRDP 3. Such a client is only offered once it says
// it is FreeRDP 3. Both 2.x and 3.x print a line such as "This is FreeRDP
// version 3.31.1 (63b948ca5c)" for --version; the revision in brackets
// differs between builds, so only the leading major version is read.

// versionTimeout bounds a version probe. FreeRDP answers --version without
// connecting to anything, so a client still running after this is not one
// to offer.
const versionTimeout = 3 * time.Second

// versionOutputLimit caps what a probe keeps of a client's output: the
// version line is short, and a binary that prints without end must not fill
// memory until the timeout.
const versionOutputLimit = 64 << 10

// versionLine needs a minor version after the major, so a truncated or
// mangled line such as "version 3." does not pass for one.
var versionLine = regexp.MustCompile(`This is FreeRDP version (\d{1,4})\.\d`)

// ParseFreeRDPMajor reads the major version from a client's --version
// output. ok is false when the output holds no version line.
func ParseFreeRDPMajor(out []byte) (major int, ok bool) {
	m := versionLine.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	major, err := strconv.Atoi(string(m[1]))
	return major, err == nil
}

// FreeRDPMajor runs the client at path with --version and returns the major
// version it reports. ok is false when it cannot be run, does not exit
// within versionTimeout, or prints no version line. Its exit status is not
// read: a build may report its version and still exit non-zero.
func FreeRDPMajor(path string) (major int, ok bool) {
	return FreeRDPMajorContext(context.Background(), path)
}

// FreeRDPMajorContext is FreeRDPMajor, also stopped, with everything the
// client started, when ctx is done. The client runs in a process group of
// its own, out of reach of a Ctrl+C typed at the terminal, so a caller with
// no handler of its own cancels ctx on the signal instead.
func FreeRDPMajorContext(ctx context.Context, path string) (major int, ok bool) {
	return probeMajor(ctx, path, versionTimeout)
}

// probeWaitExited is waitExited; a test stands in for a system where it
// fails.
var probeWaitExited = waitExited

func probeMajor(ctx context.Context, path string, timeout time.Duration) (int, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	out := &cappedBuffer{limit: versionOutputLimit}
	cmd.Stdout, cmd.Stderr = out, out
	// In a group of its own, the client goes together with whatever it
	// starts: a wrapper script that runs FreeRDP without exec, or a helper.
	// Killing only the client left those running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var (
		mu sync.Mutex
		// reaped is set just before Wait collects the client.
		reaped bool
		// owned is true while nothing can collect the client unseen, so its
		// pid, and with it the group id, is not free for reuse.
		owned = true
	)
	stop := func() error {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case reaped:
		case owned:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		default:
			// Wait may already have collected the client, and a group id
			// may be reused once it has. Process.Kill knows when its
			// process is gone, so the client at least is stopped.
			_ = cmd.Process.Kill()
		}
		return nil
	}
	cmd.Cancel = stop
	// A child that left the group could still hold the output open after
	// the client exits; WaitDelay stops Wait waiting on it.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return 0, false
	}
	exited := probeWaitExited(cmd.Process.Pid)
	mu.Lock()
	if exited {
		// Anything left in the group is a helper; it goes with the client,
		// while the group id is still ours, and stops holding the output.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		reaped = true
	} else {
		// Without the wait, nothing says when Wait collects the client:
		// from here the deadline stops the client alone.
		owned = false
	}
	mu.Unlock()
	_ = cmd.Wait()
	if cmd.ProcessState == nil {
		// Wait could not collect the client, so nothing says how it ended.
		return 0, false
	}
	// A client ended by SIGKILL is taken to be the one the deadline stopped.
	// A client that exited on its own is read, even past the deadline, when
	// a helper that left its group kept the output open after it.
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL {
		return 0, false
	}
	return ParseFreeRDPMajor(out.buf.Bytes())
}

// cappedBuffer keeps the first limit bytes written to it and drops the rest
// without failing the write, so the process is not killed by a broken pipe
// before it exits on its own.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
