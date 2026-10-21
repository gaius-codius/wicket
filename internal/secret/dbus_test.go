package secret

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

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
	p := config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u", Domain: "D"}
	idA := IdentityFor("/tmp/a.toml", p)
	idB := IdentityFor("/tmp/b.toml", p)
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(bg, idA, pw); err != nil {
		t.Fatal(err)
	}
	got, err := store.Lookup(bg, idA)
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
	if _, err := store.Lookup(bg, idB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("isolation: %v", err)
	}
	if err := store.Delete(bg, idA); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(bg, idA); !errors.Is(err, ErrNotFound) {
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
	p := config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"}
	id := IdentityFor("/tmp/a.toml", p)
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}

	srv.SetPrompt(fakesecret.PromptDismiss)
	if err := store.Upsert(bg, id, pw); err == nil {
		t.Fatal("Upsert reported success although the prompt was dismissed")
	} else if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want it to wrap ErrUnavailable", err)
	}
	if n := srv.Stored(); n != 0 {
		t.Fatalf("%d items stored after a dismissed prompt", n)
	}
	if _, err := store.Lookup(bg, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup err = %v, want ErrNotFound", err)
	}

	srv.SetPrompt(fakesecret.PromptAccept)
	if err := store.Upsert(bg, id, pw); err != nil {
		t.Fatalf("Upsert with an accepted prompt: %v", err)
	}
	if n := srv.Stored(); n != 1 {
		t.Fatalf("%d items stored after an accepted prompt, want 1", n)
	}

	srv.SetPrompt(fakesecret.PromptDismiss)
	if err := store.Delete(bg, id); err == nil {
		t.Fatal("Delete reported success although the prompt was dismissed")
	}
	if n := srv.Stored(); n != 1 {
		t.Fatalf("%d items stored after a dismissed delete, want 1", n)
	}

	srv.SetPrompt(fakesecret.PromptAccept)
	if err := store.Delete(bg, id); err != nil {
		t.Fatalf("Delete with an accepted prompt: %v", err)
	}
	if n := srv.Stored(); n != 0 {
		t.Fatalf("%d items stored after an accepted delete, want 0", n)
	}
}

// Reading from a locked keyring goes through Unlock, which answers with a
// prompt of its own. Until this was exercised, nothing proved that Wicket
// waits for it. A refused prompt leaves a stored password unreadable, which
// is not the same as having none: "not found" sent connect off to offer a
// save into the same locked keyring (issue #64).
func TestDBus_LookupUnlocksALockedKeyring(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	store := NewDBus()
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(bg, id, pw); err != nil {
		t.Fatal(err)
	}

	srv.SetLocked(true)
	srv.SetPrompt(fakesecret.PromptDismiss)
	_, err = store.Lookup(bg, id)
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("dismissed unlock: err = %v, which says nothing is stored", err)
	}
	if !errors.Is(err, ErrLocked) || !errors.Is(err, ErrPromptDismissed) || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("dismissed unlock: err = %v, want ErrLocked wrapping the dismissal", err)
	}

	// Locked, but nothing stored for this profile: that is still "not
	// found", and no prompt should be raised to find it out.
	other := IdentityFor("/tmp/a.toml", config.Profile{ID: "11111111-2222-4333-8444-555555555555", Name: "home", Host: "h2", User: "u"})
	before := len(srv.Calls())
	if _, err := store.Lookup(bg, other); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrLocked) {
		t.Fatalf("locked, no match: err = %v, want ErrNotFound", err)
	}
	if slices.Contains(srv.Calls()[before:], "Unlock") {
		t.Fatal("locked, no match: Lookup asked to unlock the keyring")
	}

	srv.SetPrompt(fakesecret.PromptAccept)
	got, err := store.Lookup(bg, id)
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

// An unlock prompt nobody answers leaves the password just as unreadable as
// a refused one, and must not read as "not found" either.
func TestDBus_LookupWithAnUnansweredUnlockIsLocked(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	old := promptTimeout
	promptTimeout = 150 * time.Millisecond
	defer func() { promptTimeout = old }()

	store := NewDBus()
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(bg, id, pw); err != nil {
		t.Fatal(err)
	}
	srv.SetLocked(true)
	srv.SetPrompt(fakesecret.PromptStall)
	_, err = store.Lookup(bg, id)
	if !errors.Is(err, ErrLocked) || !errors.Is(err, ErrPromptTimeout) || errors.Is(err, ErrNotFound) {
		t.Fatalf("unanswered unlock: err = %v, want ErrLocked wrapping the timeout", err)
	}
	if n := srv.Dismissed(); n != 1 {
		t.Fatalf("prompt dismissed %d times, want 1", n)
	}
}

// Two items can share a profile's attributes -- two machines writing the same
// config, or a restored backup. Wicket takes the most recently modified and
// says so, which the in-memory store proved but the real one never did.
func TestDBus_LookupPrefersTheNewestOfSeveralMatches(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	srv.Seed(id.Attrs(), "older", 1000)
	srv.Seed(id.Attrs(), "newer", 2000)

	got, err := NewDBus().Lookup(bg, id)
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

// A profile can match an item in an open collection and a newer one in a
// locked collection. If the unlock is refused, the open one is readable but
// stale, and sending it would ignore the password the user saved last with
// no word of why (issue #80). Lookup reports the lock instead.
func TestDBus_LookupWithARefusedUnlockIgnoresAnOlderReadableMatch(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	srv.Seed(id.Attrs(), "older", 100)
	srv.LockItem(srv.Seed(id.Attrs(), "newer", 200))
	srv.SetPrompt(fakesecret.PromptDismiss)

	store := NewDBus()
	got, err := store.Lookup(bg, id)
	if !errors.Is(err, ErrLocked) || !errors.Is(err, ErrPromptDismissed) || !errors.Is(err, ErrUnavailable) {
		var buf bytes.Buffer
		_ = got.Password.WriteLine(&buf)
		t.Fatalf("dismissed unlock: password %q, err = %v, want ErrLocked wrapping the dismissal", buf.String(), err)
	}
	// Not even the older password should cross the bus.
	if slices.Contains(srv.Calls(), "GetSecrets") {
		t.Fatal("dismissed unlock: Lookup fetched secrets anyway")
	}

	srv.SetPrompt(fakesecret.PromptAccept)
	got, err = store.Lookup(bg, id)
	if err != nil {
		t.Fatalf("accepted unlock: %v", err)
	}
	var buf bytes.Buffer
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "newer\n" || !got.Multiple {
		t.Fatalf("accepted unlock: %q, multiple %v, want the newer password and a warning", buf.String(), got.Multiple)
	}
}

// An unlock the user accepts can still leave a match locked, when the
// backend lets them open some items and not others. The older readable
// match must not stand in for it then either.
func TestDBus_LookupWithAPartialUnlockIgnoresAnOlderReadableMatch(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	srv.Seed(id.Attrs(), "older", 100)
	srv.KeepLocked(srv.Seed(id.Attrs(), "newer", 200))
	srv.SetPrompt(fakesecret.PromptAccept)

	got, err := NewDBus().Lookup(bg, id)
	if !errors.Is(err, ErrLocked) || !errors.Is(err, ErrUnavailable) {
		var buf bytes.Buffer
		_ = got.Password.WriteLine(&buf)
		t.Fatalf("partial unlock: password %q, err = %v, want ErrLocked", buf.String(), err)
	}
}

// A password saved before the limit came down, or by another tool, is too
// long for the client. Lookup says so, rather than "not found", which would
// ask for a new password without saying what was wrong with the old one.
func TestDBus_LookupReportsASavedPasswordTooLongToUse(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	srv.Seed(id.Attrs(), strings.Repeat("a", 512), 1000)
	if _, err := NewDBus().Lookup(bg, id); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v, want ErrTooLong", err)
	}

	// An older usable password is not used in its place: it is likely stale,
	// and sending it would fail the login with no reason given.
	srv.Seed(id.Attrs(), "older", 500)
	if _, err := NewDBus().Lookup(bg, id); !errors.Is(err, ErrTooLong) {
		t.Fatalf("with an older usable match: err = %v, want ErrTooLong", err)
	}

	// A newer usable one is used, and the other matches are still counted.
	srv.Seed(id.Attrs(), "newer", 2000)
	got, err := NewDBus().Lookup(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Multiple {
		t.Fatal("three matching items must be reported as multiple")
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

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPrompt(fakesecret.PromptStall)
	err = NewDBus().Upsert(bg, id, pw)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want it to wrap ErrUnavailable", err)
	}
	if !errors.Is(err, ErrPromptTimeout) {
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

	saved := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	other := IdentityFor("/tmp/a.toml", config.Profile{ID: "bbbbbbbb-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "home", Host: "h", User: "u"})
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

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
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
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	if _, err := NewDBus().Presence(context.Background(), id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want it to wrap ErrUnavailable", err)
	}
}

// A keyring daemon that stops answering must not hold a read, a write or a
// delete past the caller's deadline. Before these took a context, a wedged
// keyring froze the TUI for good: its connect ran Lookup on the update loop,
// and nothing could end the wait.
func TestDBus_OperationsHonourTheContext(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()
	srv.StallEverything()

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDBus()
	for name, op := range map[string]func(context.Context) error{
		"Lookup": func(ctx context.Context) error { _, err := store.Lookup(ctx, id); return err },
		"Upsert": func(ctx context.Context) error { return store.Upsert(ctx, id, pw) },
		"Delete": func(ctx context.Context) error { return store.Delete(ctx, id) },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		start := time.Now()
		err := op(ctx)
		cancel()
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("%s took %v against a 150ms deadline", name, elapsed)
		}
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s: err = %v, want ErrUnavailable and the deadline", name, err)
		}
	}
}

// A caller that stops waiting for a prompt -- the user pressed esc -- is not
// the user refusing it, and the dialog must not be left on screen.
func TestDBus_CancelledPromptIsDismissed(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPrompt(fakesecret.PromptStall)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- NewDBus().Upsert(ctx, id, pw) }()
	// A cancel before Prompt is sent would leave no dialog to dismiss.
	waitForPrompt(t, srv)
	cancel()
	err = <-errc
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want ErrUnavailable and the cancellation", err)
	}
	if errors.Is(err, ErrPromptDismissed) {
		t.Fatalf("err = %v: a cancelled wait is not the user refusing", err)
	}
	if n := srv.Dismissed(); n != 1 {
		t.Fatalf("prompt dismissed %d times, want 1", n)
	}
	if n := srv.Stored(); n != 0 {
		t.Fatalf("%d items stored although the prompt was never answered", n)
	}
}

// waitForPrompt waits until the fake has seen Prompt, so a test can act while
// the client is in the middle of an operation, a prompt on screen.
func waitForPrompt(t *testing.T, srv *fakesecret.Server) {
	t.Helper()
	waitForCall(t, srv, "Prompt")
}

// waitForCall waits until the client has called method on the fake.
func waitForCall(t *testing.T, srv *fakesecret.Server, method string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(srv.Calls(), method) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the client never called %s; calls: %v", method, srv.Calls())
}

// gnome-keyring does not prompt for a write to a locked collection or a
// delete of a locked item; it refuses both with IsLocked. Wicket has to
// unlock and ask again, as libsecret does. It used to report the keyring as
// unavailable instead, so a save after a locked login never raised the
// unlock dialog at all (issue #101).
func TestDBus_WritesUnlockAKeyringThatRefusesThem(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()
	srv.RefuseLockedWrites()

	store := NewDBus()
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}

	srv.SetLocked(true)
	srv.SetPrompt(fakesecret.PromptDismiss)
	err = store.Upsert(bg, id, pw)
	if !errors.Is(err, ErrLocked) || !errors.Is(err, ErrPromptDismissed) || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Upsert, unlock refused: err = %v, want ErrLocked wrapping the dismissal", err)
	}
	if n := srv.Stored(); n != 0 {
		t.Fatalf("%d items stored after a refused unlock", n)
	}

	srv.SetPrompt(fakesecret.PromptAccept)
	if err := store.Upsert(bg, id, pw); err != nil {
		t.Fatalf("Upsert, unlock accepted: %v", err)
	}
	if n := srv.Stored(); n != 1 {
		t.Fatalf("%d items stored after an accepted unlock, want 1", n)
	}
	if n := countCalls(srv, "CreateItem"); n != 3 {
		t.Fatalf("CreateItem called %d times, want 3: one refused, one retried after each unlock", n)
	}

	srv.SetLocked(true)
	srv.SetPrompt(fakesecret.PromptDismiss)
	err = store.Delete(bg, id)
	if !errors.Is(err, ErrLocked) || !errors.Is(err, ErrPromptDismissed) {
		t.Fatalf("Delete, unlock refused: err = %v, want ErrLocked wrapping the dismissal", err)
	}
	if n := srv.Stored(); n != 1 {
		t.Fatalf("%d items stored after a refused delete, want 1", n)
	}

	srv.SetPrompt(fakesecret.PromptAccept)
	if err := store.Delete(bg, id); err != nil {
		t.Fatalf("Delete, unlock accepted: %v", err)
	}
	if n := srv.Stored(); n != 0 {
		t.Fatalf("%d items stored after an accepted delete, want 0", n)
	}

	// An unlock that goes through and still leaves the item locked is a
	// lock that held, not a keyring out of reach.
	srv.KeepLocked(srv.Seed(id.Attrs(), "s3cret", 100))
	err = store.Delete(bg, id)
	if !errors.Is(err, ErrLocked) || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Delete, item still locked after the unlock: err = %v, want ErrLocked", err)
	}
	if n := srv.Stored(); n != 1 {
		t.Fatalf("%d items stored after a delete the lock refused, want 1", n)
	}
}

func countCalls(srv *fakesecret.Server, method string) int {
	n := 0
	for _, c := range srv.Calls() {
		if c == method {
			n++
		}
	}
	return n
}

// A match that was open at SearchItems can be locked again before
// GetSecrets, which then leaves it out without a word. The older match that
// is left must not stand in for it (issue #102).
func TestDBus_LookupWithAMatchLockedBeforeTheReadIgnoresAnOlderOne(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	srv.Seed(id.Attrs(), "older", 100)
	srv.RelockBeforeRead(srv.Seed(id.Attrs(), "newer", 200))

	got, err := NewDBus().Lookup(bg, id)
	if !errors.Is(err, ErrLocked) {
		var buf bytes.Buffer
		_ = got.Password.WriteLine(&buf)
		t.Fatalf("relocked match: password %q, err = %v, want ErrLocked", buf.String(), err)
	}
}

// When the session bus goes away under an open prompt, godbus closes the
// signal channel. Wicket used to spin on it until the prompt timed out, two
// minutes of a busy core, and then blame the user for not answering. It is
// the bus that failed, and that is said at once.
func TestDBus_LostBusDuringAPromptIsUnavailable(t *testing.T) {
	// LookupMidCall loses the bus before the Prompt call itself is answered,
	// which once came back as ErrLocked: the call failed, not the unlock.
	for name, mode := range map[string]fakesecret.PromptMode{"Lookup": fakesecret.PromptStall, "Upsert": fakesecret.PromptStall, "LookupMidCall": fakesecret.PromptHang} {
		op := strings.TrimSuffix(name, "MidCall")
		t.Run(name, func(t *testing.T) {
			_, srv, stop := fakesecret.Start(t)
			cleanup := sync.OnceFunc(stop)
			defer cleanup()
			id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
			pw, err := NewPassword("s3cret")
			if err != nil {
				t.Fatal(err)
			}
			store := NewDBus()
			if op == "Lookup" {
				if err := store.Upsert(bg, id, pw); err != nil {
					t.Fatal(err)
				}
				srv.SetLocked(true)
			}
			srv.SetPrompt(mode)

			errc := make(chan error, 1)
			go func() {
				if op == "Lookup" {
					_, err := store.Lookup(bg, id)
					errc <- err
					return
				}
				errc <- store.Upsert(bg, id, pw)
			}()
			waitForPrompt(t, srv)
			cleanup()
			select {
			case err := <-errc:
				if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrLocked) || errors.Is(err, ErrPromptTimeout) {
					t.Fatalf("err = %v, want ErrUnavailable, not a lock or a timeout", err)
				}
			case <-time.After(time.Second):
				t.Fatal("still waiting a second after the bus went away")
			}
		})
	}
}

// A keyring daemon that dies while an unlock's Unlock or Prompt call waits
// on it, with the session bus still up, gets the call answered by the bus:
// NoReply. That error is the transport's, not the keyring refusing, so it is
// unavailable and never ErrLocked (issue #147). The fake holds the call and
// drops its connection under it, so the bus always gets its NoReply out.
func TestDBus_DaemonLostDuringAnUnlockIsUnavailable(t *testing.T) {
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	ops := []struct {
		name  string
		setup func(*fakesecret.Server)
		op    func(*DBus) error
	}{
		{"lookup", func(srv *fakesecret.Server) {
			srv.Seed(id.Attrs(), "s3cret", 100)
		}, func(d *DBus) error { _, err := d.Lookup(bg, id); return err }},
		{"upsert", func(srv *fakesecret.Server) {
			srv.RefuseLockedWrites()
		}, func(d *DBus) error { return d.Upsert(bg, id, pw) }},
		{"delete", func(srv *fakesecret.Server) {
			srv.Seed(id.Attrs(), "s3cret", 100)
			srv.RefuseLockedWrites()
		}, func(d *DBus) error { return d.Delete(bg, id) }},
	}
	for _, tc := range ops {
		for _, held := range []string{"Unlock", "Prompt"} {
			t.Run(tc.name+"/"+held, func(t *testing.T) {
				_, srv, cleanup := fakesecret.Start(t)
				defer cleanup()
				tc.setup(srv)
				srv.SetLocked(true)
				srv.SetPrompt(fakesecret.PromptStall)
				srv.Hold(held)

				errc := make(chan error, 1)
				go func() { errc <- tc.op(NewDBus()) }()
				waitForCall(t, srv, held)
				srv.Disconnect()
				var err error
				select {
				case err = <-errc:
				case <-time.After(5 * time.Second):
					t.Fatal("still waiting after the daemon went away")
				}
				var reply dbus.Error
				if !errors.As(err, &reply) || reply.Name != "org.freedesktop.DBus.Error.NoReply" {
					t.Fatalf("err = %v, want the bus's NoReply", err)
				}
				if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrLocked) {
					t.Fatalf("err = %v, want ErrUnavailable, not ErrLocked", err)
				}
			})
		}
	}
}

// An error reply to Unlock is the keyring's own answer, so the lock held,
// unless it is one the bus sends in the keyring's place, as it does when the
// daemon has gone: that is unavailable.
func TestDBus_UnlockErrorReplyIsLockedUnlessTheBusSentIt(t *testing.T) {
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	for _, tc := range []struct {
		name   string
		locked bool
	}{
		{ssErrIsLocked, true},
		{"org.freedesktop.DBus.Error.ServiceUnknown", false},
		{"org.freedesktop.DBus.Error.NameHasNoOwner", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, srv, cleanup := fakesecret.Start(t)
			defer cleanup()
			srv.Seed(id.Attrs(), "s3cret", 100)
			srv.SetLocked(true)
			srv.FailUnlock(tc.name)

			_, err := NewDBus().Lookup(bg, id)
			var reply dbus.Error
			if !errors.As(err, &reply) || reply.Name != tc.name {
				t.Fatalf("err = %v, want the %s reply", err, tc.name)
			}
			if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrLocked) != tc.locked {
				t.Fatalf("err = %v, want ErrUnavailable, and ErrLocked %v", err, tc.locked)
			}
		})
	}
}

// A keyring daemon replies to Prompt as soon as its dialog is up. One that
// dies after that, or hands the name to another daemon, never sends
// Completed, and Wicket used to wait out the prompt timeout and report the
// keyring locked (issue #149). It is unavailable, said at once. The fake
// goes only once the bus has passed its reply on, so the client is always
// past the Prompt call; the timeout stays long, so waiting it out fails.
func TestDBus_DaemonLostUnderAnOpenPromptIsUnavailable(t *testing.T) {
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	ops := []struct {
		name  string
		setup func(*fakesecret.Server)
		op    func(*DBus) error
	}{
		// Lookup's prompt is the unlock's, whose timeout was ErrLocked.
		{"lookup", func(srv *fakesecret.Server) {
			srv.Seed(id.Attrs(), "s3cret", 100)
			srv.SetLocked(true)
		}, func(d *DBus) error { _, err := d.Lookup(bg, id); return err }},
		{"upsert", func(*fakesecret.Server) {}, func(d *DBus) error { return d.Upsert(bg, id, pw) }},
		{"delete", func(srv *fakesecret.Server) {
			srv.Seed(id.Attrs(), "s3cret", 100)
		}, func(d *DBus) error { return d.Delete(bg, id) }},
	}
	losses := []struct {
		name string
		lose func(*fakesecret.Server) error
		// dismissed is whether the daemon is still there to take its
		// dialog down.
		dismissed int
	}{
		{"disconnect", func(srv *fakesecret.Server) error { srv.Disconnect(); return nil }, 0},
		{"dropname", func(srv *fakesecret.Server) error { return srv.DropName(ssBus) }, 1},
	}
	for _, tc := range ops {
		for _, loss := range losses {
			t.Run(tc.name+"/"+loss.name, func(t *testing.T) {
				_, srv, cleanup := fakesecret.Start(t)
				defer cleanup()
				tc.setup(srv)
				srv.SetPrompt(fakesecret.PromptStall)
				replied := srv.WatchReply(t, "Prompt")

				errc := make(chan error, 1)
				go func() { errc <- tc.op(NewDBus()) }()
				select {
				case <-replied:
				case <-time.After(5 * time.Second):
					t.Fatalf("the fake never replied to Prompt; calls: %v", srv.Calls())
				}
				if err := loss.lose(srv); err != nil {
					t.Fatal(err)
				}
				select {
				case err = <-errc:
				case <-time.After(5 * time.Second):
					t.Fatal("still waiting after the keyring daemon went away")
				}
				if !errors.Is(err, errDaemonLost) {
					t.Fatalf("err = %v, want the daemon's loss under the prompt", err)
				}
				if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrLocked) || errors.Is(err, ErrPromptTimeout) {
					t.Fatalf("err = %v, want ErrUnavailable, not a lock or a timeout", err)
				}
				if n := srv.Dismissed(); n != loss.dismissed {
					t.Fatalf("dismissed %d times, want %d", n, loss.dismissed)
				}
			})
		}
	}
}

// Only the bus can say the keyring has gone, and only about the keyring's
// name. A NameOwnerChanged forged by another process, sent straight to the
// client, or a real one about another name the daemon held, must not end
// the wait: the prompt is still open, and its answer still counts.
func TestDBus_PromptIgnoresANameOwnerChangedFromAnotherSenderOrName(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.TakeName("org.freedesktop.secrets.other"); err != nil {
		t.Fatal(err)
	}
	srv.SetPrompt(fakesecret.PromptStall)
	errc := make(chan error, 1)
	go func() { errc <- NewDBus().Upsert(bg, id, pw) }()
	waitForPrompt(t, srv)

	forger, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = forger.Close() }()
	var names []string
	if err := forger.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		t.Fatal(err)
	}
	var keyring string
	if err := forger.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, ssBus).Store(&keyring); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !strings.HasPrefix(name, ":") || name == keyring || name == forger.Names()[0] {
			continue
		}
		if err := forger.Send(forgedNameOwnerChanged(name, keyring), nil).Err; err != nil {
			t.Fatal(err)
		}
	}
	// The bus handles one connection's messages in order, so once this
	// reply is back the forgeries are on their way to the client, ahead of
	// anything the fake sends from here on.
	if err := forger.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
		t.Fatal(err)
	}
	if err := srv.DropName("org.freedesktop.secrets.other"); err != nil {
		t.Fatal(err)
	}
	srv.AcceptPrompt()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("err = %v, want the write to go through when the prompt is answered", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting after the prompt was answered")
	}
	if n := srv.Stored(); n != 1 {
		t.Fatalf("%d items stored, want the accepted write", n)
	}
}

// The watch starts before runPrompt asks who owns the name, so it can catch
// a change from before then: the name passing to the daemon now holding it.
// That is not the daemon leaving. The bus cannot be timed to send one into
// that gap, so ownerLost is checked directly, along with the rest of what
// marks the bus's own signal.
func TestOwnerLost(t *testing.T) {
	const owner = ":1.7"
	sig := func(sender, path, name string, body ...any) *dbus.Signal {
		return &dbus.Signal{Sender: sender, Path: dbus.ObjectPath(path), Name: name, Body: body}
	}
	for _, tc := range []struct {
		name string
		sig  *dbus.Signal
		want bool
	}{
		{"owner gone", sig(busName, busPath, busName+".NameOwnerChanged", ssBus, owner, ""), true},
		{"name passed on", sig(busName, busPath, busName+".NameOwnerChanged", ssBus, owner, ":1.9"), true},
		{"name passed to owner", sig(busName, busPath, busName+".NameOwnerChanged", ssBus, ":1.3", owner), false},
		{"another name", sig(busName, busPath, busName+".NameOwnerChanged", "org.example.other", owner, ""), false},
		{"another sender", sig(":1.8", busPath, busName+".NameOwnerChanged", ssBus, owner, ""), false},
		{"another path", sig(busName, "/", busName+".NameOwnerChanged", ssBus, owner, ""), false},
		{"another signal", sig(busName, busPath, busName+".NameLost", ssBus), false},
		{"short body", sig(busName, busPath, busName+".NameOwnerChanged", ssBus, owner), false},
	} {
		if got := ownerLost(tc.sig, owner); got != tc.want {
			t.Errorf("%s: ownerLost = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// forgedNameOwnerChanged is the bus's signal that owner has lost the Secret
// Service's name, addressed to dest alone and sent by whoever sends it.
func forgedNameOwnerChanged(dest, owner string) *dbus.Message {
	body := []any{ssBus, owner, ""}
	return &dbus.Message{
		Type: dbus.TypeSignal,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldPath:        dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/DBus")),
			dbus.FieldInterface:   dbus.MakeVariant("org.freedesktop.DBus"),
			dbus.FieldMember:      dbus.MakeVariant("NameOwnerChanged"),
			dbus.FieldDestination: dbus.MakeVariant(dest),
			dbus.FieldSignature:   dbus.MakeVariant(dbus.SignatureOf(body...)),
		},
		Body: body,
	}
}

// Completed decides whether a write went through, and any process of the
// same user can emit one on the prompt's path. Only the keyring's counts.
func TestDBus_PromptIgnoresACompletedFromAnotherSender(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	old := promptTimeout
	promptTimeout = 500 * time.Millisecond
	defer func() { promptTimeout = old }()

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPrompt(fakesecret.PromptStall)
	errc := make(chan error, 1)
	go func() { errc <- NewDBus().Upsert(bg, id, pw) }()
	waitForPrompt(t, srv)

	forger, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = forger.Close() }()
	if err := forger.Emit("/org/freedesktop/secrets/prompt/p1", ssPromptIface+".Completed", false, dbus.MakeVariant("")); err != nil {
		t.Fatal(err)
	}
	// The bus delivers a signal addressed to a connection whatever its match
	// rules say, so the forger also sends one straight to every other client.
	var names []string
	if err := forger.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		t.Fatal(err)
	}
	var keyring string
	if err := forger.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, "org.freedesktop.secrets").Store(&keyring); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !strings.HasPrefix(name, ":") || name == keyring || name == forger.Names()[0] {
			continue
		}
		if err := forger.Send(forgedCompleted(name), nil).Err; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-errc; !errors.Is(err, ErrPromptTimeout) {
		t.Fatalf("err = %v, want the prompt to time out: another sender's Completed must not count", err)
	}
	if n := srv.Stored(); n != 0 {
		t.Fatalf("%d items stored although the prompt was never answered", n)
	}
}

// A caller that gives up while Lookup's unlock prompt is open -- the user
// pressed esc -- is told so. It is not the keyring staying locked, and the
// dialog is dismissed on the way out.
func TestDBus_CancelledLookupPromptIsNotLocked(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()

	store := NewDBus()
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(bg, id, pw); err != nil {
		t.Fatal(err)
	}
	srv.SetLocked(true)
	srv.SetPrompt(fakesecret.PromptStall)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { _, err := store.Lookup(ctx, id); errc <- err }()
	waitForPrompt(t, srv)
	cancel()
	err = <-errc
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrUnavailable and the cancellation, not ErrLocked", err)
	}
	if n := srv.Dismissed(); n != 1 {
		t.Fatalf("prompt dismissed %d times, want 1", n)
	}
}

// The service shows a prompt's dialog when it receives Prompt, before it
// replies. A caller that gives up in between gets ctx's error from the call
// itself, not from the wait for Completed, and the dialog must still be
// dismissed: it is on screen, and the user pressed esc. The fake holds the
// reply for good, so the cancel always lands there.
func TestDBus_CancelBeforeThePromptRepliesDismisses(t *testing.T) {
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		setup func(*fakesecret.Server)
		op    func(context.Context, *DBus) error
	}{
		{"lookup", func(srv *fakesecret.Server) {
			srv.Seed(id.Attrs(), "s3cret", 100)
			srv.SetLocked(true)
		}, func(ctx context.Context, d *DBus) error { _, err := d.Lookup(ctx, id); return err }},
		{"upsert", func(*fakesecret.Server) {}, func(ctx context.Context, d *DBus) error { return d.Upsert(ctx, id, pw) }},
		{"delete", func(srv *fakesecret.Server) {
			srv.Seed(id.Attrs(), "s3cret", 100)
		}, func(ctx context.Context, d *DBus) error { return d.Delete(ctx, id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, srv, cleanup := fakesecret.Start(t)
			defer cleanup()
			tc.setup(srv)
			srv.SetPrompt(fakesecret.PromptHang)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			errc := make(chan error, 1)
			go func() { errc <- tc.op(ctx, NewDBus()) }()
			waitForPrompt(t, srv)
			cancel()
			err := <-errc
			if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrLocked) || errors.Is(err, ErrPromptDismissed) {
				t.Fatalf("err = %v, want ErrUnavailable and the cancellation, not the user refusing", err)
			}
			if n := srv.Dismissed(); n != 1 {
				t.Fatalf("prompt dismissed %d times, want 1", n)
			}
		})
	}
}

// A profile can match an open item and one behind a lock of its own. If the
// open one goes and the locked one stays, the password is still stored, and
// Delete must not report success: the TUI would say it was forgotten.
func TestDBus_DeleteThatLeavesAMatchBehindFails(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()
	srv.RefuseLockedWrites()
	srv.SetPrompt(fakesecret.PromptDismiss)

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	srv.Seed(id.Attrs(), "open", 100)
	srv.LockItem(srv.Seed(id.Attrs(), "locked", 200))

	err := NewDBus().Delete(bg, id)
	if !errors.Is(err, ErrLocked) || !errors.Is(err, ErrPromptDismissed) {
		t.Fatalf("err = %v, want ErrLocked wrapping the dismissal", err)
	}
	if n := srv.Stored(); n != 1 {
		t.Fatalf("%d items stored, want the locked one left", n)
	}
}

// forgedCompleted is a prompt's Completed signal, saying it was accepted,
// addressed to dest alone.
func forgedCompleted(dest string) *dbus.Message {
	body := []any{false, dbus.MakeVariant("")}
	return &dbus.Message{
		Type: dbus.TypeSignal,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldPath:        dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/secrets/prompt/p1")),
			dbus.FieldInterface:   dbus.MakeVariant(ssPromptIface),
			dbus.FieldMember:      dbus.MakeVariant("Completed"),
			dbus.FieldDestination: dbus.MakeVariant(dest),
			dbus.FieldSignature:   dbus.MakeVariant(dbus.SignatureOf(body...)),
		},
		Body: body,
	}
}

// A user who refuses the unlock is asked once, not once per duplicate
// entry: a restored backup can leave several items with the same details.
func TestDBus_DeleteAsksToUnlockOnce(t *testing.T) {
	_, srv, cleanup := fakesecret.Start(t)
	defer cleanup()
	srv.RefuseLockedWrites()
	srv.SetPrompt(fakesecret.PromptDismiss)

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	for i := range 3 {
		srv.Seed(id.Attrs(), "s3cret", int64(100+i))
	}
	srv.SetLocked(true)

	if err := NewDBus().Delete(bg, id); !errors.Is(err, ErrLocked) || !errors.Is(err, ErrPromptDismissed) {
		t.Fatalf("err = %v, want ErrLocked wrapping the dismissal", err)
	}
	if n := countCalls(srv, "Prompt"); n != 1 {
		t.Fatalf("%d unlock prompts, want 1", n)
	}
	if n := srv.Stored(); n != 3 {
		t.Fatalf("%d items stored, want all 3 left", n)
	}
}
