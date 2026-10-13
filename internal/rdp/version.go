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
	return probeMajor(path, versionTimeout)
}

func probeMajor(path string, timeout time.Duration) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	out := &cappedBuffer{limit: versionOutputLimit}
	cmd.Stdout, cmd.Stderr = out, out
	// In a group of its own, the client goes together with whatever it
	// starts: a wrapper script that runs FreeRDP without exec, or a helper.
	// Killing only the client left those running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var (
		mu     sync.Mutex
		reaped bool
	)
	stop := func() error {
		mu.Lock()
		defer mu.Unlock()
		// Once the client is collected the group id may be reused, so the
		// group is signalled only before then.
		if !reaped {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
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
	if waitExited(cmd.Process.Pid) {
		// Anything left in the group is a helper; it goes with the client,
		// while the group id is still ours, and stops holding the output.
		_ = stop()
		mu.Lock()
		reaped = true
		mu.Unlock()
	}
	_ = cmd.Wait()
	// A client ended by SIGKILL is taken to be the one the deadline stopped.
	// A client that exited on its own is read, even near the deadline, and
	// even if a helper it left behind kept the output open after it.
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
