package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/secret"
)

func TestDelete_ConfirmRemovesAll(t *testing.T) {
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	_ = store.Upsert(secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, "secret"))
	_ = h.m.app.State.Record("work")
	h.m = press(h.m, "D")
	out := screen(h.m)
	if !strings.Contains(out, "work") || !strings.Contains(out, "h") {
		t.Fatalf("confirm should show name+host:\n%s", out)
	}
	h.m = press(h.m, "y")
	if _, ok := h.m.app.Cfg.Profile("work"); ok {
		t.Fatal("profile remains")
	}
	if _, err := store.Lookup(secret.IdentityFor(h.m.app.Cfg.Path(), p)); err == nil {
		t.Fatal("secret remains")
	}
	if _, ok := h.m.app.State.LastUsed("work"); ok {
		t.Fatal("state remains")
	}
	if !strings.Contains(screen(h.m), "No saved connections") {
		t.Fatal("want empty")
	}
}

func TestDelete_CancelLeavesAll(t *testing.T) {
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	_ = store.Upsert(secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, "secret"))
	h.m = press(h.m, "D")
	h.m = press(h.m, "n")
	if _, ok := h.m.app.Cfg.Profile("work"); !ok {
		t.Fatal("removed on cancel")
	}
	if _, err := store.Lookup(secret.IdentityFor(h.m.app.Cfg.Path(), p)); err != nil {
		t.Fatal(err)
	}
}

func TestDelete_CursorNextThenPrevious(t *testing.T) {
	body := fixtureTOML("a", "h", "u") + `
[[profiles]]
name = "b"
host = "h2"
user = "u2"
[[profiles]]
name = "c"
host = "h3"
user = "u3"
`
	h := newHarness(t, body, secret.NewMemory())
	h.m.cursor = 0
	h.m = press(h.m, "D")
	h.m = press(h.m, "y")
	got, _ := h.m.selected()
	if got.Name != "b" {
		t.Fatalf("next = %s", got.Name)
	}
	h.m.cursor = 1
	h.m = press(h.m, "D")
	h.m = press(h.m, "y")
	got, _ = h.m.selected()
	if got.Name != "b" {
		t.Fatalf("previous of last = %s", got.Name)
	}
}

func TestDelete_LeftoverStatus(t *testing.T) {
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, deleteErr: os.ErrPermission}
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	_ = inner.Upsert(secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, "secret"))
	h.m = press(h.m, "D")
	h.m = press(h.m, "y")
	if !strings.Contains(h.m.status, "leftover") {
		t.Fatalf("status %q", h.m.status)
	}
}

func TestDelete_Help(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	h.m = press(h.m, "D")
	h.m = press(h.m, "?")
	out := screen(h.m)
	if !strings.Contains(out, "y") || !strings.Contains(out, "confirm") {
		t.Fatalf("%s", out)
	}
}
