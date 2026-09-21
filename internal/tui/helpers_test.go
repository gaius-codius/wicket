package tui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
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
		nm, cmd := m.Update(keyMsg(k))
		m = nm.(Model)
		if m.session != nil {
			m = settle(m, cmd)
		}
	}
	return m
}

// settle runs cmd the way Bubble Tea would and feeds the model the end of
// the session it started. Ticks and other replies are dropped, as press has
// always dropped them.
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
	for m.session != nil {
		select {
		case msg := <-msgs:
			if _, ok := msg.(sessionEndedMsg); !ok {
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

type panicStore struct{}

func (panicStore) Lookup(secret.Identity) (secret.LookupResult, error) {
	panic("list must not query secret.Store")
}
func (panicStore) Upsert(secret.Identity, secret.Password) error {
	panic("list must not query secret.Store")
}
func (panicStore) Delete(secret.Identity) error {
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

func (w *wrapStore) Lookup(id secret.Identity) (secret.LookupResult, error) {
	w.lookups++
	if w.lookupErr != nil {
		return secret.LookupResult{}, w.lookupErr
	}
	return w.inner.Lookup(id)
}
func (w *wrapStore) Upsert(id secret.Identity, pw secret.Password) error {
	if w.upsertErr != nil {
		return w.upsertErr
	}
	return w.inner.Upsert(id, pw)
}
func (w *wrapStore) Presence(ctx context.Context, id secret.Identity) (secret.Presence, error) {
	return w.inner.Presence(ctx, id)
}
func (w *wrapStore) Delete(id secret.Identity) error {
	if w.deleteErr != nil {
		return w.deleteErr
	}
	return w.inner.Delete(id)
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
	h.m = m
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
