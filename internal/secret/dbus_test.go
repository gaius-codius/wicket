package secret

import (
	"bytes"
	"errors"
	"os"
	"testing"

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
