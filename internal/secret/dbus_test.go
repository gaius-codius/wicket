package secret

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/testutil/fakesecret"
)

func TestDBus_CRUDAndIsolation(t *testing.T) {
	userBus := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	addr, _, cleanup := fakesecret.Start(t)
	defer cleanup()
	if addr == "" {
		t.Fatal("empty bus address")
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == userBus && userBus != "" {
		t.Fatal("tests must not use the user session bus")
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") != addr {
		t.Fatalf("DBUS_SESSION_BUS_ADDRESS=%q want %q", os.Getenv("DBUS_SESSION_BUS_ADDRESS"), addr)
	}

	store := NewDBus()
	p := config.Profile{Name: "work", Host: "h", User: "u", Domain: "D"}
	idA := IdentityFor("/tmp/a.toml", p)
	idB := IdentityFor("/tmp/b.toml", p)
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(idA, pw); err != nil {
		t.Fatal(err)
	}
	got, err := store.Lookup(idA)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "s3cret\n" {
		t.Fatalf("lookup %q", buf.String())
	}
	if _, err := store.Lookup(idB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("isolation: %v", err)
	}
	if err := store.Delete(idA); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

// A locked keyring answers a write with a prompt path. Wicket must complete
// that prompt, and must not report success when the user refuses it.
func TestDBus_WritesWaitForTheKeyringPrompt(t *testing.T) {
	addr, srv, cleanup := fakesecret.Start(t)
	defer cleanup()
	if addr == "" {
		t.Fatal("empty bus address")
	}

	store := NewDBus()
	p := config.Profile{Name: "work", Host: "h", User: "u"}
	id := IdentityFor("/tmp/a.toml", p)
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}

	srv.SetPrompt(fakesecret.PromptDismiss)
	if err := store.Upsert(id, pw); err == nil {
		t.Fatal("Upsert reported success although the prompt was dismissed")
	} else if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want it to wrap ErrUnavailable", err)
	}
	if n := srv.Stored(); n != 0 {
		t.Fatalf("%d items stored after a dismissed prompt", n)
	}
	if _, err := store.Lookup(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup err = %v, want ErrNotFound", err)
	}

	srv.SetPrompt(fakesecret.PromptAccept)
	if err := store.Upsert(id, pw); err != nil {
		t.Fatalf("Upsert with an accepted prompt: %v", err)
	}
	if n := srv.Stored(); n != 1 {
		t.Fatalf("%d items stored after an accepted prompt, want 1", n)
	}

	srv.SetPrompt(fakesecret.PromptDismiss)
	if err := store.Delete(id); err == nil {
		t.Fatal("Delete reported success although the prompt was dismissed")
	}
	if n := srv.Stored(); n != 1 {
		t.Fatalf("%d items stored after a dismissed delete, want 1", n)
	}

	srv.SetPrompt(fakesecret.PromptAccept)
	if err := store.Delete(id); err != nil {
		t.Fatalf("Delete with an accepted prompt: %v", err)
	}
	if n := srv.Stored(); n != 0 {
		t.Fatalf("%d items stored after an accepted delete, want 0", n)
	}
}

// Reading from a locked keyring goes through Unlock, which answers with a
// prompt of its own. Until this was exercised, nothing proved that Wicket
// waits for it -- or that a refused prompt reads as "no password" rather than
// as an error the user cannot act on.
func TestDBus_LookupUnlocksALockedKeyring(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	store := NewDBus()
	id := IdentityFor("/tmp/a.toml", config.Profile{Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(id, pw); err != nil {
		t.Fatal(err)
	}

	srv.SetLocked(true)
	srv.SetPrompt(fakesecret.PromptDismiss)
	if _, err := store.Lookup(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dismissed unlock: err = %v, want ErrNotFound", err)
	}

	srv.SetPrompt(fakesecret.PromptAccept)
	got, err := store.Lookup(id)
	if err != nil {
		t.Fatalf("accepted unlock: %v", err)
	}
	var buf bytes.Buffer
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "s3cret\n" {
		t.Fatalf("lookup %q", buf.String())
	}
}

// Two items can share a profile's attributes -- two machines writing the same
// config, or a restored backup. Wicket takes the most recently modified and
// says so, which the in-memory store proved but the real one never did.
func TestDBus_LookupPrefersTheNewestOfSeveralMatches(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	id := IdentityFor("/tmp/a.toml", config.Profile{Name: "work", Host: "h", User: "u"})
	srv.Seed(id.Attrs(), "older", 1000)
	srv.Seed(id.Attrs(), "newer", 2000)

	got, err := NewDBus().Lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Multiple {
		t.Fatal("two matching items must be reported as multiple")
	}
	var buf bytes.Buffer
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "newer\n" {
		t.Fatalf("lookup %q, want the most recently modified", buf.String())
	}
}

// A prompt nobody answers must not hang Wicket for good. The wait is bounded,
// and the prompt is dismissed on the way out so no dialog is left behind.
func TestDBus_UnansweredPromptTimesOutAndDismisses(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	old := promptTimeout
	promptTimeout = 150 * time.Millisecond
	defer func() { promptTimeout = old }()

	id := IdentityFor("/tmp/a.toml", config.Profile{Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPrompt(fakesecret.PromptStall)
	err = NewDBus().Upsert(id, pw)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want it to wrap ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want it to say it timed out", err)
	}
	if n := srv.Dismissed(); n != 1 {
		t.Fatalf("prompt dismissed %d times, want 1", n)
	}
	if n := srv.Stored(); n != 0 {
		t.Fatalf("%d items stored although the prompt was never answered", n)
	}
}

// Presence runs whenever the list cursor moves, so it must answer from
// metadata alone. Anything beyond SearchItems -- a session, an Unlock, a
// prompt, a secret -- would put an unlock dialog in front of the user just
// for looking at a profile, or pull a password into memory for nothing.
func TestDBus_PresenceSearchesAndNothingElse(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	saved := IdentityFor("/tmp/a.toml", config.Profile{Name: "work", Host: "h", User: "u"})
	other := IdentityFor("/tmp/a.toml", config.Profile{Name: "home", Host: "h", User: "u"})
	srv.Seed(saved.Attrs(), "s3cret", 1000)
	// A locked keyring with a prompt configured: if Presence tried to unlock,
	// the prompt would be recorded below.
	srv.SetLocked(true)
	srv.SetPrompt(fakesecret.PromptAccept)

	store := NewDBus()
	ctx := context.Background()
	got, err := store.Presence(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}
	if got != Saved {
		t.Fatalf("locked match: %v, want saved", got)
	}
	got, err = store.Presence(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if got != NotSaved {
		t.Fatalf("no match: %v, want not saved", got)
	}

	srv.SetLocked(false)
	got, err = store.Presence(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}
	if got != Saved {
		t.Fatalf("unlocked match: %v, want saved", got)
	}

	calls := srv.Calls()
	if len(calls) != 3 {
		t.Fatalf("calls %v, want exactly three SearchItems", calls)
	}
	for _, c := range calls {
		if c != "SearchItems" {
			t.Fatalf("Presence called %s (all calls: %v); it may only search", c, calls)
		}
	}
}

// A keyring daemon that stops answering must not hold the check up past the
// caller's deadline, and the failure must read as "unavailable" so the list
// never claims the password is missing.
func TestDBus_PresenceHonoursTheDeadline(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()
	srv.StallSearches()

	id := IdentityFor("/tmp/a.toml", config.Profile{Name: "work", Host: "h", User: "u"})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := NewDBus().Presence(ctx, id)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Presence took %v against a 150ms deadline", elapsed)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want it to wrap ErrUnavailable", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to say the deadline passed", err)
	}
}

func TestDBus_PresenceWithoutAServiceIsUnavailable(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+t.TempDir()+"/nobus")
	id := IdentityFor("/tmp/a.toml", config.Profile{Name: "work", Host: "h", User: "u"})
	if _, err := NewDBus().Presence(context.Background(), id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want it to wrap ErrUnavailable", err)
	}
}
