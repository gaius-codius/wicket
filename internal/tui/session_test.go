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
	if err := h.store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel)); err != nil {
		t.Fatal(err)
	}
	m, cmd := act(h.m, keyMsg("enter"))
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
		"● FreeRDP running for work",
		"jdoe@192.168.1.20 · started 14:02 · elapsed 00:00:00",
		"FreeRDP connects in its own window.",
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
	// Wicket sees the client start, not the connection: it must not claim
	// one while FreeRDP may still be failing to reach the host.
	if strings.Contains(out, "Connected") {
		t.Fatalf("the session view claims a connection Wicket cannot see:\n%s", out)
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
	_ = h.store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
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

// A client that fails goes to the retry offer, and shows what it said.
func TestSession_FailureOffersRetry(t *testing.T) {
	_ = withFakeRDP(t)
	t.Setenv("FAKERDP_EXIT", "131")
	t.Setenv("FAKERDP_OUTPUT", "\x1b[31m[12:00:00:000] [1:2] [ERROR][com.freerdp.core] - ERRCONNECT_CONNECT_FAILED\x1b[0m\x07")
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	m, cmd := startSession(t, h)
	m = settle(m, cmd)
	if m.view != viewRetry || m.retry.class != rdp.ClassFailed {
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

// A failure is a failure however long it takes. FreeRDP spends about fifteen
// seconds giving up on a host it cannot reach, and that used to come back as
// a plain "session ended", with no exit status and no error, looking like a
// session that had worked.
func TestSession_SlowFailureOffersRetry(t *testing.T) {
	_ = withFakeRDP(t)
	t.Setenv("FAKERDP_EXIT", "131")
	t.Setenv("FAKERDP_OUTPUT", "[12:00:16:000] [1:2] [ERROR][com.freerdp.core] - ERRCONNECT_CONNECT_FAILED [0x00020006]")
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	h.m.app.Launcher.Clock = &jumpClock{times: []time.Time{time.Unix(0, 0), time.Unix(16, 0)}}
	m, cmd := startSession(t, h)
	m = settle(m, cmd)
	if m.view != viewRetry || m.retry.class != rdp.ClassFailed {
		t.Fatalf("view %v class %v status %q, want the retry offer for a failure", m.view, m.retry.class, m.status)
	}
	out := screen(m)
	for _, want := range []string{"exited with status 131 after 16s", "client: ERRCONNECT_CONNECT_FAILED"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

// A session the user stopped is not a failure, however the client exited.
func TestSession_StoppedIsNotAFailure(t *testing.T) {
	log, _ := trapClient(t)
	t.Setenv("FAKERDP_STUBBORN", "1")
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	m, cmd := startSession(t, h)
	waitFile(t, os.Getenv("FAKERDP_TRAP_READY"))
	// Each press waits for the last signal to land: sent back to back, the
	// SIGKILL can beat the client's handler to logging the SIGTERM.
	m, _ = updateKey(m, "ctrl+c")
	waitLog(t, log, "client:interrupt")
	m, _ = updateKey(m, "ctrl+c")
	waitLog(t, log, "client:terminated")
	m, _ = updateKey(m, "ctrl+c")
	m = settle(m, cmd)
	if m.view != viewList || !strings.Contains(m.status, "session stopped") {
		t.Fatalf("view %v status %q, want a plain stop for a killed client", m.view, m.status)
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

// A password with an escape sequence in it is not drawn once the sequence
// has been stripped from the line that echoed it: the check against the
// cleaned line used to look for the password as stored, which cleaning had
// already changed.
func TestSession_EchoedEscapedPasswordNeverDrawn(t *testing.T) {
	_ = withFakeRDP(t)
	t.Setenv("FAKERDP_ECHO_STDIN", "1")
	escaped := sentinel[:3] + "\x1b[31m" + sentinel[3:]
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	p, _ := h.m.app.Cfg.Profile("work")
	if err := h.store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, escaped)); err != nil {
		t.Fatal(err)
	}
	m, cmd := act(h.m, keyMsg("enter"))
	m = settle(m, cmd)
	if m.view != viewRetry {
		t.Fatalf("view %v", m.view)
	}
	assertNoSentinel(t, []byte(stripANSI(m.View().Content)), "screen")
	assertNoSentinel(t, []byte(m.retry.note), "retry note")
}

// clientNote drops a line that carries the password before or after
// cleaning, and still finds the client's reason among the lines left.
func TestClientNote_DropsEveryFormOfThePassword(t *testing.T) {
	pw := mustPassword(t, "abc\x1b[31mdef")
	for _, out := range []string{
		"[ERROR] - ERRCONNECT_X\nechoed abc\x1b[31mdef\n",
		"[ERROR] - ERRCONNECT_X\nechoed abcdef\n",
		"[ERROR] - ERRCONNECT_X\n[ERROR] - failed for abc\x1b[31mdef\n",
	} {
		if got := clientNote([]byte(out), pw); got != "ERRCONNECT_X" {
			t.Errorf("clientNote(%q) = %q, want the line without the password", out, got)
		}
	}
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
	_ = h.store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))

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
	for !strings.Contains(stripANSI(out.String()), "FreeRDP running for work") {
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
		wantStopped(t, err, syscall.SIGTERM)
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

// Another Wicket holding the state lock cannot freeze a session: recording
// the last-used time waits for the lock, but drawing the session and
// stopping it with Ctrl+C do not. The time is recorded once the lock is free.
// This runs the real program loop, headless: starting used to record the
// time inside Update, so the loop blocked before the session was drawn or
// published for StopSession to find.
func TestRun_SessionRunsWhileTheStateLockIsHeld(t *testing.T) {
	_ = withFakeRDP(t)
	pidFile := filepath.Join(t.TempDir(), "client.pid")
	t.Setenv("FAKERDP_PID", pidFile)
	t.Setenv("FAKERDP_SIGINT_HOLD", "1")
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	p, _ := h.m.app.Cfg.Profile("work")
	_ = h.store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
	app := h.m.app
	t.Cleanup(func() { app.StopSession(time.Second) })

	lock, err := os.OpenFile(app.State.Path()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = lock.Close() }) }
	// Registered after StopSession, so it runs first: a test that fails
	// with the loop wedged on the lock must not hang there.
	t.Cleanup(release)

	inR, inW := io.Pipe()
	t.Cleanup(func() { _ = inW.Close() })
	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- runProgram(h.m, tea.WithInput(inR), tea.WithOutput(&out), tea.WithWindowSize(80, 24))
	}()
	waitOutput := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(stripANSI(out.String()), want) {
			if time.Now().After(deadline) {
				t.Fatalf("%q never drawn with the state lock held:\n%s", want, stripANSI(out.String()))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	if _, err := inW.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(waitFile(t, pidFile))
	waitOutput("FreeRDP running for work")

	if _, err := inW.Write([]byte{0x03}); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, pid)
	waitOutput("session stopped")
	if _, ok := app.State.LastUsed("work"); ok {
		t.Fatal("last-used written through a lock another process holds")
	}

	release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := app.State.LastUsed("work"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("last-used never recorded once the lock was free")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := inW.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wicket did not quit")
	}
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

// FreeRDP's exit code says how a session ended. A logoff or a closed window
// exits non-zero, and is a session that ended, not an error with a password
// hint; a failure says what failed, and only an authentication failure
// suggests the password.
func TestSession_FreeRDPExitCodes(t *testing.T) {
	for _, tc := range []struct {
		code   string
		view   view
		status string
		hint   bool
	}{
		{"2", viewList, "session ended: logged off", false},
		{"11", viewList, "session ended: disconnected by the user", false},
		{"141", viewRetry, "FreeRDP failed: could not connect", false},
		{"132", viewRetry, "FreeRDP failed: authentication failed", true},
		{"134", viewRetry, "FreeRDP failed: logon failed", true},
		{"99", viewRetry, "FreeRDP exited with an error", true},
	} {
		t.Run(tc.code, func(t *testing.T) {
			_ = withFakeRDP(t)
			t.Setenv("FAKERDP_EXIT", tc.code)
			h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
			h.m.app.Launcher.Clock = &jumpClock{times: []time.Time{time.Unix(0, 0), time.Unix(60, 0)}}
			m, cmd := startSession(t, h)
			m = settle(m, cmd)
			out := screen(m)
			if m.view != tc.view || !strings.Contains(out, tc.status) {
				t.Fatalf("view %v, want %v with %q:\n%s", m.view, tc.view, tc.status, out)
			}
			if tc.view == viewList && m.statusKind != statusInfo {
				t.Fatalf("status kind %v for a session that ended", m.statusKind)
			}
			if tc.view == viewRetry && !strings.Contains(out, "exited with status "+tc.code) {
				t.Fatalf("exit status missing:\n%s", out)
			}
			if got := strings.Contains(out, "If the password may be wrong"); got != tc.hint {
				t.Fatalf("password hint shown %v, want %v:\n%s", got, tc.hint, out)
			}
		})
	}
}

// Another client's exit codes mean something else, so they are not read
// through FreeRDP's table: exit 2 from it is a failure, as any non-zero
// status is.
func TestSession_OtherClientsExitCodesAreNotFreeRDPs(t *testing.T) {
	_ = withFakeRDP(t)
	t.Setenv("FAKERDP_EXIT", "2")
	h := newHarness(t, strings.Replace(fixtureTOML("work", "h", "u"), `client = "sdl-freerdp3"`, `client = "myrdp"`, 1), secret.NewMemory())
	m, cmd := startSession(t, h)
	m = settle(m, cmd)
	if m.view != viewRetry || m.retry.class != rdp.ClassFailed || strings.Contains(screen(m), "logged off") {
		t.Fatalf("view %v class %v:\n%s", m.view, m.retry.class, screen(m))
	}
}

// The line picked from the client's output is the one that names the error.
// FreeRDP's password reader logs a tcsetattr failure as an ERROR on the way
// out, after the real error, because its stdin is a pipe; it used to be what
// the retry view showed for every real failure.
func TestClientNote_PrefersTheErrorCodeOverTerminalNoise(t *testing.T) {
	out := strings.Join([]string{
		"Password: [00:44:58:825] [1:2] [ERROR][com.freerdp.utils.passphrase] - [set_termianl_nonblock]: tcsetattr(TCSANOW) failed with Inappropriate ioctl for device",
		"[00:45:13:961] [1:3] [ERROR][com.freerdp.core] - [get_next_addrinfo]: ERRCONNECT_CONNECT_FAILED [0x00020006]",
		"[00:45:13:961] [1:3] [ERROR][com.freerdp.core.nego] - [nego_connect]: Failed to connect",
		"[00:45:13:966] [1:2] [ERROR][com.freerdp.client.SDL] - [handleShow]: ERRCONNECT_CONNECT_FAILED [0x00020006]",
		"The connection failed.",
		"[00:45:13:970] [1:2] [ERROR][com.freerdp.core.transport] - [transport_default_write]: BIO_should_retry returned a system error 32: Broken pipe",
		"[00:45:14:001] [1:2] [ERROR][com.freerdp.utils.passphrase] - [restore_terminal]: tcsetattr(TCSANOW) failed with Inappropriate ioctl for device",
		"",
	}, "\n")
	if got := clientNote([]byte(out), nil); got != "ERRCONNECT_CONNECT_FAILED [0x00020006]" {
		t.Fatalf("clientNote = %q", got)
	}
	noise := "[1] [ERROR][com.freerdp.utils.passphrase] - [x]: tcsetattr(TCSANOW) failed\nsome last line\n"
	if got := clientNote([]byte(noise), nil); got != "some last line" {
		t.Fatalf("clientNote = %q, want the last line that is not terminal noise", got)
	}
}

// wantStopped checks that Run reported being ended by sig, so that Wicket
// exits 128 plus its number, as wicket connect does, rather than 0.
func wantStopped(t *testing.T, err error, sig syscall.Signal) {
	t.Helper()
	var stopped *StoppedError
	if !errors.As(err, &stopped) || stopped.Signal != sig || stopped.ExitStatus() != 128+int(sig) {
		t.Fatalf("run returned %v, want it stopped by %v", err, sig)
	}
}
