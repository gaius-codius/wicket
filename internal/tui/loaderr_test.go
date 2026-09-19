package tui

import (
	"os"
	"strings"
	"testing"
)

func TestLoadError_InvalidTOMLUnchanged(t *testing.T) {
	orig := []byte("this is { not toml")
	h := newHarness(t, string(orig), nil)
	if h.m.view != viewLoadErr {
		t.Fatalf("view %v want load-error", h.m.view)
	}
	got, err := os.ReadFile(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(orig) {
		t.Fatalf("bytes changed: %q", got)
	}
	out := screen(h.m)
	if !strings.Contains(out, h.cfg) {
		t.Fatalf("path missing:\n%s", out)
	}
	h.m = press(h.m, "?")
	if h.m.view != viewHelp {
		t.Fatal("load-error help")
	}
	help := screen(h.m)
	if !strings.Contains(help, "q") || !strings.Contains(strings.ToLower(help), "quit") {
		t.Fatalf("load-error help:\n%s", help)
	}
	h.m = press(h.m, "q")
	if h.m.view != viewLoadErr {
		t.Fatal("q in help should return to load-error, not quit")
	}
	h.m = press(h.m, "q")
	if !h.m.quit {
		t.Fatal("q on load-error quits")
	}
}

func TestLoadError_DuplicateNames(t *testing.T) {
	body := fixtureTOML("work", "h", "u") + fixtureTOML("work", "h2", "u2")
	h := newHarness(t, body, nil)
	if h.m.view != viewLoadErr {
		t.Fatalf("view %v", h.m.view)
	}
}

func TestLoadError_UnreadableNotReplaced(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), nil)
	if err := os.Chmod(h.cfg, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(h.cfg, 0o600)
	m := New(Options{
		Home:       h.home,
		ConfigPath: h.cfg,
		StatePath:  h.state,
		Store:      panicStore{},
		Width:      80,
		Height:     24,
	})
	if m.view != viewLoadErr {
		t.Fatalf("view %v", m.view)
	}
	if err := os.Chmod(h.cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "name = \"work\"") {
		t.Fatalf("file replaced: %s", got)
	}
}
