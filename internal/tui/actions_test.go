package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
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

func TestSaveProfile_RenameKeepsSameKeyringIdentity(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	pw := mustPassword(t, "secret")
	if err := store.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), pw); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Name = "office"
	if _, err := a.SaveProfile(bg, "work", newP, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	// UUID identity does not change with the display name: no copy, no delete.
	if got := storedAs(t, store, a, newP); got != "secret" {
		t.Fatalf("stored %q under renamed profile", got)
	}
	if secret.IdentityFor(a.Cfg.Path(), p) != secret.IdentityFor(a.Cfg.Path(), newP) {
		t.Fatal("rename must keep the same keyring identity")
	}
}

func TestSaveProfile_RenameIgnoresKeyringUpsertFailure(t *testing.T) {
	// A rename no longer touches the keyring (issue #24), so a broken Upsert
	// must not block saving the new display name.
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, upsertErr: errors.New("upsert boom")}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	_ = inner.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	newP := p
	newP.Name = "office"
	if _, err := a.SaveProfile(bg, "work", newP, PasswordIntent{}); err != nil {
		t.Fatalf("rename blocked: %v", err)
	}
	if _, ok := a.Cfg.Profile("office"); !ok {
		t.Fatal("profile not renamed")
	}
	if got := storedAs(t, inner, a, newP); got != "secret" {
		t.Fatalf("password %q, want kept under the same id", got)
	}
}

func TestSaveProfile_TOMLFailAfterNewPasswordRollsBack(t *testing.T) {
	// Typed password is stored in finishSave after the config write, so a
	// failed write stores nothing.
	store := secret.NewMemory()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[general]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Cfg: cfg, Secrets: store}
	p := config.Profile{Name: "office", Host: "h", User: "u", Client: config.DefaultClient, Scale: 100, DynamicResolution: true, Clipboard: true}
	config.EnsureID(&p)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	_, err = a.SaveProfile(bg, "", p, PasswordIntent{Action: PasswordSet, Password: mustPassword(t, "secret")})
	if err == nil {
		t.Fatal("want TOML write failure")
	}
	if _, err := store.Lookup(bg, secret.IdentityFor(cfg.Path(), p)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("password should not be stored when the config write failed")
	}
}

func storedAs(t *testing.T, store secret.Store, a *App, p config.Profile) string {
	t.Helper()
	res, err := store.Lookup(bg, secret.IdentityFor(a.Cfg.Path(), p))
	if errors.Is(err, secret.ErrNotFound) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := res.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// A blank password field keeps what is stored. With UUID identities, a new
// host, user, domain or display name does not move the keyring item (issue #24).
func TestSaveProfile_AccountEditsKeepThePassword(t *testing.T) {
	for name, edit := range map[string]func(*config.Profile){
		"rename":         func(p *config.Profile) { p.Name = "office" },
		"host":           func(p *config.Profile) { p.Host = "other" },
		"user":           func(p *config.Profile) { p.User = "someone" },
		"domain":         func(p *config.Profile) { p.Domain = "CORP" },
		"rename+host":    func(p *config.Profile) { p.Name, p.Host = "office", "other" },
		"domain removed": func(p *config.Profile) { p.Domain = "" },
	} {
		t.Run(name, func(t *testing.T) {
			store := secret.NewMemory()
			a := testApp(t, fixtureTOML("work", "h", "u"), store)
			p, _ := a.Cfg.Profile("work")
			if name == "domain removed" {
				p.Domain = "OLD"
				if _, err := a.SaveProfile(bg, "work", p, PasswordIntent{}); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret")); err != nil {
				t.Fatal(err)
			}
			newP := p
			edit(&newP)
			warns, err := a.SaveProfile(bg, "work", newP, PasswordIntent{})
			if err != nil || len(warns) > 0 {
				t.Fatalf("save: %v %q", err, warns)
			}
			if got := storedAs(t, store, a, newP); got != "secret" {
				t.Fatalf("identity holds %q, want the password kept in place", got)
			}
			if secret.IdentityFor(a.Cfg.Path(), p) != secret.IdentityFor(a.Cfg.Path(), newP) {
				t.Fatal("account edits must not change the keyring identity")
			}
		})
	}
}

// Typing a password or forgetting it updates the same UUID identity.
func TestSaveProfile_TypedOrForgetOnAccountEdit(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	_ = store.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	newP := p
	newP.Host = "other"
	if _, err := a.SaveProfile(bg, "work", newP, PasswordIntent{Action: PasswordSet, Password: mustPassword(t, "typed")}); err != nil {
		t.Fatal(err)
	}
	if got := storedAs(t, store, a, newP); got != "typed" {
		t.Fatalf("stored %q, want the typed password", got)
	}

	third := newP
	third.User = "someone"
	if _, err := a.SaveProfile(bg, "work", third, PasswordIntent{Action: PasswordForget}); err != nil {
		t.Fatal(err)
	}
	if storedAs(t, store, a, third) != "" {
		t.Fatal("forget left a password")
	}
}

// A typed password the keyring refuses must not be reported as saved; the
// existing password under the same UUID remains.
func TestSaveProfile_FailedTypedPasswordKeepsTheOldOne(t *testing.T) {
	mem := secret.NewMemory()
	store := &wrapStore{inner: mem}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	_ = mem.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	store.upsertErr = fmt.Errorf("%w: %w", secret.ErrUnavailable, secret.ErrPromptDismissed)
	newP := p
	newP.Host = "other"
	warns, err := a.SaveProfile(bg, "work", newP, PasswordIntent{Action: PasswordSet, Password: mustPassword(t, "typed")})
	if err != nil {
		t.Fatal(err)
	}
	if got := storedAs(t, mem, a, newP); got != "secret" {
		t.Fatalf("entry %q, want the old password kept", got)
	}
	msg := strings.Join(warns, "; ")
	if !strings.Contains(msg, "could not save password") {
		t.Fatalf("warnings %q", msg)
	}
}

// keyringProblem keeps D-Bus detail off the status line: one short phrase
// the user can act on, never the socket path the error carries.
func TestKeyringProblem_IsShort(t *testing.T) {
	raw := errors.New("dial unix /tmp/x/nobus: connect: no such file or directory")
	for _, err := range []error{
		fmt.Errorf("%w: %v", secret.ErrUnavailable, raw),
		fmt.Errorf("%w: %w", secret.ErrUnavailable, context.DeadlineExceeded),
		fmt.Errorf("%w: %w", secret.ErrUnavailable, secret.ErrPromptDismissed),
		errors.New("line one\nline two " + strings.Repeat("x", 200)),
	} {
		got := keyringProblem(err)
		if strings.Contains(got, "/tmp") || strings.Contains(got, "\n") || len(got) > 80 {
			t.Errorf("keyringProblem(%v) = %q", err, got)
		}
	}
}

func TestSaveProfile_CRLFRejected(t *testing.T) {
	a := testApp(t, "[general]\n", nil)
	p := config.Profile{Name: "n", Host: "h", User: "u", Client: config.DefaultClient, Scale: 100, DynamicResolution: true}
	_, err := secret.NewPassword("a\nb")
	if err == nil {
		t.Fatal("NewPassword must reject CR/LF")
	}
	_, err = a.SaveProfile(bg, "", p, PasswordIntent{Action: PasswordSet, Password: secret.Password{}})
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
	if _, err := a.SaveProfile(bg, "lab", p, PasswordIntent{}); err == nil {
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
	_ = store.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	if _, err := a.SaveProfile(bg, "work", p, PasswordIntent{Action: PasswordForget}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(bg, secret.IdentityFor(a.Cfg.Path(), p)); !errors.Is(err, secret.ErrNotFound) {
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
	if _, err := a.SaveProfile(bg, "work", p, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.State.LastUsed("office"); !ok {
		t.Fatal("state key not moved")
	}
	if _, ok := a.State.LastUsed("work"); ok {
		t.Fatal("old state key remains")
	}
}

func TestSaveProfile_RenameSurvivesAnUnreachableKeyring(t *testing.T) {
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, lookupErr: secret.ErrUnavailable, deleteErr: secret.ErrUnavailable}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	oldID := secret.IdentityFor(a.Cfg.Path(), p)
	if err := inner.Upsert(bg, oldID, mustPassword(t, "secret")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Name = "office"
	// Rename does not consult the keyring (issue #24).
	warns, err := a.SaveProfile(bg, "work", newP, PasswordIntent{})
	if err != nil {
		t.Fatalf("rename blocked by the keyring: %v", err)
	}
	if _, ok := a.Cfg.Profile("office"); !ok {
		t.Fatal("profile not renamed")
	}
	if len(warns) != 0 {
		t.Fatalf("warnings %q, want none for a name-only save", warns)
	}
	if _, err := inner.Lookup(bg, oldID); err != nil {
		t.Fatalf("secret destroyed: %v", err)
	}
}

func TestDeleteProfile_UnreachableKeyringSaysSo(t *testing.T) {
	store := &wrapStore{inner: secret.NewMemory(), deleteErr: secret.ErrUnavailable}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	warns, err := a.DeleteProfile(bg, "work")
	if err != nil {
		t.Fatal(err)
	}
	// "a leftover secret may remain" claimed one for every profile deleted
	// without a keyring, including the ones that never had a password.
	if len(warns) != 1 || !strings.Contains(warns[0], "keyring unavailable") {
		t.Fatalf("warnings %q", warns)
	}
}

func TestSaveProfile_RenameTypedReplacesSecret(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	id := secret.IdentityFor(a.Cfg.Path(), p)
	if err := store.Upsert(bg, id, mustPassword(t, "old")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Name = "office"
	typed := mustPassword(t, "new")
	if _, err := a.SaveProfile(bg, "work", newP, PasswordIntent{Action: PasswordSet, Password: typed}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Lookup(bg, id)
	if err != nil {
		t.Fatal("typed password should replace under the same identity")
	}
	var buf strings.Builder
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "new\n" {
		t.Fatalf("stored %q, the typed password must win over the old one", buf.String())
	}
}

func TestSaveProfile_RenameBlankKeepsSecret(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	id := secret.IdentityFor(a.Cfg.Path(), p)
	if err := store.Upsert(bg, id, mustPassword(t, "kept")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Name = "office"
	if _, err := a.SaveProfile(bg, "work", newP, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Lookup(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "kept\n" {
		t.Fatalf("kept %q", buf.String())
	}
}

func TestSaveProfile_SizeTrimmed(t *testing.T) {
	a := testApp(t, "[general]\n", nil)
	p := config.Profile{Name: "n", Host: "h", User: "u", Client: config.DefaultClient, Scale: 100, DynamicResolution: true, Size: "  100%  "}
	if _, err := a.SaveProfile(bg, "", p, PasswordIntent{}); err != nil {
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
	warns, err := a.DeleteProfile(bg, "work")
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
	_ = inner.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	warns, err := a.DeleteProfile(bg, "work")
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

// A host the config refuses to save is refused before any keyring work, so
// the password is never copied for a save that cannot land, and refused
// again at connect time rather than left to FreeRDP.
func TestSave_SpacedHostIsRefusedBeforeTheKeyring(t *testing.T) {
	// panicStore fails the test if the save reaches the keyring at all.
	h := newHarness(t, fixtureTOML("work", "good.example", "u"), panicStore{})
	p, _ := h.m.app.Cfg.Profile("work")
	p.Host = "bad host"
	_, err := h.m.app.planSave("work", p, PasswordIntent{})
	var fe *config.FieldError
	if !errors.As(err, &fe) || fe.Field != "host" {
		t.Fatalf("planSave error = %v, want a host field error", err)
	}
	if _, err := rdp.BuildPlan(p); !errors.As(err, &fe) || fe.Field != "host" {
		t.Fatalf("BuildPlan error = %v, want a host field error", err)
	}
}

// A save that has to rewrite config.toml in full says so, since the user's
// comments went with it; one that edits it in place says nothing.
func TestSaveProfile_WarnsWhenTheConfigIsRewritten(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		warn       bool
	}{
		{"patched", fixtureTOML("work", "h", "u"), false},
		{"rewritten", "profiles = [{ id = \"00000000-0000-4000-8000-000000000001\", name = \"work\", host = \"h\", user = \"u\" }]\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t, tc.body, nil)
			p, _ := a.Cfg.Profile("work")
			p.User = "u2"
			warns, err := a.SaveProfile(bg, "work", p, PasswordIntent{})
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Contains(strings.Join(warns, "\n"), "rewritten in full")
			if got != tc.warn {
				t.Fatalf("warnings %q, want the rewrite warning: %v", warns, tc.warn)
			}
		})
	}
}

// Issue #24: a leftover keyring secret from a deleted profile must not be
// inherited by a later profile that reuses the same display name (and
// host/user/domain). UUID identities make the new profile a different key,
// and there is no name-keyed legacy lookup path that could bridge them.
func TestSaveProfile_RecreatedNameDoesNotInheritOrphan(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	id := secret.IdentityFor(a.Cfg.Path(), p)
	if err := store.Upsert(bg, id, mustPassword(t, "orphan")); err != nil {
		t.Fatal(err)
	}
	// Delete the profile but leave the secret (simulate keyring failure).
	failing := &wrapStore{inner: store, deleteErr: secret.ErrUnavailable}
	a.Secrets = failing
	if _, err := a.DeleteProfile(bg, "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(bg, id); err != nil {
		t.Fatal("setup: orphan must remain under the old UUID")
	}

	// Recreate the same display name and account fields with a new UUID.
	a.Secrets = store
	neu := config.Profile{Name: "work", Host: "h", User: "u", Client: config.DefaultClient, Scale: 100, DynamicResolution: true, Clipboard: true}
	if _, err := a.SaveProfile(bg, "", neu, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	saved, ok := a.Cfg.Profile("work")
	if !ok {
		t.Fatal("recreated work missing")
	}
	if saved.ID == "" || saved.ID == p.ID {
		t.Fatalf("recreated profile must have a distinct id, got %q", saved.ID)
	}
	neuID := secret.IdentityFor(a.Cfg.Path(), saved)
	if neuID == id {
		t.Fatal("recreated profile must use a different keyring identity")
	}
	if _, err := store.Lookup(bg, neuID); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("recreated profile must not see the orphaned secret")
	}
	res := a.ResolveCredential(bg, saved, nil)
	if !res.NeedModal || res.Err != nil || res.Cred != nil {
		t.Fatalf("ResolveCredential = %+v, want NeedModal and no orphan (no name-based inherit)", res)
	}
	// Original orphan remains under the old UUID until cleared manually.
	if _, err := store.Lookup(bg, id); err != nil {
		t.Fatal("orphan under the deleted UUID should still be present")
	}
}

// Duplicate assigns a new id and does not touch the source's keyring entry.
// planSave must FreshID even when the caller copied the source Profile with
// its id intact (SaveProfile("", copied) must not share keyring).
func TestSaveProfile_DuplicateGetsFreshID(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	orig, _ := a.Cfg.Profile("work")
	_ = store.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), orig), mustPassword(t, "orig"))
	cp := orig
	cp.Name = "work-copy"
	// Intentionally keep orig.ID — create path must remint.
	if _, err := a.SaveProfile(bg, "", cp, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	saved, ok := a.Cfg.Profile("work-copy")
	if !ok {
		t.Fatal("work-copy missing")
	}
	if saved.ID == "" || saved.ID == orig.ID {
		t.Fatalf("create must mint a fresh id, got %q (source %q)", saved.ID, orig.ID)
	}
	if _, err := store.Lookup(bg, secret.IdentityFor(a.Cfg.Path(), saved)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("untouched duplicate must not bind a password")
	}
	if got := storedAs(t, store, a, orig); got != "orig" {
		t.Fatalf("source password %q", got)
	}
}
