//go:build darwin && cgo

package secret

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/config"
)

// Keychain integration tests use a throwaway keychain file and never touch
// the login keychain. They stay off unless WICKET_KEYCHAIN_TEST=1 so a
// casual go test on a Mac cannot prompt or write somewhere lasting.
func requireKeychainTest(t *testing.T) {
	t.Helper()
	if os.Getenv("WICKET_KEYCHAIN_TEST") != "1" {
		t.Skip("set WICKET_KEYCHAIN_TEST=1 to run Keychain integration tests")
	}
}

// trackSearchList snapshots the user's Keychain search list and asserts it
// is unchanged when the test finishes (including failure paths).
func trackSearchList(t *testing.T) {
	t.Helper()
	before, err := copySearchList()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := copySearchList()
		if err != nil {
			releaseCFArray(before)
			t.Errorf("copySearchList after test: %v", err)
			return
		}
		same := searchListsEqual(before, after)
		releaseCFArray(after)
		releaseCFArray(before)
		if !same {
			t.Errorf("user keychain search list changed during the test")
		}
	})
}

func newTestKeychain(t *testing.T) *Keychain {
	t.Helper()
	requireKeychainTest(t)
	trackSearchList(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "wicket-test.keychain")
	pass := []byte("wicket-test-keychain-pass")
	store, err := newFileKeychain(path, pass)
	clear(pass)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.destroyFileKeychain)
	return store
}

func TestKeychain_CRUDAndIsolation(t *testing.T) {
	store := newTestKeychain(t)
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
	if got.Multiple {
		t.Fatal("single Upsert reported Multiple")
	}
	if _, err := store.Lookup(bg, idB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("isolation: %v", err)
	}
	pres, err := store.Presence(bg, idA)
	if err != nil {
		t.Fatal(err)
	}
	if pres != Saved {
		t.Fatalf("Presence = %v, want Saved", pres)
	}
	pres, err = store.Presence(bg, idB)
	if err != nil {
		t.Fatal(err)
	}
	if pres != NotSaved {
		t.Fatalf("Presence isolation = %v, want NotSaved", pres)
	}
	if err := store.Delete(bg, idA); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(bg, idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	if err := store.Delete(bg, idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete: %v, want ErrNotFound", err)
	}
}

func TestKeychain_UpsertReplaces(t *testing.T) {
	store := newTestKeychain(t)
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work"})
	pw1, _ := NewPassword("first")
	pw2, _ := NewPassword("second")
	if err := store.Upsert(bg, id, pw1); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(bg, id, pw2); err != nil {
		t.Fatal(err)
	}
	got, err := store.Lookup(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "second\n" {
		t.Fatalf("lookup %q, want second", buf.String())
	}
	if got.Multiple {
		t.Fatal("Upsert left multiple items")
	}
}

func TestKeychain_LookupPrefersTheNewestOfSeveralMatches(t *testing.T) {
	requireKeychainTest(t)
	trackSearchList(t)

	dir := t.TempDir()
	pass := []byte("wicket-test-keychain-pass")
	lookup, older, newer, cleanup, err := newMultiFileKeychain(
		filepath.Join(dir, "older.keychain"),
		filepath.Join(dir, "newer.keychain"),
		pass,
	)
	clear(pass)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pwOlder, err := NewPassword("older")
	if err != nil {
		t.Fatal(err)
	}
	pwNewer, err := NewPassword("newer")
	if err != nil {
		t.Fatal(err)
	}
	if err := older.Upsert(bg, id, pwOlder); err != nil {
		t.Fatal(err)
	}
	// Keychain modification dates are second-granularity; wait so "newer" sorts first.
	time.Sleep(1100 * time.Millisecond)
	if err := newer.Upsert(bg, id, pwNewer); err != nil {
		t.Fatal(err)
	}

	got, err := lookup.Lookup(bg, id)
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

	// Delete must clear every match across both keychains (MatchLimitAll).
	if err := lookup.Delete(bg, id); err != nil {
		t.Fatal(err)
	}
	if _, err := lookup.Lookup(bg, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete Lookup: %v, want ErrNotFound", err)
	}
	pres, err := lookup.Presence(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if pres != NotSaved {
		t.Fatalf("after Delete Presence = %v, want NotSaved", pres)
	}
}

func TestKeychain_CancelledContext(t *testing.T) {
	store := newTestKeychain(t)
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"})
	pw, _ := NewPassword("x")
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Lookup(done, id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Lookup: %v, want ErrUnavailable", err)
	}
	if err := store.Upsert(done, id, pw); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Upsert: %v, want ErrUnavailable", err)
	}
	if err := store.Delete(done, id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Delete: %v, want ErrUnavailable", err)
	}
	if _, err := store.Presence(done, id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Presence: %v, want ErrUnavailable", err)
	}
}

// Cancelling between the attribute query and the password fetch must stop
// Lookup before the second SecItemCopyMatching and return ErrUnavailable.
func TestKeychain_LookupCancelBetweenSteps(t *testing.T) {
	store := newTestKeychain(t)
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(bg, id, pw); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prev := lookupBetweenSteps
	lookupBetweenSteps = func() { cancel() }
	t.Cleanup(func() { lookupBetweenSteps = prev })

	_, err = store.Lookup(ctx, id)
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Lookup: %v, want ErrUnavailable wrapping context.Canceled", err)
	}
}

// Cancelling while a SecItem* worker is mid-call must return ErrUnavailable
// without use-after-free: the worker CFRetains caller-owned dictionaries, and
// the caller's defer CFRelease may run as soon as cancel returns.
func TestKeychain_CancelDuringSecItem(t *testing.T) {
	store := newTestKeychain(t)
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(bg, id, pw); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	proceed := make(chan struct{})
	prev := secItemInFlight
	secItemInFlight = func() {
		select {
		case <-started:
			// already closed (e.g. second SecItem in Lookup)
		default:
			close(started)
		}
		<-proceed
	}
	t.Cleanup(func() {
		secItemInFlight = prev
		select {
		case <-proceed:
		default:
			close(proceed)
		}
	})

	errc := make(chan error, 1)
	go func() {
		_, err := store.Lookup(ctx, id)
		errc <- err
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SecItem worker to start")
	}
	cancel()

	// Wait for Lookup to return while the worker is still blocked. The
	// caller's defer CFRelease of the query runs here; without CFRetain the
	// worker would UAF when proceed is closed below.
	var lookupErr error
	select {
	case lookupErr = <-errc:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cancelled Lookup")
	}
	if !errors.Is(lookupErr, ErrUnavailable) || !errors.Is(lookupErr, context.Canceled) {
		t.Fatalf("Lookup: %v, want ErrUnavailable wrapping context.Canceled", lookupErr)
	}

	close(proceed)

	// Store must still work after the cancelled in-flight call drained.
	got, err := store.Lookup(bg, id)
	if err != nil {
		t.Fatalf("Lookup after cancel drain: %v", err)
	}
	var buf bytes.Buffer
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "s3cret\n" {
		t.Fatalf("password = %q", buf.String())
	}
}

func TestDefault_IsKeychain(t *testing.T) {
	if _, ok := Default().(*Keychain); !ok {
		t.Fatalf("Default() = %T, want *Keychain", Default())
	}
}

func TestAccountAttr_FoldsConfig(t *testing.T) {
	id := Identity{Service: "wicket", Config: "/tmp/a.toml", ProfileID: "pid"}
	if accountAttr(id) != "pid\x1f/tmp/a.toml" {
		t.Fatalf("accountAttr = %q", accountAttr(id))
	}
}
