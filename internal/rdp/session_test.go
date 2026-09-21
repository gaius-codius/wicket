package rdp

import (
	"bytes"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/testutil"
)

// trapEnv starts clients that log every signal they receive and ignore
// SIGINT, with a helper in the same process group doing the same.
func trapEnv(t *testing.T) (log, helperPID string) {
	t.Helper()
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	dir := t.TempDir()
	log = filepath.Join(dir, "signals")
	helperPID = filepath.Join(dir, "helper.pid")
	ready := filepath.Join(dir, "ready")
	t.Setenv("FAKERDP_TRAP", log)
	t.Setenv("FAKERDP_TRAP_READY", ready)
	t.Setenv("FAKERDP_SPAWN", "1")
	t.Setenv("FAKERDP_HELPER_PID", helperPID)
	return log, helperPID
}

func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

func waitForLog(t *testing.T, path string, want ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(path)
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(string(b), w)
		}
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, _ := os.ReadFile(path)
	t.Fatalf("signal log %q, want %q", b, want)
}

// alive reports whether pid still names a running process. A zombie counts
// as gone: it has stopped, and only its parent's reaping is left.
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The state follows the parenthesised command name.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] != 'Z'
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("pid %d outlived its session", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func startOwned(t *testing.T, l *Launcher) *Session {
	t.Helper()
	l.OwnSignals = true
	sess, err := l.Start(testPlan(t), lineCred("pw"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Terminate(time.Second) })
	return sess
}

// Interrupt reaches the whole process group, not just the client, and a
// client that ignores it can still be stopped by escalating.
func TestSession_InterruptThenEscalate(t *testing.T) {
	log, helperPID := trapEnv(t)
	sess := startOwned(t, &Launcher{Stdout: NewTail(0), Stderr: NewTail(0)})
	waitForFile(t, os.Getenv("FAKERDP_TRAP_READY"))
	waitForFile(t, helperPID)

	if !sess.Interrupt() {
		t.Fatal("Interrupt did not signal a running client")
	}
	waitForLog(t, log, "client:interrupt", "helper:interrupt")
	select {
	case <-sess.Done():
		t.Fatal("a client that ignores SIGINT exited on it")
	case <-time.After(100 * time.Millisecond):
	}

	if sig := sess.Escalate(); sig != syscall.SIGTERM {
		t.Fatalf("escalated with %v, want SIGTERM after SIGINT", sig)
	}
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client did not exit on SIGTERM")
	}
	waitForLog(t, log, "client:terminated")
}

// Escalate runs SIGINT, SIGTERM, SIGKILL in order when nothing has been
// sent before it.
func TestSession_EscalateOrder(t *testing.T) {
	_, _ = trapEnv(t)
	t.Setenv("FAKERDP_STUBBORN", "1")
	sess := startOwned(t, &Launcher{Stdout: NewTail(0), Stderr: NewTail(0)})
	waitForFile(t, os.Getenv("FAKERDP_TRAP_READY"))
	for _, want := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL} {
		if got := sess.Escalate(); got != want {
			t.Fatalf("Escalate sent %v, want %v", got, want)
		}
	}
	o := sess.Wait()
	if !o.Signaled || o.ExitCode != 128+int(syscall.SIGKILL) {
		t.Fatalf("outcome %+v, want killed", o)
	}
}

// Terminate does not leave a client that ignores SIGTERM running: it
// follows up with SIGKILL, and returns once the client is gone.
func TestSession_TerminateKillsAStubbornClient(t *testing.T) {
	_, helperPID := trapEnv(t)
	t.Setenv("FAKERDP_STUBBORN", "1")
	sess := startOwned(t, &Launcher{Stdout: NewTail(0), Stderr: NewTail(0)})
	waitForFile(t, os.Getenv("FAKERDP_TRAP_READY"))
	pid, _ := strconv.Atoi(waitForFile(t, helperPID))

	sess.Terminate(200 * time.Millisecond)
	select {
	case <-sess.Done():
	default:
		t.Fatal("Terminate returned with the client still running")
	}
	waitGone(t, pid)
}

// A client that exits when asked to stop does not leave the rest of its
// group behind.
func TestSession_StopTakesTheGroupWithIt(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	helperPID := filepath.Join(t.TempDir(), "helper.pid")
	t.Setenv("FAKERDP_SIGINT_HOLD", "1")
	t.Setenv("FAKERDP_SPAWN", "1")
	t.Setenv("FAKERDP_HELPER_PID", helperPID)
	sess := startOwned(t, &Launcher{Stdout: NewTail(0), Stderr: NewTail(0)})
	pid, _ := strconv.Atoi(waitForFile(t, helperPID))

	// The client exits on SIGINT; the helper ignores it.
	deadline := time.After(10 * time.Second)
	for done := false; !done; {
		sess.Interrupt()
		select {
		case <-sess.Done():
			done = true
		case <-deadline:
			t.Fatal("client did not exit on SIGINT")
		case <-time.After(50 * time.Millisecond):
		}
	}
	waitGone(t, pid)
}

// Once the client has been collected its pid is not Wicket's to signal.
// (The race this guards, a reused pid, cannot be staged here; this pins the
// behaviour callers see.)
func TestSession_SignalsAfterExitAreDropped(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	t.Setenv("FAKERDP_EXIT", "0")
	sess := startOwned(t, &Launcher{Stdout: NewTail(0), Stderr: NewTail(0)})
	sess.Wait()
	if sess.Interrupt() {
		t.Fatal("Interrupt signalled a collected client")
	}
	if sig := sess.Escalate(); sig != 0 {
		t.Fatalf("Escalate sent %v to a collected client", sig)
	}
}

// With OwnSignals the caller decides what a SIGINT means; Start must not
// forward it behind the caller's back.
func TestSession_OwnSignalsDoesNotForward(t *testing.T) {
	outer := make(chan os.Signal, 1)
	signal.Notify(outer, os.Interrupt)
	defer signal.Stop(outer)

	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	t.Setenv("FAKERDP_SIGINT_HOLD", "1")
	sess := startOwned(t, &Launcher{Stdout: NewTail(0), Stderr: NewTail(0)})
	time.Sleep(200 * time.Millisecond)
	for i := 0; i < 3; i++ {
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		<-outer
	}
	select {
	case <-sess.Done():
		t.Fatal("SIGINT to Wicket was forwarded to the client")
	case <-time.After(300 * time.Millisecond):
	}
}

// A helper that keeps the client's output open must not hold Wait past the
// launcher's WaitDelay, and a clean exit is still reported as one rather
// than as a failure to start.
func TestSession_WaitDelayReportsTheClientsExit(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	helperPID := filepath.Join(t.TempDir(), "helper.pid")
	t.Setenv("FAKERDP_SPAWN", "1")
	t.Setenv("FAKERDP_HELPER_PID", helperPID)
	t.Setenv("FAKERDP_EXIT", "0")
	tail := NewTail(0)
	l := &Launcher{Stdout: tail, Stderr: tail, WaitDelay: 200 * time.Millisecond}
	sess := startOwned(t, l)
	pid, _ := strconv.Atoi(waitForFile(t, helperPID))
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	done := make(chan Outcome, 1)
	go func() { done <- sess.Wait() }()
	select {
	case o := <-done:
		if o.StartErr != nil || o.ExitCode != 0 {
			t.Fatalf("outcome %+v, want a clean exit", o)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait held open by the helper's copy of the output")
	}
}

// Nothing a session starts outlives it: the reaper and the SIGINT forwarder
// both end with the client.
func TestSession_LeavesNoGoroutines(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	t.Setenv("FAKERDP_EXIT", "0")
	runtime.GC()
	before := runtime.NumGoroutine()
	for _, own := range []bool{false, true} {
		tail := NewTail(0)
		l := &Launcher{Stdout: tail, Stderr: tail, OwnSignals: own, WaitDelay: time.Second}
		sess, err := l.Start(testPlan(t), lineCred("pw"))
		if err != nil {
			t.Fatal(err)
		}
		sess.Wait()
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines, was %d:\n%s", runtime.NumGoroutine(), before, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The client's output goes to the launcher's writers and never mixes with
// the password, which only ever travels on the client's stdin.
func TestSession_OutputCapturedWithoutThePassword(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	t.Setenv("FAKERDP_OUTPUT", "hello from the client")
	const pw = "pw-SENTINEL"
	tail := NewTail(0)
	l := &Launcher{Stdout: tail, Stderr: tail, OwnSignals: true, WaitDelay: time.Second}
	sess, err := l.Start(testPlan(t), lineCred(pw))
	if err != nil {
		t.Fatal(err)
	}
	sess.Wait()
	got := tail.Bytes()
	if !bytes.Contains(got, []byte("stdout: hello")) || !bytes.Contains(got, []byte("stderr: hello")) {
		t.Fatalf("captured %q", got)
	}
	if bytes.Contains(got, []byte(pw)) {
		t.Fatalf("password in captured output: %q", got)
	}
}

func TestTail_KeepsTheLastBytes(t *testing.T) {
	t.Parallel()
	tl := NewTail(8)
	for _, s := range []string{"abc", "defg", "hij"} {
		if n, err := tl.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if got := string(tl.Bytes()); got != "cdefghij" {
		t.Fatalf("tail %q", got)
	}
	if _, err := tl.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if got := string(tl.Bytes()); got != "23456789" {
		t.Fatalf("tail after an oversized write %q", got)
	}
	if NewTail(0).limit != OutputLimit {
		t.Fatal("default limit")
	}
}
