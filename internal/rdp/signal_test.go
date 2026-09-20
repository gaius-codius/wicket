package rdp

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/testutil"
)

func testPlan(t *testing.T) Plan {
	t.Helper()
	p := config.Profile{Name: "n", Host: "h", User: "u", Client: "sdl-freerdp3", Scale: 100}
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// The TUI keeps its own SIGINT handler for the whole run. Session.Wait must
// unregister only its own channel, or the next Ctrl+C kills Wicket outright
// and the terminal is never restored.
func TestWait_LeavesOtherSignalHandlersInstalled(t *testing.T) {
	outer := make(chan os.Signal, 1)
	signal.Notify(outer, os.Interrupt)
	defer signal.Stop(outer)

	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	t.Setenv("FAKERDP_EXIT", "0")

	l := &Launcher{Stdout: io.Discard, Stderr: io.Discard}
	sess, err := l.Start(testPlan(t), lineCred("pw"))
	if err != nil {
		t.Fatal(err)
	}
	sess.Wait()

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-outer:
	case <-time.After(2 * time.Second):
		// Only reachable if the default disposition was restored and the
		// process somehow survived; a real Reset kills the test binary here.
		t.Fatal("SIGINT no longer reaches a handler installed before the session")
	}
}

// Ctrl+C in the TUI reaches the client through its process group.
func TestStart_ForwardsInterruptToChildGroup(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	t.Setenv("FAKERDP_SIGINT_HOLD", "1")

	l := &Launcher{Stdout: io.Discard, Stderr: io.Discard}
	sess, err := l.Start(testPlan(t), lineCred("pw"))
	if err != nil {
		t.Fatal(err)
	}

	// The child installs its handler after reading stdin; retry until the
	// signal lands rather than racing it once.
	done := make(chan Outcome, 1)
	go func() { done <- sess.Wait() }()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case o := <-done:
			if o.StartErr != nil {
				t.Fatalf("start error: %v", o.StartErr)
			}
			if o.ExitCode != 0 {
				t.Fatalf("exit %d, want the child to exit cleanly on SIGINT", o.ExitCode)
			}
			return
		case <-deadline:
			t.Fatal("child did not exit after SIGINT")
		case <-time.After(50 * time.Millisecond):
			_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		}
	}
}

type errCred struct{ err error }

func (c errCred) WriteLine(io.Writer) error { return c.err }

// A password that cannot be written must fail the start, not leave the client
// waiting on a prompt it can never satisfy.
func TestStart_CredentialWriteFailureStopsTheClient(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	t.Setenv("FAKERDP_SLEEP", "30s")

	want := errors.New("boom")
	l := &Launcher{Stdout: io.Discard, Stderr: io.Discard}
	sess, err := l.Start(testPlan(t), errCred{want})
	if sess != nil {
		t.Fatal("session returned despite a credential write failure")
	}
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want it to wrap %v", err, want)
	}
}

// The password is written before the child is known to be reading, so a client
// that is slow to read stdin must not stall the launch.
func TestStart_DoesNotBlockWhenTheClientDelaysReadingStdin(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	t.Setenv("FAKERDP_DELAY_STDIN", "1s")
	t.Setenv("FAKERDP_EXIT", "0")

	l := &Launcher{Stdout: io.Discard, Stderr: io.Discard}
	started := make(chan *Session, 1)
	go func() {
		s, err := l.Start(testPlan(t), lineCred(strings.Repeat("x", 4096)))
		if err != nil {
			t.Error(err)
			close(started)
			return
		}
		started <- s
	}()
	select {
	case s, ok := <-started:
		if !ok {
			t.Fatal("start failed")
		}
		s.Wait()
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Start blocked writing the password while the client was not reading")
	}
}
