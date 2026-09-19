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
