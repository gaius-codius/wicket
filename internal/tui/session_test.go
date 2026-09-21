package tui

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
)

// trapClient makes the fake client ignore SIGINT and log every signal it
// gets, with a helper in its process group doing the same. It returns the
// log and the helper's pid file.
func trapClient(t *testing.T) (log, helperPID string) {
	t.Helper()
	_ = withFakeRDP(t)
	dir := t.TempDir()
	log = filepath.Join(dir, "signals")
	helperPID = filepath.Join(dir, "helper.pid")
	t.Setenv("FAKERDP_TRAP", log)
	t.Setenv("FAKERDP_TRAP_READY", filepath.Join(dir, "ready"))
	t.Setenv("FAKERDP_SPAWN", "1")
	t.Setenv("FAKERDP_HELPER_PID", helperPID)
	return log, helperPID
}

func waitFile(t *testing.T, path string) string {
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

func waitLog(t *testing.T, path string, want ...string) {
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

// running reports whether pid is a live process rather than gone or a zombie.
func running(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] != 'Z'
}

func waitStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for running(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("pid %d outlived Wicket", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startSession connects the stored-password profile "work" and returns the
// model showing the running session, with the commands Bubble Tea would run.
func startSession(t *testing.T, h *harness) (Model, tea.Cmd) {
	t.Helper()
	p, _ := h.m.app.Cfg.Profile("work")
	if err := h.store.Upsert(secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel)); err != nil {
		t.Fatal(err)
	}
	nm, cmd := h.m.Update(keyMsg("enter"))
	m := nm.(Model)
	if m.session == nil || m.view != viewSession {
		t.Fatalf("no session: view %v status %q", m.view, m.status)
	}
	// Whatever the test does, the client does not outlive it.
	t.Cleanup(func() { m.app.StopSession(time.Second) })
	return m, cmd
}

func updateKey(m Model, k string) (Model, tea.Cmd) {
	nm, cmd := m.Update(keyMsg(k))
	return nm.(Model), cmd
}

func TestSession_ViewShowsTheSessionAndTicks(t *testing.T) {
	_ = withFakeRDP(t)
	t.Setenv("FAKERDP_SIGINT_HOLD", "1")
	h := newHarness(t, fixtureTOML("work", "192.168.1.20", "jdoe"), secret.NewMemory())
	opened := time.Date(2026, 9, 21, 14, 2, 0, 0, time.Local)
	now := opened
	h.m.now = func() time.Time { return now }
	m, cmd := startSession(t, h)
	defer func() { m, _ = updateKey(m, "ctrl+c"); settle(m, cmd) }()

	out := screen(m)
	for _, want := range []string{
		"session open",
		"● Connected to work",
		"jdoe@192.168.1.20 · opened 14:02 · elapsed 00:00:00",
		"The session runs in its own window.",
		"ctrl+c stop session",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// The footer offers nothing but stopping the session.
	for _, gone := range []string{"enter", "quit", "help", "new"} {
		if strings.Contains(out, gone) {
			t.Fatalf("session footer offers %q:\n%s", gone, out)
		}
	}

	now = opened.Add(time.Hour + 65*time.Second)
	nm, next := m.Update(sessionTickMsg{id: m.session.id})
	m = nm.(Model)
	if next == nil {
		t.Fatal("a tick for the running session must schedule the next")
	}
	if out := screen(m); !strings.Contains(out, "elapsed 01:01:05") {
		t.Fatalf("elapsed did not tick:\n%s", out)
	}
	if _, next := m.Update(sessionTickMsg{id: m.session.id + 1}); next != nil {
		t.Fatal("a tick for another session kept ticking")
	}
}

// While the session runs only ctrl+c does anything: a key meant for the list
// must not start a second session, open a form or quit. Resizing still works.
func TestSession_OtherKeysAreIgnored(t *testing.T) {
	_ = withFakeRDP(t)
	t.Setenv("FAKERDP_SIGINT_HOLD", "1")
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	m, cmd := startSession(t, h)
	defer func() { m, _ = updateKey(m, "ctrl+c"); settle(m, cmd) }()
	for _, k := range []string{"enter", "q", "n", "e", "D", "?", "/", "esc", "j"} {
		var c tea.Cmd
		m, c = updateKey(m, k)
		if m.view != viewSession || m.quit || c != nil {
			t.Fatalf("%q acted during a session: view %v quit %v cmd %v", k, m.view, m.quit, c != nil)
		}
	}
	nm, _ := m.Update(teaWin(100, 30))
	m = nm.(Model)
	if m.width != 100 || m.height != 30 {
		t.Fatal("resize ignored during a session")
	}
}

// Ctrl+C in raw mode is a key. The first sends SIGINT to the client's whole
// process group and leaves the view up; the second escalates to SIGTERM.
// A session the user stopped goes back to the list, not to the retry offer.
func TestSession_CtrlCStopsTheGroupAndEscalates(t *testing.T) {
	log, helperPID := trapClient(t)
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	m, cmd := startSession(t, h)
	waitFile(t, os.Getenv("FAKERDP_TRAP_READY"))
	waitFile(t, helperPID)

	m, esc := updateKey(m, "ctrl+c")
	if esc == nil {
		t.Fatal("a stop must schedule its own escalation")
	}
	waitLog(t, log, "client:interrupt", "helper:interrupt")
	if m.quit || m.view != viewSession {
		t.Fatalf("ctrl+c left the session view: view %v quit %v", m.view, m.quit)
	}
	out := screen(m)
	if !strings.Contains(out, "Stopping session…") || !strings.Contains(out, "ctrl+c force stop") {
		t.Fatalf("no stopping state:\n%s", out)
	}
	if strings.Contains(string(mustRead(t, log)), "terminated") {
		t.Fatal("the first ctrl+c went past SIGINT")
	}

	m, _ = updateKey(m, "ctrl+c")
	waitLog(t, log, "client:terminated")
	m = settle(m, cmd)
	if m.view != viewList {
		t.Fatalf("view %v after a stopped session, want the list", m.view)
	}
	if m.statusKind == statusError || !strings.Contains(m.status, "session stopped") {
		t.Fatalf("status %q", m.status)
	}
}

// A client that ignores the first ctrl+c is escalated without another
// keypress, once it has had stopGrace to act.
func TestSession_StopEscalatesOnItsOwn(t *testing.T) {
	log, _ := trapClient(t)
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	m, cmd := startSession(t, h)
	waitFile(t, os.Getenv("FAKERDP_TRAP_READY"))
	m, _ = updateKey(m, "ctrl+c")
	waitLog(t, log, "client:interrupt")

	// A timer from a stop the user has since pushed further is stale.
	nm, _ := m.Update(sessionEscalateMsg{id: m.session.id, sig: syscall.SIGTERM})
	m = nm.(Model)
	if m.session.stopping != syscall.SIGINT {
		t.Fatalf("stale timer escalated to %v", m.session.stopping)
	}

	nm, _ = m.Update(sessionEscalateMsg{id: m.session.id, sig: syscall.SIGINT})
	m = nm.(Model)
	waitLog(t, log, "client:terminated")
	settle(m, cmd)
}

// A SIGINT sent to Wicket is ctrl+c by another route while a session runs.
// SIGTERM quits, and quitting stops the client and everything in its group.
func TestSession_SignalsToWicket(t *testing.T) {
	log, helperPID := trapClient(t)
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	m, _ := startSession(t, h)
	waitFile(t, os.Getenv("FAKERDP_TRAP_READY"))
	pid, _ := strconv.Atoi(waitFile(t, helperPID))

	nm, _ := m.Update(signalMsg{sig: syscall.SIGINT})
	m = nm.(Model)
	waitLog(t, log, "client:interrupt")
	if m.quit || m.view != viewSession {
		t.Fatal("SIGINT during a session quit Wicket")
	}

	nm, cmd := m.Update(signalMsg{sig: syscall.SIGTERM})
	m = nm.(Model)
	if !m.quit || cmd == nil {
		t.Fatal("SIGTERM did not quit")
	}
	s := m.session.s
	m.app.StopSession(time.Second)
	select {
	case <-s.rdp.Done():
	default:
		t.Fatal("StopSession returned with the client running")
	}
	waitStopped(t, pid)
}

// A client that cannot be started goes to the retry offer, as it always
// has, and is not recorded as used.
func TestSession_StartErrorOffersRetry(t *testing.T) {
	_ = withFakeRDP(t)
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	h.m.app.Launcher.Runner = brokenRunner{}
	p, _ := h.m.app.Cfg.Profile("work")
	_ = h.store.Upsert(secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
	h.m = press(h.m, "enter")
	if h.m.view != viewRetry || h.m.retry.class != rdp.ClassStartError {
		t.Fatalf("view %v class %v, want the retry offer for a start error", h.m.view, h.m.retry.class)
	}
	if h.m.session != nil {
		t.Fatal("a session that never started is still up")
	}
	if _, ok := h.m.app.State.LastUsed("work"); ok {
		t.Fatal("a client that never started was recorded as used")
	}
	assertNoSentinel(t, []byte(screen(h.m)), "screen")
}

// brokenRunner finds the client but cannot run it.
type brokenRunner struct{ rdp.OSRunner }

func (brokenRunner) Command(string, ...string) *exec.Cmd {
	return exec.Command(filepath.Join(os.TempDir(), "wicket-no-such-client"))
}

// A session that ends within seconds goes to the retry offer, and shows
// what the client said.
func TestSession_ShortSessionOffersRetry(t *testing.T) {
	_ = withFakeRDP(t)
	t.Setenv("FAKERDP_EXIT", "131")
	t.Setenv("FAKERDP_OUTPUT", "\x1b[31m[12:00:00:000] [1:2] [ERROR][com.freerdp.core] - ERRCONNECT_CONNECT_FAILED\x1b[0m\x07")
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	m, cmd := startSession(t, h)
	m = settle(m, cmd)
	if m.view != viewRetry || m.retry.class != rdp.ClassShortSession {
		t.Fatalf("view %v class %v", m.view, m.retry.class)
	}
	out := m.View().Content
	if !strings.Contains(stripANSI(out), "client: ERRCONNECT_CONNECT_FAILED") {
		t.Fatalf("client note missing:\n%s", stripANSI(out))
	}
	if strings.Contains(out, "\x07") || strings.Contains(out, "[31m") {
		t.Fatalf("client control characters reached the screen: %q", out)
	}
}

// A client that echoes its stdin would put the password in its output. It
// is captured, never drawn.
func TestSession_EchoedPasswordNeverDrawn(t *testing.T) {
	_ = withFakeRDP(t)
	t.Setenv("FAKERDP_ECHO_STDIN", "1")
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	m, cmd := startSession(t, h)
	m = settle(m, cmd)
	if m.view != viewRetry {
		t.Fatalf("view %v", m.view)
	}
	assertNoSentinel(t, []byte(m.View().Content), "screen")
	assertNoSentinel(t, []byte(m.retry.note), "retry note")
	assertNoSentinel(t, []byte(m.status), "status")
	assertNoSentinel(t, h.stdout.Bytes(), "terminal")
}

// Quitting Wicket while a session runs stops the client and its helpers,
// and nothing the client writes reaches Wicket's output. This runs the real
// program loop, headless, and delivers a real SIGTERM to the process.
func TestRun_QuitStopsTheSessionAndKeepsItsOutput(t *testing.T) {
	_, helperPID := trapClient(t)
	t.Setenv("FAKERDP_OUTPUT", "client-log-line")
	t.Setenv("FAKERDP_ECHO_STDIN", "1")
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	p, _ := h.m.app.Cfg.Profile("work")
	_ = h.store.Upsert(secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))

	inR, inW := io.Pipe()
	defer inW.Close()
	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- runProgram(h.m, tea.WithInput(inR), tea.WithOutput(&out), tea.WithWindowSize(80, 24))
	}()
	if _, err := inW.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	waitFile(t, os.Getenv("FAKERDP_TRAP_READY"))
	pid, _ := strconv.Atoi(waitFile(t, helperPID))
	// Let the session view draw before quitting.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stripANSI(out.String()), "Connected to work") {
		if time.Now().After(deadline) {
			t.Fatalf("session view never drawn:\n%q", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wicket did not quit on SIGTERM")
	}
	waitStopped(t, pid)
	got := out.String()
	if strings.Contains(got, "client-log-line") {
		t.Fatalf("client output reached Wicket's screen:\n%q", got)
	}
	assertNoSentinel(t, []byte(got), "program output")
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return b
}

func TestClockDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                     "00:00:00",
		-time.Second:                          "00:00:00",
		59*time.Second + 900*time.Millisecond: "00:00:59",
		26*time.Hour + 3*time.Minute + 4*time.Second: "26:03:04",
	} {
		if got := clockDuration(d); got != want {
			t.Errorf("clockDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

// withSession puts m in the session view for profile p without a client,
// for tests that only draw it.
func withSession(m Model, p config.Profile, stopping bool) Model {
	m.session = &sessionState{id: 1, profile: p, opened: time.Now()}
	m.view = viewSession
	if stopping {
		m.session.stopping = syscall.SIGINT
		m.setStatus(stoppingStatus(syscall.SIGINT), statusInfo)
	}
	return m
}
