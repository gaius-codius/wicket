package config

import (
	"errors"
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

// A Config can be minutes old by the time the user saves. Writing the document
// captured at Open would erase whatever another editor changed in between.
func TestUpsert_KeepsAConcurrentEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[general]\n\n[[profiles]]\nname=\"a\"\nhost=\"ha\"\nuser=\"ua\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// Another editor adds a profile after we opened the file.
	theirs, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := theirs.Upsert(Profile{Name: "b", Host: "hb", User: "ub", Scale: 100, Client: "sdl-freerdp3"}, ""); err != nil {
		t.Fatal(err)
	}

	if err := mine.Upsert(Profile{Name: "c", Host: "hc", User: "uc", Scale: 100, Client: "sdl-freerdp3"}, ""); err != nil {
		t.Fatal(err)
	}

	after, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range after.Profiles() {
		names = append(names, p.Name)
	}
	if len(names) != 3 {
		t.Fatalf("profiles = %v, want a, b and c to survive", names)
	}
	for _, want := range []string{"a", "b", "c"} {
		if _, ok := after.Profile(want); !ok {
			t.Fatalf("profile %q lost; have %v", want, names)
		}
	}
}

// The duplicate-name check has to see the file as it is now, not as it was
// when this Config was opened.
func TestUpsert_RejectsANameAddedConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[general]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := theirs.Upsert(Profile{Name: "dup", Host: "h", User: "u", Scale: 100, Client: "sdl-freerdp3"}, ""); err != nil {
		t.Fatal(err)
	}
	err = mine.Upsert(Profile{Name: "dup", Host: "h2", User: "u2", Scale: 100, Client: "sdl-freerdp3"}, "")
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "name" {
		t.Fatalf("err = %v, want a name field error", err)
	}
}

// Deleting the config while a form is open should not cost the user the
// profile they just filled in.
func TestUpsert_RecreatesAConfigDeletedUnderIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[general]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(Profile{Name: "a", Host: "h", User: "u", Scale: 100, Client: "sdl-freerdp3"}, ""); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	after, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Profile("a"); !ok {
		t.Fatal("profile not written after the file was recreated")
	}
}
