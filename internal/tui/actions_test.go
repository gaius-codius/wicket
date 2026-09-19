package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

func testApp(t *testing.T, body string, store secret.Store) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if body == "" {
		body = "[general]\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := config.OpenState(filepath.Join(t.TempDir(), "state.toml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		store = secret.NewMemory()
	}
	return &App{Cfg: cfg, Secrets: store, State: st}
}

func TestSaveProfile_PureRenameCopiesSecret(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	pw := mustPassword(t, "secret")
	if err := store.Upsert(secret.IdentityFor(a.Cfg.Path(), p), pw); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Name = "office"
	if _, err := a.SaveProfile("work", newP, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(secret.IdentityFor(a.Cfg.Path(), newP)); err != nil {
		t.Fatal("secret should exist under new name")
	}
	if _, err := store.Lookup(secret.IdentityFor(a.Cfg.Path(), p)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("old identity still present: %v", err)
	}
}

func TestSaveProfile_RenameStoreFailAbortsTOML(t *testing.T) {
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, upsertErr: errors.New("upsert boom")}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	_ = inner.Upsert(secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	newP := p
	newP.Name = "office"
	if _, err := a.SaveProfile("work", newP, PasswordIntent{}); err == nil {
		t.Fatal("want abort")
	}
	if _, ok := a.Cfg.Profile("work"); !ok {
		t.Fatal("TOML renamed despite abort")
	}
	if _, ok := a.Cfg.Profile("office"); ok {
		t.Fatal("new name written")
	}
}

func TestSaveProfile_TOMLFailAfterStoreNewRollsBack(t *testing.T) {
	store := secret.NewMemory()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(fixtureTOML("work", "h", "u")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Cfg: cfg, Secrets: store}
	p, _ := cfg.Profile("work")
	_ = store.Upsert(secret.IdentityFor(cfg.Path(), p), mustPassword(t, "secret"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	newP := p
	newP.Name = "office"
	_, err = a.SaveProfile("work", newP, PasswordIntent{})
	if err == nil {
		t.Fatal("want TOML write failure")
	}
	if _, err := store.Lookup(secret.IdentityFor(cfg.Path(), newP)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("new identity should have been rolled back")
	}
}

func TestSaveProfile_IdentityChangeBlankDeletesOldNoCopy(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	_ = store.Upsert(secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	newP := p
	newP.Host = "other"
	if _, err := a.SaveProfile("work", newP, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(secret.IdentityFor(a.Cfg.Path(), p)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("old identity should be gone")
	}
	if _, err := store.Lookup(secret.IdentityFor(a.Cfg.Path(), newP)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("must not copy secret across identity change")
	}
}

func TestSaveProfile_RenamePlusHostIsInvalidation(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	_ = store.Upsert(secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	newP := p
	newP.Name = "office"
	newP.Host = "other"
	if _, err := a.SaveProfile("work", newP, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(secret.IdentityFor(a.Cfg.Path(), p)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("old gone")
	}
	if _, err := store.Lookup(secret.IdentityFor(a.Cfg.Path(), newP)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("must not copy")
	}
}

func TestSaveProfile_CRLFRejected(t *testing.T) {
	a := testApp(t, "[general]\n", nil)
	p := config.Profile{Name: "n", Host: "h", User: "u", Client: config.DefaultClient, Scale: 100, DynamicResolution: true}
	_, err := secret.NewPassword("a\nb")
	if err == nil {
		t.Fatal("NewPassword must reject CR/LF")
	}
	_, err = a.SaveProfile("", p, PasswordIntent{Set: true, Store: true, Password: secret.Password{}})
	if err == nil {
		t.Fatal("blank stored password rejected")
	}
}

func TestSaveProfile_UniquenessReject(t *testing.T) {
	body := fixtureTOML("work", "h", "u") + `
[[profiles]]
name = "lab"
host = "h2"
user = "u2"
`
	a := testApp(t, body, nil)
	p, _ := a.Cfg.Profile("lab")
	p.Name = "work"
	if _, err := a.SaveProfile("lab", p, PasswordIntent{}); err == nil {
		t.Fatal("want uniqueness error")
	}
	if _, ok := a.Cfg.Profile("lab"); !ok {
		t.Fatal("lab should remain")
	}
}

func TestSaveProfile_Forget(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	_ = store.Upsert(secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	if _, err := a.SaveProfile("work", p, PasswordIntent{Forget: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(secret.IdentityFor(a.Cfg.Path(), p)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("secret should be forgotten")
	}
}

func TestSaveProfile_RenameMovesState(t *testing.T) {
	a := testApp(t, fixtureTOML("work", "h", "u"), nil)
	if err := a.State.Record("work"); err != nil {
		t.Fatal(err)
	}
	p, _ := a.Cfg.Profile("work")
	p.Name = "office"
	if _, err := a.SaveProfile("work", p, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.State.LastUsed("office"); !ok {
		t.Fatal("state key not moved")
	}
	if _, ok := a.State.LastUsed("work"); ok {
		t.Fatal("old state key remains")
	}
}

func TestSaveProfile_RenameLookupUnavailableAborts(t *testing.T) {
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, lookupErr: secret.ErrUnavailable}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	oldID := secret.IdentityFor(a.Cfg.Path(), p)
	if err := inner.Upsert(oldID, mustPassword(t, "secret")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Name = "office"
	if _, err := a.SaveProfile("work", newP, PasswordIntent{}); err == nil {
		t.Fatal("want abort")
	}
	if _, ok := a.Cfg.Profile("work"); !ok {
		t.Fatal("TOML renamed despite abort")
	}
	if _, ok := a.Cfg.Profile("office"); ok {
		t.Fatal("new name written")
	}
	if _, err := inner.Lookup(oldID); err != nil {
		t.Fatalf("old secret destroyed: %v", err)
	}
}

func TestSaveProfile_RenameTypedStoreOffMovesSecret(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	oldID := secret.IdentityFor(a.Cfg.Path(), p)
	if err := store.Upsert(oldID, mustPassword(t, "kept")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Name = "office"
	typed := mustPassword(t, "not-stored")
	if _, err := a.SaveProfile("work", newP, PasswordIntent{Set: true, Password: typed, Store: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(oldID); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("old identity remains: %v", err)
	}
	got, err := store.Lookup(secret.IdentityFor(a.Cfg.Path(), newP))
	if err != nil {
		t.Fatal("secret should move with the name")
	}
	var buf strings.Builder
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "kept\n" {
		t.Fatalf("moved %q, typed replacement must not replace the stored secret when store is off", buf.String())
	}
}

func TestSaveProfile_SizeTrimmed(t *testing.T) {
	a := testApp(t, "[general]\n", nil)
	p := config.Profile{Name: "n", Host: "h", User: "u", Client: config.DefaultClient, Scale: 100, DynamicResolution: true, Size: "  100%  "}
	if _, err := a.SaveProfile("", p, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	got, _ := a.Cfg.Profile("n")
	if got.Size != "100%" {
		t.Fatalf("size %q", got.Size)
	}
}

func TestDeleteProfile_ForgetWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(fixtureTOML("work", "h", "u")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	stPath := filepath.Join(t.TempDir(), "state.toml")
	st, err := config.OpenState(stPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Cfg: cfg, Secrets: secret.NewMemory(), State: st}
	if err := a.State.Record("work"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stPath, []byte("not-toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	warns, err := a.DeleteProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) == 0 || !strings.Contains(strings.Join(warns, " "), "last_used") {
		t.Fatalf("warns = %v", warns)
	}
}

func TestDeleteProfile_LeftoverSecretWarning(t *testing.T) {
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, deleteErr: errors.New("locked")}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	_ = inner.Upsert(secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	warns, err := a.DeleteProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Cfg.Profile("work"); ok {
		t.Fatal("profile should be gone")
	}
	if len(warns) == 0 || !strings.Contains(strings.Join(warns, " "), "leftover") {
		t.Fatalf("warns = %v", warns)
	}
}
