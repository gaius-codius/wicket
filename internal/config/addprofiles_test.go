package config

import (
	"path/filepath"
	"testing"
)

func TestAddProfiles_OneWrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	c, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	a := DefaultProfile()
	a.Name, a.Host, a.User = "a", "h1", "u1"
	b := DefaultProfile()
	b.Name, b.Host, b.User = "b", "h2", "u2"
	added, skipped, err := c.AddProfiles([]Profile{a, b}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || len(skipped) != 0 {
		t.Fatalf("added=%v skipped=%v", added, skipped)
	}
	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Profiles()) != 2 {
		t.Fatalf("profiles %d", len(c2.Profiles()))
	}
}

func TestAddProfiles_CollisionSkipAndRename(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dup := DefaultProfile()
	dup.Name, dup.Host, dup.User = "work", "h2", "u2"
	other := DefaultProfile()
	other.Name, other.Host, other.User = "lab", "h3", "u3"

	added, skipped, err := c.AddProfiles([]Profile{dup, other}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Name != "lab" {
		t.Fatalf("added %+v", added)
	}
	if len(skipped) != 1 || skipped[0].Reason != "name already used" {
		t.Fatalf("skipped %+v", skipped)
	}

	added, skipped, err = c.AddProfiles([]Profile{dup}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Name != "work-2" || len(skipped) != 0 {
		t.Fatalf("rename added=%+v skipped=%+v", added, skipped)
	}
	if _, ok := c.Profile("work-2"); !ok {
		t.Fatal("work-2 missing")
	}
}

func TestAddProfiles_InvalidSkippedNoWrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	c, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := DefaultProfile()
	bad.Name, bad.Host, bad.User = "bad", "bad host", "u"
	added, skipped, err := c.AddProfiles([]Profile{bad}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(skipped) != 1 {
		t.Fatalf("added=%v skipped=%v", added, skipped)
	}
	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Profiles()) != 0 {
		t.Fatalf("wrote invalid profile: %+v", c2.Profiles())
	}
}

func TestNextFreeName(t *testing.T) {
	t.Parallel()
	taken := map[string]bool{"work": true, "work-2": true}
	if got := nextFreeName("work", taken); got != "work-3" {
		t.Fatalf("got %q", got)
	}
	if got := nextFreeName("lab", taken); got != "lab" {
		t.Fatalf("got %q", got)
	}
}
