package tui

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
)

// certHarness is a profile with a stored password and a fake client that
// exits with code.
func certHarness(t *testing.T, code string) (*harness, string, secret.Identity) {
	t.Helper()
	return certHarnessFor(t, "sdl-freerdp3", code)
}

func certHarnessFor(t *testing.T, client, code string) (*harness, string, secret.Identity) {
	t.Helper()
	rec := withFakeRDP(t)
	t.Setenv("FAKERDP_EXIT", code)
	store := secret.NewMemory()
	h := newHarness(t, strings.Replace(fixtureTOML("work", "h", "u"), `"sdl-freerdp3"`, `"`+client+`"`, 1), store)
	p, _ := h.m.app.Cfg.Profile("work")
	id := secret.IdentityFor(h.m.app.Cfg.Path(), p)
	if err := store.Upsert(bg, id, mustPassword(t, sentinel)); err != nil {
		t.Fatal(err)
	}
	return h, rec, id
}

// fakeTerminal replaces execTerminal with one that runs the command at once
// with a stdin that is a pipe, not a terminal. It returns how often it ran.
func fakeTerminal(t *testing.T) *int {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	calls := new(int)
	old := execTerminal
	t.Cleanup(func() { execTerminal = old })
	execTerminal = func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
		*calls++
		return func() tea.Msg {
			c.SetStdin(r)
			c.SetStdout(io.Discard)
			c.SetStderr(io.Discard)
			return fn(c.Run())
		}
	}
	return calls
}

// connectToOverlay connects from the list and follows the client to its end.
func connectToOverlay(t *testing.T, h *harness) {
	t.Helper()
	h.m = press(h.m, "enter")
	if h.m.view != viewRetry {
		t.Fatalf("want retry overlay, view=%v status=%q", h.m.view, h.m.status)
	}
}

func hasHint(m Model, key string) bool {
	_, hs := m.chrome()
	for _, h := range hs {
		if h.key == key {
			return true
		}
	}
	return false
}

// pressC presses c and runs what it returns the way Bubble Tea would,
// feeding back the message the terminal connect ends with.
func pressC(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := act(m, keyMsg("c"))
	if cmd == nil {
		return m
	}
	msg := cmd()
	if _, ok := msg.(terminalEndedMsg); !ok {
		t.Fatalf("c produced %T, want terminalEndedMsg", msg)
	}
	nm, _ := m.Update(msg)
	return nm.(Model)
}

func TestCertificate_OfferedOnlyAfterFreeRDPTLSFailure(t *testing.T) {
	tests := []struct {
		name, client, code string
		want               bool
	}{
		{"freerdp 143", "sdl-freerdp3", "143", true},
		{"freerdp 131", "sdl-freerdp3", "131", false},
		{"other client 143", "myrdp", "143", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := certHarnessFor(t, tc.client, tc.code)
			connectToOverlay(t, h)
			if h.m.retry.certificate != tc.want {
				t.Fatalf("retry.certificate = %v, want %v", h.m.retry.certificate, tc.want)
			}
			out := screen(h.m)
			if got := strings.Contains(out, "Press c to retry in the terminal"); got != tc.want {
				t.Fatalf("certificate text shown = %v, want %v:\n%s", got, tc.want, out)
			}
			if got := hasHint(h.m, "c"); got != tc.want {
				t.Fatalf("c key hint = %v, want %v", got, tc.want)
			}
		})
	}
}

// c on the overlay goes through runConnect and runInTerminal to the exec,
// and its callback back to applyConnect. The fake's stdin is a pipe, so the
// terminal connect cannot run the client.
func TestCertificate_CRetriesInTheTerminal(t *testing.T) {
	h, rec, _ := certHarness(t, "143")
	calls := fakeTerminal(t)
	connectToOverlay(t, h)
	_ = os.Remove(rec)
	h.m = pressC(t, h.m)
	if *calls != 1 {
		t.Fatalf("terminal exec ran %d times, want 1", *calls)
	}
	if h.m.view != viewRetry {
		t.Fatalf("view %v status %q, want the retry overlay", h.m.view, h.m.status)
	}
	if out := screen(h.m); !strings.Contains(out, "not running in the foreground") {
		t.Fatalf("no-terminal reason missing:\n%s", out)
	}
	if h.m.retry.certificate {
		t.Fatal("a connect that never started must not offer the certificate retry")
	}
	if _, err := os.Stat(rec); err == nil {
		t.Fatal("the client ran although there was no terminal")
	}
	if h.m.inTerminal != "" {
		t.Fatalf("inTerminal %q left set", h.m.inTerminal)
	}
}

func TestCertificate_CWithoutCertificateDoesNothing(t *testing.T) {
	h, rec, _ := certHarness(t, "131")
	calls := fakeTerminal(t)
	connectToOverlay(t, h)
	_ = os.Remove(rec)
	before := screen(h.m)
	m, cmd := act(h.m, keyMsg("c"))
	if cmd != nil {
		t.Fatal("c returned a command")
	}
	if *calls != 0 {
		t.Fatalf("terminal exec ran %d times", *calls)
	}
	if m.view != viewRetry || screen(m) != before {
		t.Fatalf("overlay changed: view %v", m.view)
	}
	if _, err := os.Stat(rec); err == nil {
		t.Fatal("the client ran")
	}
}

func TestCertificate_EnterRetriesNormally(t *testing.T) {
	h, rec, _ := certHarness(t, "143")
	calls := fakeTerminal(t)
	connectToOverlay(t, h)
	_ = os.Remove(rec)
	h.m = press(h.m, "enter")
	if *calls != 0 {
		t.Fatalf("enter went to the terminal (%d execs)", *calls)
	}
	got := testutil.ReadRecord(t, rec)
	if !got.HasPassword || got.Password != sentinel {
		t.Fatalf("password %q (present %v), want the sentinel", got.Password, got.HasPassword)
	}
}

// c that is stopped before the client starts (here by a password that has
// to be asked for again) must not make a later connect from the list go to
// the terminal.
func TestCertificate_StaleCDoesNotReachALaterConnect(t *testing.T) {
	h, rec, id := certHarness(t, "143")
	calls := fakeTerminal(t)
	connectToOverlay(t, h)
	if err := h.store.Delete(bg, id); err != nil {
		t.Fatal(err)
	}
	h.m = press(h.m, "c")
	if h.m.view != viewModal {
		t.Fatalf("view %v status %q, want the password prompt", h.m.view, h.m.status)
	}
	if h.m.inTerminal == "" {
		t.Fatal("test setup: c should still be pending behind the prompt")
	}
	h.m = press(h.m, "esc")
	if h.m.view != viewList {
		t.Fatalf("view %v after esc", h.m.view)
	}
	if err := h.store.Upsert(bg, id, mustPassword(t, sentinel)); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(rec)
	h.m = press(h.m, "enter")
	if *calls != 0 {
		t.Fatalf("a connect from the list went to the terminal (%d execs)", *calls)
	}
	got := testutil.ReadRecord(t, rec)
	if !got.HasPassword || got.Password != sentinel {
		t.Fatalf("password %q (present %v), want the sentinel", got.Password, got.HasPassword)
	}
}

func TestTerminalConnect_RunNeedsAFileStdin(t *testing.T) {
	c := &terminalConnect{}
	c.SetStdin(strings.NewReader(""))
	if err := c.Run(); !errors.Is(err, errNoTerminal) {
		t.Fatalf("Run = %v, want errNoTerminal", err)
	}
	if c.started {
		t.Fatal("started set although no client ran")
	}
}
