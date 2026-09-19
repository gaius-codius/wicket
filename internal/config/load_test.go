package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestOpen_Missing(t *testing.T) {
	t.Parallel()
	_, err := Open(filepath.Join(t.TempDir(), "nope.toml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want NotExist", err)
	}
}

func TestOpenOrCreate_CreatesStarter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	parent := filepath.Join(dir, "wicket")
	path := filepath.Join(parent, "config.toml")
	c, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Profiles()) != 0 {
		t.Fatalf("profiles = %d", len(c.Profiles()))
	}
	if st, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %04o", st.Mode().Perm())
	}
	if st, err := os.Stat(parent); err != nil {
		t.Fatal(err)
	} else if st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %04o", st.Mode().Perm())
	}
}

func TestOpenOrCreate_DoesNotOverwriteInvalid(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := []byte("this is { not toml")
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenOrCreate(path)
	if err == nil {
		t.Fatal("want load error")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("err type %T", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatalf("bytes changed:\n%s", got)
	}
}

func TestOpen_DuplicateNames(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
[[profiles]]
name = "work"
host = "h2"
user = "u2"
`)
	orig, _ := os.ReadFile(path)
	_, err := Open(path)
	if err == nil {
		t.Fatal("want duplicate error")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("err type %T: %v", err, err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, orig) {
		t.Fatal("bytes changed")
	}
}

func TestOpen_BadSizeAndClient(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
size = "nope"
`)
	if _, err := Open(path); err == nil {
		t.Fatal("bad size")
	}
	path = writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
client = "/tmp/x"
`)
	if _, err := Open(path); err == nil {
		t.Fatal("bad client")
	}
	path = writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
scale = 120
`)
	if _, err := Open(path); err == nil {
		t.Fatal("bad scale")
	}
}

func TestOpen_Unreadable(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, "[general]\n")
	orig, _ := os.ReadFile(path)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	_, err := Open(path)
	if err == nil {
		t.Fatal("want unreadable error")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("err type %T: %v", err, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatal("bytes changed")
	}
	_, err = OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, orig) {
		t.Fatal("OpenOrCreate replaced existing file")
	}
}

func TestOpen_Defaults(t *testing.T) {
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
	p, ok := c.Profile("work")
	if !ok {
		t.Fatal("missing")
	}
	if p.Client != DefaultClient {
		t.Fatalf("client %q", p.Client)
	}
	if p.Scale != 100 {
		t.Fatalf("scale %d", p.Scale)
	}
	if !p.DynamicResolution {
		t.Fatal("dynamic_resolution default")
	}
	if p.Fullscreen {
		t.Fatal("fullscreen default")
	}
}

func TestNameTaken(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
[[profiles]]
name = "lab"
host = "h2"
user = "u2"
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.NameTaken("work", "") {
		t.Fatal("work taken")
	}
	if c.NameTaken("work", "work") {
		t.Fatal("except work")
	}
	if c.NameTaken("other", "") {
		t.Fatal("other free")
	}
}

func writeTOML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
