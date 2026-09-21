package tui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

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

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		nm, _ := m.Update(keyMsg(k))
		m = nm.(Model)
	}
	return m
}

func typeInto(m Model, s string) Model {
	for _, r := range s {
		m = press(m, string(r))
	}
	return m
}

type recTerm struct {
	events []string
}

func (r *recTerm) Release() error {
	r.events = append(r.events, "release")
	return nil
}
func (r *recTerm) Restore() error {
	r.events = append(r.events, "restore")
	return nil
}

type seqRunner struct {
	rdp.OSRunner
	events *[]string
}

func (s seqRunner) Command(name string, arg ...string) *exec.Cmd {
	*s.events = append(*s.events, "start")
	return s.OSRunner.Command(name, arg...)
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
	term   *recTerm
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
	h := &harness{t: t, home: home, cfg: cfg, state: st, store: store, term: &recTerm{}}
	events := &h.term.events
	m := New(Options{
		Home:       home,
		ConfigPath: cfg,
		StatePath:  st,
		Store:      store,
		Term:       h.term,
		Width:      80,
		Height:     24,
		Launcher: &rdp.Launcher{
			Runner: seqRunner{events: events},
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

// newAsyncHarness mirrors production, where Run installs nopTerm and
// runConnect hands the client to tea.Exec. Only then is the model returned by
// Update the one the user sees while the session runs, which is where a view
// left pointing at a cleared dialog shows up.
func newAsyncHarness(t *testing.T, body string, store secret.Store) *harness {
	t.Helper()
	h := newHarness(t, body, store)
	h.m.app.Term = nopTerm{}
	return h
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
