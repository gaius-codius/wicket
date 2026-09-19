package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSave_Atomic0600(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, "[general]\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := validProfile()
	if err := c.Upsert(p, ""); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %04o", st.Mode().Perm())
	}
}

func TestUpsert_RoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	c, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	p := Profile{
		Name:              "work",
		Host:              "192.168.1.20",
		User:              "jdoe",
		Domain:            "CORP",
		Client:            "xfreerdp3",
		Size:              "1920x1080",
		Fullscreen:        true,
		DynamicResolution: false,
		Scale:             140,
	}
	if err := c.Upsert(p, ""); err != nil {
		t.Fatal(err)
	}
	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := c2.Profile("work")
	if !ok {
		t.Fatal("missing")
	}
	if got != p {
		t.Fatalf("got %+v want %+v", got, p)
	}
}

func TestUpsert_DuplicateRejected(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := validProfile()
	p.Name = "work"
	p.Host = "other"
	if err := c.Upsert(p, ""); err == nil {
		t.Fatal("want duplicate")
	}
	p.Name = "lab"
	if err := c.Upsert(p, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(p, "lab"); err != nil {
		t.Fatal(err)
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("work"); err != nil {
		t.Fatal(err)
	}
	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Profiles()) != 0 {
		t.Fatal("still there")
	}
}
