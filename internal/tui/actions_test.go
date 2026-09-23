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

func TestSaveProfile_PureRenameCopiesSecret(t *testing.T) {
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
	if _, err := store.Lookup(bg, secret.IdentityFor(a.Cfg.Path(), newP)); err != nil {
		t.Fatal("secret should exist under new name")
	}
	if _, err := store.Lookup(bg, secret.IdentityFor(a.Cfg.Path(), p)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("old identity still present: %v", err)
	}
}

func TestSaveProfile_RenameStoreFailAbortsTOML(t *testing.T) {
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, upsertErr: errors.New("upsert boom")}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	_ = inner.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret"))
	newP := p
	newP.Name = "office"
	if _, err := a.SaveProfile(bg, "work", newP, PasswordIntent{}); err == nil {
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
	_ = store.Upsert(bg, secret.IdentityFor(cfg.Path(), p), mustPassword(t, "secret"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	newP := p
	newP.Name = "office"
	_, err = a.SaveProfile(bg, "work", newP, PasswordIntent{})
	if err == nil {
		t.Fatal("want TOML write failure")
	}
	if _, err := store.Lookup(bg, secret.IdentityFor(cfg.Path(), newP)); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("new identity should have been rolled back")
	}
}

// storedAs reads the password stored for p, or "" when there is none.
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

// A blank password field keeps what is stored, as the form says, whatever
// else the edit changes. A new host, user or domain is a new keyring
// identity, and the password used to be deleted with the old one, without a
// word, while the form promised to keep it.
func TestSaveProfile_IdentityChangeCarriesThePassword(t *testing.T) {
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
				t.Fatalf("new identity holds %q, want the password carried over", got)
			}
			if got := storedAs(t, store, a, p); got != "" {
				t.Fatal("the old identity's entry was left behind")
			}
		})
	}
}

// If the password cannot be stored under the new identity, nothing is saved
// and the password stays where it was: a save must never lose it.
func TestSaveProfile_CarryFailureKeepsEverything(t *testing.T) {
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, upsertErr: secret.ErrUnavailable}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	if err := inner.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Host = "other"
	_, err := a.SaveProfile(bg, "work", newP, PasswordIntent{})
	if err == nil || !strings.Contains(err.Error(), "nothing was saved") {
		t.Fatalf("err = %v, want the save refused and said so", err)
	}
	if strings.Contains(err.Error(), "secret service") {
		t.Fatalf("err = %v, want the keyring's error in plain words", err)
	}
	if got, _ := a.Cfg.Profile("work"); got.Host != "h" {
		t.Fatalf("host %q written although the password could not follow it", got.Host)
	}
	if got := storedAs(t, inner, a, p); got != "secret" {
		t.Fatalf("old entry holds %q, want it kept", got)
	}
}

// Moved, but the old entry could not be removed: the profile is saved and
// its password is under the new identity, and the leftover is reported.
func TestSaveProfile_CarryLeavesTheOldEntryWhenDeleteFails(t *testing.T) {
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, deleteErr: errors.New("locked")}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	if err := inner.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.User = "someone"
	warns, err := a.SaveProfile(bg, "work", newP, PasswordIntent{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "leftover") {
		t.Fatalf("warnings %q, want the leftover reported", warns)
	}
	if got := storedAs(t, inner, a, newP); got != "secret" {
		t.Fatalf("new identity holds %q", got)
	}
}

// A keyring that cannot be read cannot say whether there is a password to
// carry. The save goes ahead, but the old entry is left alone -- deleting it
// would lose a password that was never copied -- and the status says so.
func TestSaveProfile_IdentityChangeWithAnUnreadableKeyringKeepsTheOld(t *testing.T) {
	inner := secret.NewMemory()
	store := &wrapStore{inner: inner, lookupErr: secret.ErrUnavailable}
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	if err := inner.Upsert(bg, secret.IdentityFor(a.Cfg.Path(), p), mustPassword(t, "secret")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Domain = "CORP"
	warns, err := a.SaveProfile(bg, "work", newP, PasswordIntent{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "left in place") {
		t.Fatalf("warnings %q", warns)
	}
	if got := storedAs(t, inner, a, p); got != "secret" {
		t.Fatal("the only copy of the password was deleted")
	}
}

// Typing a password or forgetting it is an explicit choice, and nothing is
// carried over it.
func TestSaveProfile_IdentityChangeWithTypedOrForget(t *testing.T) {
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
	if got := storedAs(t, store, a, p); got != "" {
		t.Fatal("old entry left behind")
	}

	third := newP
	third.User = "someone"
	if _, err := a.SaveProfile(bg, "work", third, PasswordIntent{Action: PasswordForget}); err != nil {
		t.Fatal(err)
	}
	if storedAs(t, store, a, third) != "" || storedAs(t, store, a, newP) != "" {
		t.Fatal("forget carried or kept a password")
	}
}

// A typed password the keyring refuses, on a save that also moves the
// profile to a new host, must not cost the old one: it is the only password
// left. The old entry used to be deleted whether or not the new one had been
// stored.
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
	if got := storedAs(t, mem, a, p); got != "secret" {
		t.Fatalf("old entry %q, want the old password kept", got)
	}
	msg := strings.Join(warns, "; ")
	if !strings.Contains(msg, "could not save password") || !strings.Contains(msg, "old one was kept") {
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
	// Without a Secret Service there is no way to tell whether this profile
	// even has a password, and refusing the rename made renaming impossible
	// on any machine without one.
	warns, err := a.SaveProfile(bg, "work", newP, PasswordIntent{})
	if err != nil {
		t.Fatalf("rename blocked by the keyring: %v", err)
	}
	if _, ok := a.Cfg.Profile("office"); !ok {
		t.Fatal("profile not renamed")
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "keyring unavailable") {
		t.Fatalf("warnings %q, want one naming the keyring", warns)
	}
	if _, err := inner.Lookup(bg, oldID); err != nil {
		t.Fatalf("old secret destroyed: %v", err)
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
	oldID := secret.IdentityFor(a.Cfg.Path(), p)
	if err := store.Upsert(bg, oldID, mustPassword(t, "old")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Name = "office"
	typed := mustPassword(t, "new")
	if _, err := a.SaveProfile(bg, "work", newP, PasswordIntent{Action: PasswordSet, Password: typed}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(bg, oldID); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("old identity remains: %v", err)
	}
	got, err := store.Lookup(bg, secret.IdentityFor(a.Cfg.Path(), newP))
	if err != nil {
		t.Fatal("typed password should be stored under the new name")
	}
	var buf strings.Builder
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "new\n" {
		t.Fatalf("stored %q, the typed password must win over the old one", buf.String())
	}
}

func TestSaveProfile_RenameBlankMovesSecret(t *testing.T) {
	store := secret.NewMemory()
	a := testApp(t, fixtureTOML("work", "h", "u"), store)
	p, _ := a.Cfg.Profile("work")
	oldID := secret.IdentityFor(a.Cfg.Path(), p)
	if err := store.Upsert(bg, oldID, mustPassword(t, "kept")); err != nil {
		t.Fatal(err)
	}
	newP := p
	newP.Name = "office"
	if _, err := a.SaveProfile(bg, "work", newP, PasswordIntent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(bg, oldID); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("old identity remains: %v", err)
	}
	got, err := store.Lookup(bg, secret.IdentityFor(a.Cfg.Path(), newP))
	if err != nil {
		t.Fatal("an untouched password should follow the rename")
	}
	var buf strings.Builder
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "kept\n" {
		t.Fatalf("moved %q", buf.String())
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
		{"rewritten", "profiles = [{ name = \"work\", host = \"h\", user = \"u\" }]\n", true},
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
