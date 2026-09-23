package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata/preserve")

// A save edits config.toml in place: comments, blank lines, key order,
// quoting and alignment outside what changed come out byte for byte as
// they went in. Each case applies one save to hand.toml and compares the
// result with its golden file.
func TestSave_KeepsTheFileAsWritten(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		save func(t *testing.T, c *Config)
	}{
		{"edit", func(t *testing.T, c *Config) {
			p, _ := c.Profile("work")
			p.User = "alice"
			p.Scale = 180
			upsert(t, c, p, "work")
		}},
		{"edit-keys", func(t *testing.T, c *Config) {
			// Clearing domain drops its line; a new size and a non-default
			// setting go after the profile's last key.
			p, _ := c.Profile("work")
			p.Domain = ""
			p.Size = "100%"
			p.ShareHome = true
			upsert(t, c, p, "work")
		}},
		{"rename-lab", func(t *testing.T, c *Config) {
			p, _ := c.Profile("lab")
			p.Name = "lab-2"
			upsert(t, c, p, "lab")
		}},
		{"add", func(t *testing.T, c *Config) {
			upsert(t, c, Profile{Name: "new", Host: "h", User: "u", Client: DefaultClient,
				DynamicResolution: true, Scale: 100, Clipboard: true}, "")
		}},
		{"delete-middle", func(t *testing.T, c *Config) { remove(t, c, "lab") }},
		{"delete-first", func(t *testing.T, c *Config) { remove(t, c, "work") }},
		{"delete-last", func(t *testing.T, c *Config) { remove(t, c, "spare") }},
	}
	src, err := os.ReadFile(filepath.Join("testdata", "preserve", "hand.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeTOML(t, string(src))
			c, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			tc.save(t, c)
			if c.Rewrote() {
				t.Fatal("rewrote the file in full")
			}
			got, _ := os.ReadFile(path)
			golden(t, filepath.Join("testdata", "preserve", tc.name+".toml"), got)
		})
	}
}

func upsert(t *testing.T, c *Config, p Profile, except string) {
	t.Helper()
	if err := c.Upsert(p, except); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, c *Config, name string) {
	t.Helper()
	if err := c.Remove(name); err != nil {
		t.Fatal(err)
	}
}

func golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Profiles written as an inline array cannot be patched. The save still
// happens, as a full rewrite, and says so.
func TestSave_FallsBackToARewrite(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `# gone after the rewrite
profiles = [{ name = "work", host = "h", user = "u" }]

[general]
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Profile("work")
	p.User = "u2"
	upsert(t, c, p, "work")
	if !c.Rewrote() {
		t.Fatal("Rewrote() = false after a full rewrite")
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "# gone") || !strings.Contains(string(got), "[[profiles]]") {
		t.Fatalf("not a full rewrite:\n%s", got)
	}
	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if q, _ := c2.Profile("work"); q.User != "u2" {
		t.Fatalf("user %q", q.User)
	}
	// Once rewritten, the file is one a save can patch.
	p.Host = "h2"
	upsert(t, c2, p, "work")
	if c2.Rewrote() {
		t.Fatal("the rewritten file could not be patched")
	}
}

// A file with Windows line endings, or none at the end, keeps them.
func TestSave_LineEndings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, src, want string }{
		{
			"crlf",
			"[general]\r\n\r\n[[profiles]]\r\nname = \"work\"\r\nhost = \"h\"\r\nuser = \"u\"\r\n",
			"[general]\r\n\r\n[[profiles]]\r\nname = \"work\"\r\nhost = \"h\"\r\nuser = \"u\"\r\nsize = \"100%\"\r\n" +
				"\r\n[[profiles]]\r\nname = \"new\"\r\nhost = \"h\"\r\nuser = \"u\"\r\nclient = \"sdl-freerdp3\"\r\n" +
				"fullscreen = false\r\ndynamic_resolution = true\r\nscale = 100\r\n",
		},
		{
			"no final newline",
			"[general]\n\n[[profiles]]\nname = \"work\"\nhost = \"h\"\nuser = \"u\"",
			"[general]\n\n[[profiles]]\nname = \"work\"\nhost = \"h\"\nuser = \"u\"\nsize = \"100%\"\n" +
				"\n[[profiles]]\nname = \"new\"\nhost = \"h\"\nuser = \"u\"\nclient = \"sdl-freerdp3\"\n" +
				"fullscreen = false\ndynamic_resolution = true\nscale = 100\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeTOML(t, tc.src)
			c, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := c.Profile("work")
			p.Size = "100%"
			upsert(t, c, p, "work")
			upsert(t, c, Profile{Name: "new", Host: "h", User: "u", Client: DefaultClient,
				DynamicResolution: true, Scale: 100, Clipboard: true}, "")
			if c.Rewrote() {
				t.Fatal("rewrote the file in full")
			}
			got, _ := os.ReadFile(path)
			if string(got) != tc.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// The decoder has the last word on a patch. A secret inside an inline table
// is stripped from the document but is beyond a line-by-line patch, so the
// patched file would still hold it; the save must see that and rewrite the
// file instead.
func TestSave_RewritesWhenAPatchWouldBeWrong(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `[general]

[[profiles]]
name = "work"
host = "h"
user = "u"
extra = { password = "hunter2", keep = 1 }
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Profile("work")
	p.User = "u2"
	upsert(t, c, p, "work")
	if !c.Rewrote() {
		t.Fatal("Rewrote() = false")
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "hunter2") {
		t.Fatalf("secret survived:\n%s", got)
	}
}

// Whatever the file holds, a save either patches it into exactly what a
// full rewrite would say or falls back to that rewrite; it never panics.
func FuzzSave(f *testing.F) {
	for _, name := range []string{"hand", "edit", "add", "delete-last"} {
		b, err := os.ReadFile(filepath.Join("testdata", "preserve", name+".toml"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("profiles = [{ name = \"a\", host = \"h\", user = \"u\" }]\n"))
	f.Add([]byte("[[profiles]]\nname = \"a\"\nhost = \"h\"\nuser = \"u\"\nx = \"\"\"\n[[profiles]]\n\"\"\""))
	f.Fuzz(func(t *testing.T, src []byte) {
		d, err := parseDocument(src)
		if err != nil {
			return
		}
		if _, err := d.typedProfiles(); err != nil {
			return
		}
		if len(d.profiles) > 0 {
			p, err := profileFromTable(d.profiles[0])
			if err != nil {
				return
			}
			p.User += "x"
			p.Size = "100%"
			d.setProfile(0, applyProfile(d.profiles[0], p))
		}
		d.addProfile(applyProfile(nil, Profile{Name: "fuzz-new", Host: "h", User: "u",
			Client: DefaultClient, Scale: 100}))
		out, patched, err := d.render()
		if err != nil {
			return
		}
		full, _ := d.encode()
		if patched && !sameDocument(out, full) {
			t.Fatalf("patched file differs from the rewrite:\n%s", out)
		}
	})
}
