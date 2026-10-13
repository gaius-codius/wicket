package rdp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestParseFreeRDPMajor(t *testing.T) {
	for _, tc := range []struct {
		out   string
		major int
		ok    bool
	}{
		{"This is FreeRDP version 3.31.1 (63b948ca5c)\n", 3, true},
		{"This is FreeRDP version 2.11.7 (2.11.7)\n", 2, true},
		{"This is FreeRDP version 2.10.0 (n/a)\n", 2, true},
		// Log lines before the version do not hide it.
		{"[12:00:00:000] [1:1] [WARN][com.freerdp.core] - something\nThis is FreeRDP version 3.5.0 (dev)\n", 3, true},
		{"", 0, false},
		{"This is FreeRDP version\n", 0, false},
		{"This is FreeRDP version x.1\n", 0, false},
		{"This is FreeRDP version 3\n", 0, false},
		{"This is FreeRDP version 3.\n", 0, false},
		{"This is FreeRDP version 3.garbage\n", 0, false},
		{"xfreerdp: unknown option --version\n", 0, false},
		{"FreeRDP 3.31.1\n", 0, false},
	} {
		major, ok := ParseFreeRDPMajor([]byte(tc.out))
		if major != tc.major || ok != tc.ok {
			t.Errorf("%q: %d %v, want %d %v", tc.out, major, ok, tc.major, tc.ok)
		}
	}
}

// fakeClient writes a script that stands in for a FreeRDP client.
func fakeClient(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "xfreerdp")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFreeRDPMajor_RunsTheClient(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		major int
		ok    bool
	}{
		{"version 3", `[ "$1" = --version ] || exit 9; echo "This is FreeRDP version 3.31.1 (63b948ca5c)"`, 3, true},
		{"version 2", `echo "This is FreeRDP version 2.11.7 (2.11.7)"`, 2, true},
		// A build may report its version and still exit non-zero.
		{"version 3, exit 1", `echo "This is FreeRDP version 3.31.1 (63b948ca5c)"; exit 1`, 3, true},
		{"on stderr", `echo "This is FreeRDP version 3.0.0 (x)" >&2`, 3, true},
		{"malformed", `echo "This is FreeRDP version unknown"`, 0, false},
		{"silent failure", `exit 1`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			major, ok := FreeRDPMajor(fakeClient(t, tc.body))
			if major != tc.major || ok != tc.ok {
				t.Fatalf("%d %v, want %d %v", major, ok, tc.major, tc.ok)
			}
		})
	}
	if _, ok := FreeRDPMajor(filepath.Join(t.TempDir(), "missing")); ok {
		t.Fatal("a missing executable qualified")
	}
}

// A client that has printed a good version but does not exit in time is not
// one to offer.
func TestFreeRDPMajor_Timeout(t *testing.T) {
	path := fakeClient(t, `echo "This is FreeRDP version 3.31.1 (63b948ca5c)"; exec sleep 10`)
	start := time.Now()
	if _, ok := probeMajor(context.Background(), path, 200*time.Millisecond); ok {
		t.Fatal("a client that timed out qualified")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("probe took %v", d)
	}
}

// Output past the cap is dropped, not an error, and what was kept still reads.
func TestFreeRDPMajor_CapsOutput(t *testing.T) {
	path := fakeClient(t, `echo "This is FreeRDP version 3.1.0 (x)"; head -c 1000000 /dev/zero`)
	if major, ok := FreeRDPMajor(path); major != 3 || !ok {
		t.Fatalf("%d %v", major, ok)
	}
}

// readPID reads the pid a fake client wrote to path.
func readPID(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// A wrapper that runs the client without exec, and hangs, is stopped at the
// deadline together with everything it started.
func TestFreeRDPMajor_TimeoutStopsTheGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	path := fakeClient(t, `echo $$ > `+pidFile+`
echo "This is FreeRDP version 3.31.1 (63b948ca5c)"
sh -c 'sleep 30' &
sleep 30`)
	if _, ok := probeMajor(context.Background(), path, time.Second); ok {
		t.Fatal("a client that timed out qualified")
	}
	testutil.WaitGroupGone(t, readPID(t, pidFile))
}

// A client that answers and exits on its own is read, though a helper it
// leaves behind holds the output open past the deadline; the helper is
// stopped.
func TestFreeRDPMajor_HelperHoldsOutput(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	path := fakeClient(t, `echo $$ > `+pidFile+`
echo "This is FreeRDP version 3.31.1 (63b948ca5c)"
sleep 30 &
exit 0`)
	if major, ok := probeMajor(context.Background(), path, 300*time.Millisecond); major != 3 || !ok {
		t.Fatalf("%d %v, want 3 true", major, ok)
	}
	testutil.WaitGroupGone(t, readPID(t, pidFile))
}

// A client that answers and exits on its own is read even when Wait returns
// after the deadline, because a helper that left the client's group held the
// output open until WaitDelay gave up on it.
func TestFreeRDPMajor_AnswerReadAfterDeadline(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	path := fakeClient(t, `echo "This is FreeRDP version 3.31.1 (63b948ca5c)"
setsid sh -c 'echo $$ > `+pidFile+`; exec sleep 30' &
# Exit only once the helper has left the group, or it goes with the client.
while [ ! -s `+pidFile+` ]; do sleep 0.01; done
exit 0`)
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	start := time.Now()
	if major, ok := probeMajor(context.Background(), path, 300*time.Millisecond); major != 3 || !ok {
		t.Fatalf("%d %v, want 3 true", major, ok)
	}
	// Only a probe that outlived the deadline tests the rule.
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Fatalf("probe returned after %v, before the deadline", d)
	}
}

// Cancelling the context stops a hanging client and everything it started.
func TestFreeRDPMajorContext_CancelStopsTheGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	path := fakeClient(t, `echo $$ > `+pidFile+`
sh -c 'sleep 30' &
sleep 30`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			if _, err := os.Stat(pidFile); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	start := time.Now()
	if _, ok := FreeRDPMajorContext(ctx, path); ok {
		t.Fatal("a cancelled probe qualified")
	}
	if d := time.Since(start); d >= versionTimeout {
		t.Fatalf("probe ran %v, to its timeout", d)
	}
	testutil.WaitGroupGone(t, readPID(t, pidFile))
}

// Where the probe cannot wait for the client without collecting it, the
// deadline stops the client alone: once Wait has collected it, its group id
// may belong to someone else. Helpers the client started are left running.
func TestFreeRDPMajor_NoWaitKillsOnlyTheClient(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("needs /proc to tell a live helper from a killed one")
	}
	old := probeWaitExited
	probeWaitExited = func(int) bool { return false }
	t.Cleanup(func() { probeWaitExited = old })
	dir := t.TempDir()
	clientFile, helperFile := filepath.Join(dir, "client"), filepath.Join(dir, "helper")
	path := fakeClient(t, `echo $$ > `+clientFile+`
sleep 30 > /dev/null 2>&1 &
echo $! > `+helperFile+`
exec sleep 30`)
	if _, ok := probeMajor(context.Background(), path, time.Second); ok {
		t.Fatal("a client that timed out qualified")
	}
	helper := readPID(t, helperFile)
	t.Cleanup(func() { _ = syscall.Kill(helper, syscall.SIGKILL) })
	// A helper killed with the client would be gone, or a zombie, by now.
	time.Sleep(100 * time.Millisecond)
	if !testutil.Running(helper) {
		t.Fatal("the helper was stopped too")
	}
	if syscall.Kill(readPID(t, clientFile), 0) == nil {
		t.Fatal("the client is still there")
	}
}
