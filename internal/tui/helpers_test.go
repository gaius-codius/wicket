package tui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;]*[[:alpha:]]`)

func stripANSI(s string) string {
	return ansiEscapePattern.ReplaceAllString(s, "")
}

func teaWin(w, h int) tea.WindowSizeMsg {
	return tea.WindowSizeMsg{Width: w, Height: h}
}

func screen(m Model) string {
	return stripANSI(m.View().Content)
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "?":
		return tea.KeyPressMsg{Code: '?', Text: "?"}
	default:
		if k == "" {
			return tea.KeyPressMsg{}
		}
		r := []rune(k)[0]
		return tea.KeyPressMsg{Code: r, Text: k}
	}
}

// press sends keys one at a time. A key that starts a session is followed
// through to the session's end, as if the user had waited for the client to
// exit, so the model returned is the one they would see next; use Update
// directly to look at the session view itself.
func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var cmd tea.Cmd
		m, cmd = act(m, keyMsg(k))
		if m.session != nil {
			m = settle(m, cmd)
		}
	}
	return m
}

// act sends msg and, when that starts a keyring operation, runs it as Bubble
// Tea would and feeds the model its reply, until nothing is left waiting on
// the keyring. It returns the model then and the commands the last update
// returned, so a test sees what the user would once the keyring answered.
func act(m Model, msg tea.Msg) (Model, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return drainKeyring(nm.(Model), cmd)
}

// drainKeyring feeds m the replies of the keyring operations cmd runs.
func drainKeyring(m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	for m.keyring != nil {
		nm, next := m.Update(awaitKeyring(cmd, m.keyring.id))
		m, cmd = nm.(Model), next
	}
	return m, cmd
}

// awaitKeyring runs cmd, and any batch it stands for, and returns the reply
// of keyring operation id. Everything else it produces is dropped.
func awaitKeyring(cmd tea.Cmd, id int) keyringReplyMsg {
	found := make(chan keyringReplyMsg, 1)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			switch msg := c().(type) {
			case tea.BatchMsg:
				for _, c := range msg {
					run(c)
				}
			case keyringReplyMsg:
				if msg.id == id {
					found <- msg
				}
			}
		}()
	}
	run(cmd)
	select {
	case r := <-found:
		return r
	case <-time.After(10 * time.Second):
		panic("keyring operation did not finish within 10s")
	}
}

// settle runs cmd the way Bubble Tea would and feeds the model the end of
// the session it started, after the last-used time it recorded as it began,
// the order they arrive in unless another Wicket holds the state lock. Ticks
// and other replies are dropped, as press has always dropped them.
func settle(m Model, cmd tea.Cmd) Model {
	msgs := make(chan tea.Msg, 64)
	stop := make(chan struct{})
	defer close(stop)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, c := range b {
					run(c)
				}
				return
			}
			select {
			case msgs <- msg:
			case <-stop:
			}
		}()
	}
	run(cmd)
	timeout := time.After(10 * time.Second)
	var ended tea.Msg
	for m.session != nil {
		select {
		case msg := <-msgs:
			switch msg.(type) {
			case sessionRecordedMsg:
				nm, _ := m.Update(msg)
				m = nm.(Model)
				if ended == nil {
					continue
				}
				msg = ended
			case sessionEndedMsg:
				if !m.session.recorded {
					ended = msg
					continue
				}
			default:
				continue
			}
			nm, next := m.Update(msg)
			m = nm.(Model)
			if m.session != nil {
				run(next)
			}
		case <-timeout:
			panic("session did not end within 10s")
		}
	}
	return m
}

func typeInto(m Model, s string) Model {
	for _, r := range s {
		m = press(m, string(r))
	}
	return m
}

// bg is the context tests hand the keyring when nothing is meant to cancel it.
var bg = context.Background()

type panicStore struct{}

func (panicStore) Lookup(context.Context, secret.Identity) (secret.LookupResult, error) {
	panic("list must not query secret.Store")
}
func (panicStore) Upsert(context.Context, secret.Identity, secret.Password) error {
	panic("list must not query secret.Store")
}
func (panicStore) Delete(context.Context, secret.Identity) error {
	panic("list must not query secret.Store")
}
func (panicStore) Presence(context.Context, secret.Identity) (secret.Presence, error) {
	panic("list must not query secret.Store")
}

type wrapStore struct {
	inner     secret.Store
	upsertErr error
	deleteErr error
	lookupErr error
	lookups   int
}

func (w *wrapStore) Lookup(ctx context.Context, id secret.Identity) (secret.LookupResult, error) {
	w.lookups++
	if w.lookupErr != nil {
		return secret.LookupResult{}, w.lookupErr
	}
	return w.inner.Lookup(ctx, id)
}
func (w *wrapStore) Upsert(ctx context.Context, id secret.Identity, pw secret.Password) error {
	if w.upsertErr != nil {
		return w.upsertErr
	}
	return w.inner.Upsert(ctx, id, pw)
}
func (w *wrapStore) Presence(ctx context.Context, id secret.Identity) (secret.Presence, error) {
	return w.inner.Presence(ctx, id)
}
func (w *wrapStore) Delete(ctx context.Context, id secret.Identity) error {
	if w.deleteErr != nil {
		return w.deleteErr
	}
	return w.inner.Delete(ctx, id)
}

type harness struct {
	t      *testing.T
	home   string
	cfg    string
	state  string
	store  secret.Store
	stdout bytes.Buffer
	stderr bytes.Buffer
	m      Model
}

func newHarness(t *testing.T, body string, store secret.Store) *harness {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg", "config.toml")
	st := filepath.Join(home, "state.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if store == nil {
		store = secret.NewMemory()
	}
	h := &harness{t: t, home: home, cfg: cfg, state: st, store: store}
	m := New(Options{
		Home:       home,
		ConfigPath: cfg,
		StatePath:  st,
		Store:      store,
		Width:      80,
		Height:     24,
		// The launcher's writers stand in for the terminal: the TUI must
		// never let the client write there.
		Launcher: &rdp.Launcher{
			Stdout: &h.stdout,
			Stderr: &h.stderr,
		},
	})
	// Which FreeRDP clients the form offers must not depend on the machine
	// running the tests. Both known clients are "installed" unless a test
	// says otherwise; launching still goes through the launcher's runner.
	m.app.LookPath = onPath(rdp.ClientSDL, rdp.ClientX11)
	h.m = m
	// A test that starts a client and then fails, or simply returns, must
	// not leave it running: the app is shared by every copy of the model,
	// so this stops whichever session it last started.
	t.Cleanup(func() { m.app.StopSession(time.Second) })
	return h
}

func fixtureTOML(name, host, user string) string {
	return `
[general]
[[profiles]]
name = "` + name + `"
host = "` + host + `"
user = "` + user + `"
client = "sdl-freerdp3"
dynamic_resolution = true
scale = 100
`
}

func mustPassword(t *testing.T, s string) secret.Password {
	t.Helper()
	pw, err := secret.NewPassword(s)
	if err != nil {
		t.Fatal(err)
	}
	return pw
}

// focusField moves to id the way a user would. Assigning form.field directly
// leaves the input blurred, and Bubbles then drops every keystroke, so a test
// that types into it proves nothing.
func focusField(t *testing.T, m Model, id int) Model {
	t.Helper()
	// tab wraps, so this reaches a field in either direction.
	for i := 0; i <= fieldCount; i++ {
		if m.form.field == id {
			if v := m.form.textValue(id); v != nil && !m.form.inputs[id].Focused() {
				t.Fatalf("field %d is current but its input is not focused", id)
			}
			return m
		}
		m = press(m, "tab")
	}
	t.Fatalf("could not reach field %d", id)
	return m
}

// onPath is a LookPath that finds only names.
func onPath(names ...string) func(string) (string, error) {
	return func(file string) (string, error) {
		if slices.Contains(names, file) {
			return "/fake/bin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
}
