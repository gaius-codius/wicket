package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestModal_UnavailableShowsLockedCopy(t *testing.T) {
	_ = withFakeRDP(t)
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, lookupErr: secret.ErrUnavailable}
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	h.m = press(h.m, "enter")
	if h.m.view != viewModal {
		t.Fatalf("view %v status=%s", h.m.view, h.m.status)
	}
	out := screen(h.m)
	if !strings.Contains(out, "unavailable") && !strings.Contains(out, "Secret store") {
		t.Fatalf("locked-keyring copy missing:\n%s", out)
	}
}

func TestModal_UseOnceDoesNotStore(t *testing.T) {
	rec := withFakeRDP(t)
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	h.m = press(h.m, "enter")
	if h.m.view != viewModal {
		t.Fatalf("view %v status=%s", h.m.view, h.m.status)
	}
	h.m = typeInto(h.m, sentinel)
	h.m = press(h.m, "enter")
	got := testutil.ReadRecord(t, rec)
	if got.Stdin != sentinel+"\n" {
		t.Fatalf("stdin %q", got.Stdin)
	}
	p, _ := h.m.app.Cfg.Profile("work")
	if _, err := store.Lookup(secret.IdentityFor(h.m.app.Cfg.Path(), p)); err == nil {
		t.Fatal("use-once must not create an item")
	}
	if strings.Contains(screen(h.m), sentinel) {
		t.Fatal("sentinel in view")
	}
}

func TestModal_CtrlSCreatesItem(t *testing.T) {
	_ = withFakeRDP(t)
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	h.m = press(h.m, "enter")
	h.m = typeInto(h.m, sentinel)
	h.m = press(h.m, "ctrl+s")
	p, _ := h.m.app.Cfg.Profile("work")
	if _, err := store.Lookup(secret.IdentityFor(h.m.app.Cfg.Path(), p)); err != nil {
		t.Fatal(err)
	}
}

func TestModal_StoreFailureStillConnects(t *testing.T) {
	rec := withFakeRDP(t)
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, upsertErr: errors.New("locked")}
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	h.m = press(h.m, "enter")
	h.m = typeInto(h.m, sentinel)
	h.m = press(h.m, "ctrl+s")
	got := testutil.ReadRecord(t, rec)
	if got.Stdin != sentinel+"\n" {
		t.Fatalf("stdin %q", got.Stdin)
	}
	if !strings.Contains(h.m.status, "could not save password") {
		t.Fatalf("status %q", h.m.status)
	}
}

func TestModal_CRLFRejected(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	_ = withFakeRDP(t)
	h.m = press(h.m, "enter")
	h.m.modal.input = "a\nb"
	h.m = press(h.m, "enter")
	if h.m.view != viewModal {
		t.Fatal("should stay on modal")
	}
	if h.m.modal.err == "" {
		t.Fatal("want CR/LF error")
	}
}

func TestModal_HelpWhenUnfocused(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	_ = withFakeRDP(t)
	h.m = press(h.m, "enter")
	h.m = press(h.m, "tab")
	h.m = press(h.m, "?")
	out := screen(h.m)
	if !strings.Contains(out, "enter") || !strings.Contains(out, "ctrl+s") {
		t.Fatalf("modal help:\n%s", out)
	}
}

func TestRetry_UseOnceUntilDismiss(t *testing.T) {
	rec := withFakeRDP(t)
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	h.m = press(h.m, "enter")
	h.m = typeInto(h.m, sentinel)
	h.m = press(h.m, "enter")
	if h.m.view != viewRetry {
		t.Fatalf("want retry overlay, view=%v status=%s", h.m.view, h.m.status)
	}
	helpM := press(h.m, "?")
	help := screen(helpM)
	if !strings.Contains(help, "retry") || !strings.Contains(help, "new password") {
		t.Fatalf("retry help:\n%s", help)
	}
	h.m = press(helpM, "esc")
	os.Remove(rec)
	h.m = press(h.m, "enter")
	got := testutil.ReadRecord(t, rec)
	if got.Stdin != sentinel+"\n" {
		t.Fatalf("retry stdin %q", got.Stdin)
	}
	h.m = press(h.m, "esc")
	if h.m.useOnce != nil {
		t.Fatal("dismiss should clear use-once")
	}
}

func TestRetry_NewPasswordThenEscClearsUseOnce(t *testing.T) {
	_ = withFakeRDP(t)
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	h.m = press(h.m, "enter")
	h.m = typeInto(h.m, sentinel)
	h.m = press(h.m, "enter")
	if h.m.view != viewRetry {
		t.Fatalf("want retry overlay, view=%v status=%s", h.m.view, h.m.status)
	}
	h.m = press(h.m, "n")
	if h.m.view != viewModal {
		t.Fatalf("view %v", h.m.view)
	}
	h.m = press(h.m, "esc")
	if h.m.useOnce != nil {
		t.Fatal("esc after new-password must drop the held credential")
	}
	h.m = press(h.m, "enter")
	if h.m.view != viewModal {
		t.Fatalf("must prompt again, view=%v", h.m.view)
	}
}
