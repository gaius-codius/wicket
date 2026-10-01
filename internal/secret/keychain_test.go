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

func newTestKeychain(t *testing.T) *Keychain {
	t.Helper()
	requireKeychainTest(t)

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
