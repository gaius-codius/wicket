package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
)

const sentinel = "s3cret-SENTINEL"

func withFakeRDP(t *testing.T) string {
	t.Helper()
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)
	t.Setenv("FAKERDP_EXIT", "0")
	return rec
}

func TestConnect_StoredSecretNoModal(t *testing.T) {
	rec := withFakeRDP(t)
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "192.168.1.20", "jdoe"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	_ = store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
	h.m = press(h.m, "enter")
	if h.m.view == viewModal {
		t.Fatal("modal should be skipped")
	}
	out := screen(h.m)
	if strings.Contains(out, sentinel) {
		t.Fatal("sentinel in TUI status")
	}
	if !strings.Contains(out, "work") || !strings.Contains(out, "192.168.1.20") {
		t.Fatalf("selected card missing:\n%s", out)
	}
	if strings.Contains(out, "never") {
		t.Fatal("last-used should be written")
	}
	if h.m.statusKind == statusError {
		t.Fatalf("error status %q", h.m.status)
	}
	got := testutil.ReadRecord(t, rec)
	plan, err := rdp.BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Argv) < 2 {
		t.Fatalf("argv %v", got.Argv)
	}
	if strings.Join(got.Argv[1:], "\n") != strings.Join(plan.Args, "\n") {
		t.Fatalf("argv %v want %v", got.Argv[1:], plan.Args)
	}
	if got.Stdin != sentinel+"\n" {
		t.Fatalf("stdin %q", got.Stdin)
	}
	for _, a := range got.Argv {
		if strings.Contains(a, sentinel) || strings.Contains(a, "/p:") {
			t.Fatal(a)
		}
	}
}

func TestConnect_RestartSharedStore(t *testing.T) {
	rec := withFakeRDP(t)
	store := secret.NewMemory()
	h := newHarness(t, "", store)
	p := config.Profile{
		Name: "work", Host: "h", User: "u", Client: "sdl-freerdp3",
		DynamicResolution: true, Scale: 100,
	}
	h.m.form = formState{p: p, password: sentinel}
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("after save view=%v err=%s", h.m.view, h.m.form.err)
	}
	m2 := New(Options{
		Home: h.home, ConfigPath: h.cfg, StatePath: h.state, Store: store,
		Width: 80, Height: 24, Launcher: h.m.app.Launcher,
	})
	m2 = press(m2, "enter")
	if m2.view == viewModal {
		t.Fatal("restart should not show modal")
	}
	got := testutil.ReadRecord(t, rec)
	if got.Stdin != sentinel+"\n" {
		t.Fatalf("stdin %q", got.Stdin)
	}
}

func TestConnect_MissingClientNoLastUsed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	p, _ := h.m.app.Cfg.Profile("work")
	_ = h.store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
	h.m = press(h.m, "enter")
	if h.m.view != viewList {
		t.Fatalf("view %v", h.m.view)
	}
	if h.m.statusKind != statusError || !strings.Contains(h.m.status, "sdl-freerdp3") {
		t.Fatalf("status %q", h.m.status)
	}
	if _, ok := h.m.app.State.LastUsed("work"); ok {
		t.Fatal("last_used written")
	}
}

// The terminal belongs to Bubble Tea while a session runs, so whatever the
// client logs goes to a buffer instead. The launcher's writers stand in for
// the terminal here; before the session view, they got the client's output.
func TestConnect_ClientOutputNeverReachesTheTerminal(t *testing.T) {
	_ = withFakeRDP(t)
	t.Setenv("FAKERDP_OUTPUT", "[ERROR][com.freerdp.core] - ERRCONNECT_LOGON_FAILURE")
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	_ = store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
	h.m = press(h.m, "enter")
	if h.stdout.Len() != 0 || h.stderr.Len() != 0 {
		t.Fatalf("client wrote to the terminal: stdout %q stderr %q", h.stdout.String(), h.stderr.String())
	}
	if h.m.view != viewRetry {
		t.Fatalf("view %v, want the retry overlay", h.m.view)
	}
	// What it logged is still there for the retry overlay.
	if out := screen(h.m); !strings.Contains(out, "ERRCONNECT_LOGON_FAILURE") {
		t.Fatalf("the client's error is missing from the overlay:\n%s", out)
	}
}

// A new user or domain keeps the saved password, which goes with the
// profile to its new keyring identity, so connecting does not ask again.
func TestConnect_AccountChangeKeepsThePassword(t *testing.T) {
	for name, edit := range map[string]func(*config.Profile){
		"user":   func(p *config.Profile) { p.User = "other" },
		"domain": func(p *config.Profile) { p.Domain = "CORP" },
	} {
		t.Run(name, func(t *testing.T) {
			rec := withFakeRDP(t)
			store := secret.NewMemory()
			h := newHarness(t, fixtureTOML("work", "h", "u"), store)
			p, _ := h.m.app.Cfg.Profile("work")
			_ = store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
			edit(&p)
			h.m.form = formState{oldName: "work", p: p}
			h.m.view = viewForm
			h.m = press(h.m, "ctrl+s")
			if h.m.view != viewList || h.m.statusKind != statusSuccess {
				t.Fatalf("after save view=%v status=%q err=%s", h.m.view, h.m.status, h.m.form.err)
			}
			if !strings.Contains(h.m.status, "saved password moved with it") {
				t.Fatalf("status %q, want it to say the password moved", h.m.status)
			}
			h.m = press(h.m, "enter")
			if h.m.view == viewModal {
				t.Fatal("asked for a password the edit should have kept")
			}
			if got := testutil.ReadRecord(t, rec); got.Stdin != sentinel+"\n" {
				t.Fatalf("stdin %q", got.Stdin)
			}
		})
	}
}

type jumpClock struct {
	times []time.Time
	i     int
}

func (c *jumpClock) Now() time.Time {
	if c.i >= len(c.times) {
		return c.times[len(c.times)-1]
	}
	t := c.times[c.i]
	c.i++
	return t
}

func TestConnect_LongSessionNoRetryOverlay(t *testing.T) {
	_ = withFakeRDP(t)
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	_ = store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
	h.m.app.Launcher.Clock = &jumpClock{
		times: []time.Time{time.Unix(0, 0), time.Unix(4, 0)},
	}
	h.m = press(h.m, "enter")
	if h.m.view != viewList {
		t.Fatalf("view %v want list, status=%s", h.m.view, h.m.status)
	}
	if h.m.statusKind == statusError || !strings.Contains(h.m.status, "session ended") {
		t.Fatalf("status %q", h.m.status)
	}
	if h.m.useOnce != nil {
		t.Fatal("use-once must not survive a finished session")
	}
}

func TestConnect_UseOnceNotReusedOnOtherProfile(t *testing.T) {
	body := fixtureTOML("work", "h1", "u1") + `
[[profiles]]
name = "lab"
host = "h2"
user = "u2"
client = "sdl-freerdp3"
dynamic_resolution = true
scale = 100
`
	_ = withFakeRDP(t)
	h := newHarness(t, body, secret.NewMemory())
	h.m.app.Launcher.Clock = &jumpClock{
		times: []time.Time{time.Unix(0, 0), time.Unix(4, 0)},
	}
	h.m = press(h.m, "enter")
	h.m = typeInto(h.m, sentinel)
	h.m = press(h.m, "enter")
	if h.m.view != viewList {
		t.Fatalf("view %v status=%s", h.m.view, h.m.status)
	}
	if h.m.useOnce != nil {
		t.Fatal("use-once leaked after ClassEnded")
	}
	h.m = press(h.m, "j")
	h.m = press(h.m, "enter")
	if h.m.view != viewModal {
		t.Fatalf("lab must prompt, view=%v status=%s", h.m.view, h.m.status)
	}
}

type multiStore struct {
	secret.Store
}

func (s multiStore) Lookup(ctx context.Context, id secret.Identity) (secret.LookupResult, error) {
	res, err := s.Store.Lookup(ctx, id)
	if err == nil {
		res.Multiple = true
	}
	return res, err
}

func TestConnect_MultipleWarningSurvives(t *testing.T) {
	_ = withFakeRDP(t)
	inner := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), multiStore{inner})
	p, _ := h.m.app.Cfg.Profile("work")
	_ = inner.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
	h.m.app.Launcher.Clock = &jumpClock{
		times: []time.Time{time.Unix(0, 0), time.Unix(4, 0)},
	}
	h.m = press(h.m, "enter")
	if !strings.Contains(h.m.status, "multiple matching secrets") {
		t.Fatalf("status %q", h.m.status)
	}
}

// Pressing n after a failed session asks for a replacement. Saying "No stored
// password" there was backwards: a password is stored, and suspecting it is
// wrong is the reason for pressing n at all.
func TestRetry_NewPasswordDoesNotClaimThereIsNone(t *testing.T) {
	_ = withFakeRDP(t)
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	if err := store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, "secret")); err != nil {
		t.Fatal(err)
	}
	h.m = press(h.m, "enter")
	if h.m.view != viewRetry {
		t.Fatalf("view %v, want the retry overlay", h.m.view)
	}
	h.m = press(h.m, "n")
	if h.m.view != viewModal {
		t.Fatalf("view %v, want the password modal", h.m.view)
	}
	out := stripANSI(screen(h.m))
	if strings.Contains(out, "No stored password") || strings.Contains(out, "No password is saved") {
		t.Fatalf("the modal denies the stored password:\n%s", out)
	}
	if !strings.Contains(out, "Connect to work") || !strings.Contains(out, "Type a new password") {
		t.Fatalf("%s", out)
	}
}
