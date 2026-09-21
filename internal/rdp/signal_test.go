package rdp

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"strconv"
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

// Without OwnSignals -- wicket connect -- a SIGTERM or SIGHUP sent to Wicket
// stops the client's whole process group, escalating to SIGKILL for a client
// that ignores it, and the outcome says which signal it was so Wicket can
// exit as a program killed by it would. The client runs in a process group
// of its own, so before this a launcher that ended Wicket left FreeRDP and
// its helpers running.
func TestStart_ForwardsTermAndHangupToTheGroup(t *testing.T) {
	for _, tc := range []struct {
		sig      syscall.Signal
		stubborn bool
	}{
		{syscall.SIGTERM, false},
		{syscall.SIGHUP, true},
	} {
		t.Run(tc.sig.String(), func(t *testing.T) {
			// The test binary must survive the signal whether or not
			// Start forwards it, so a missing forwarder fails the test
			// rather than killing it.
			outer := make(chan os.Signal, 4)
			signal.Notify(outer, syscall.SIGTERM, syscall.SIGHUP)
			defer signal.Stop(outer)

			log, helperPID := trapEnv(t)
			if tc.stubborn {
				t.Setenv("FAKERDP_STUBBORN", "1")
			}
			l := &Launcher{Stdout: io.Discard, Stderr: io.Discard, StopGrace: 300 * time.Millisecond}
			sess, err := l.Start(testPlan(t), lineCred("pw"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { sess.Terminate(time.Second) })
			waitForFile(t, os.Getenv("FAKERDP_TRAP_READY"))
			pid, _ := strconv.Atoi(waitForFile(t, helperPID))

			if err := syscall.Kill(os.Getpid(), tc.sig); err != nil {
				t.Fatal(err)
			}
			select {
			case <-sess.Done():
			case <-time.After(5 * time.Second):
				t.Fatalf("client still running after Wicket got %v", tc.sig)
			}
			waitForLog(t, log, "client:terminated")
			waitGone(t, pid)
			o := sess.Wait()
			if o.Stopped != tc.sig {
				t.Fatalf("outcome %+v, want it stopped by %v", o, tc.sig)
			}
			if got, want := o.ExitStatus(), 128+int(tc.sig); got != want {
				t.Fatalf("exit status %d, want %d", got, want)
			}
		})
	}
}

// signalCred writes the password and then sends Wicket sig, as a launcher
// script or a closed terminal might while the client is still starting. It
// returns only once the signal has been delivered to the test's own handler,
// so the signal is not still in flight when Start carries on.
type signalCred struct {
	sig   syscall.Signal
	outer <-chan os.Signal
}

func (c signalCred) WriteLine(w io.Writer) error {
	if _, err := io.WriteString(w, "pw\n"); err != nil {
		return err
	}
	if err := syscall.Kill(os.Getpid(), c.sig); err != nil {
		return err
	}
	select {
	case <-c.outer:
	case <-time.After(5 * time.Second):
	}
	return nil
}

// A SIGTERM or SIGHUP that lands while Start is still running -- the client
// started, its password on the way -- stops the client's group too. The
// handler used to go in only once Start was done with the password, and a
// signal before that ended Wicket and left the client running.
func TestStart_TermDuringStartStopsTheGroup(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			// The test binary must survive the signal either way, so a
			// handler installed too late fails the test rather than
			// killing it.
			outer := make(chan os.Signal, 4)
			signal.Notify(outer, sig)
			defer signal.Stop(outer)

			testutil.PrependPATH(t, testutil.FakeRDPDir(t))
			t.Setenv("FAKERDP_SLEEP", "30s")
			l := &Launcher{Stdout: io.Discard, Stderr: io.Discard, StopGrace: 300 * time.Millisecond}
			sess, err := l.Start(testPlan(t), signalCred{sig: sig, outer: outer})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { sess.Terminate(time.Second) })
			select {
			case <-sess.Done():
			case <-time.After(5 * time.Second):
				t.Fatalf("client still running after Wicket got %v during Start", sig)
			}
			if o := sess.Wait(); o.Stopped != sig || o.ExitStatus() != 128+int(sig) {
				t.Fatalf("outcome %+v, want it stopped by %v", o, sig)
			}
		})
	}
}
