package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// Whatever the file holds, the patch either declines it or produces a file
// that reads back as exactly the document being saved, and never panics.
// render would catch a wrong patch and fall back, so this calls patch
// directly: a patch that only render's check stops is still a bug.
func FuzzSave(f *testing.F) {
	for _, name := range []string{"hand", "edit", "add", "delete-last"} {
		b, err := os.ReadFile(filepath.Join("testdata", "preserve", name+".toml"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, uint8(0))
		f.Add(b, uint8(1))
	}
	f.Add([]byte("profiles = [{ name = \"a\", host = \"h\", user = \"u\" }]\n"), uint8(0))
	f.Add([]byte("[[profiles]]\nname = \"a\"\nhost = \"h\"\nuser = \"u\"\nx = \"\"\"\n[[profiles]]\n\"\"\""), uint8(0))
	f.Add([]byte("[[profiles]]\nname = \"a\"\nhost = \"h\"\nuser = \"u\"\npassword = \"x\""), uint8(0))
	f.Fuzz(func(t *testing.T, src []byte, op uint8) {
		d, err := parseDocument(src)
		if err != nil {
			return
		}
		if _, err := d.typedProfiles(); err != nil {
			return
		}
		switch {
		case op%2 == 1 && len(d.profiles) > 0:
			d.removeProfile(0)
		case len(d.profiles) > 0:
			p, err := profileFromTable(d.profiles[0])
			if err != nil {
				return
			}
			p.User += "x"
			p.Size = "100%"
			d.setProfile(0, applyProfile(d.profiles[0], p))
			fallthrough
		default:
			d.addProfile(applyProfile(nil, Profile{Name: "fuzz-new", Host: "h", User: "u",
				Client: DefaultClient, Scale: 100}))
		}
		out, err := d.patch()
		if err == nil && !d.matches(out) {
			t.Fatalf("patch reads back as something else:\n%s", out)
		}
	})
}

// Cases from review, each once a lost comment, a stray line or a needless
// full rewrite. want is the whole file after the save.
func TestSave_ReviewCases(t *testing.T) {
	t.Parallel()
	prof := func(name string) string {
		return "[[profiles]]\nname = \"" + name + "\"\nhost = \"h\"\nuser = \"u\"\n"
	}
	cases := []struct {
		name, src string
		save      func(t *testing.T, c *Config)
		want      string
	}{
		{
			// A divider separated from the next header by a blank line is
			// about what follows, not about the profile deleted before it.
			name: "delete keeps the next section's divider",
			src:  "[general]\n\n# --- Work ---\n\n" + prof("a") + "\n# --- Home ---\n\n# the NAS\n" + prof("b"),
			save: func(t *testing.T, c *Config) { remove(t, c, "a") },
			want: "[general]\n\n# --- Work ---\n\n# --- Home ---\n\n# the NAS\n" + prof("b"),
		},
		{
			name: "delete keeps a note on the table after it",
			src:  "[general]\n\n" + prof("a") + "\n" + prof("b") + "\n# theme can also come from WICKET_THEME\n\n[ui]\ntheme = \"auto\"\n",
			save: func(t *testing.T, c *Config) { remove(t, c, "b") },
			want: "[general]\n\n" + prof("a") + "\n# theme can also come from WICKET_THEME\n\n[ui]\ntheme = \"auto\"\n",
		},
		{
			name: "delete keeps the file's own header comment",
			src:  "# my wicket config\n" + prof("a") + "\n" + prof("b"),
			save: func(t *testing.T, c *Config) { remove(t, c, "a") },
			want: "# my wicket config\n" + prof("b"),
		},
		{
			// reflect.DeepEqual has NaN unequal to itself, which made every
			// save of this file a full rewrite.
			name: "nan elsewhere",
			src:  "# keep me\nnote = nan\n\n" + prof("a"),
			save: func(t *testing.T, c *Config) {
				p, _ := c.Profile("a")
				p.User = "u2"
				upsert(t, c, p, "a")
			},
			want: "# keep me\nnote = nan\n\n" + strings.Replace(prof("a"), `"u"`, `"u2"`, 1),
		},
		{
			name: "quoted key with an escape",
			src:  "# keep me\n\"a\\tb\" = 1\n\n" + prof("a"),
			save: func(t *testing.T, c *Config) {
				p, _ := c.Profile("a")
				p.User = "u2"
				upsert(t, c, p, "a")
			},
			want: "# keep me\n\"a\\tb\" = 1\n\n" + strings.Replace(prof("a"), `"u"`, `"u2"`, 1),
		},
		{
			// A \r\n inside a multi-line string says nothing about the
			// file's line endings.
			name: "crlf only inside a string",
			src:  "note = \"\"\"a\r\nb\"\"\"\n\n" + prof("a"),
			save: func(t *testing.T, c *Config) {
				p, _ := c.Profile("a")
				p.Size = "100%"
				upsert(t, c, p, "a")
			},
			want: "note = \"\"\"a\r\nb\"\"\"\n\n" + prof("a") + "size = \"100%\"\n",
		},
		{
			// The new key goes after the last line that stays, not after a
			// secret the save removes.
			name: "new key after a removed secret on the last line",
			src:  strings.TrimSuffix(prof("a"), "\n") + "\npassword = \"x\"",
			save: func(t *testing.T, c *Config) {
				p, _ := c.Profile("a")
				p.Size = "100%"
				upsert(t, c, p, "a")
			},
			want: prof("a") + "size = \"100%\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeTOML(t, tc.src)
			c, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			tc.save(t, c)
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

// The encoder writes a local date or time shifted by the machine's zone, so
// a patch must not be judged against its output: east of UTC that threw the
// correct patch away and wrote 1979-05-26 for 1979-05-27.
func TestSave_KeepsLocalDates(t *testing.T) {
	orig := time.Local
	time.Local = time.FixedZone("AEST", 10*60*60)
	t.Cleanup(func() { time.Local = orig })

	src := "[general]\nd = 1979-05-27\nt = 07:32:00\nldt = 1979-05-27T07:32:00\n\n" +
		"[[profiles]]\nname = \"a\"\nhost = \"h\"\nuser = \"u\"\n"
	path := writeTOML(t, src)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Profile("a")
	p.User = "u2"
	upsert(t, c, p, "a")
	if c.Rewrote() {
		t.Fatal("rewrote the file in full")
	}
	got, _ := os.ReadFile(path)
	if want := strings.Replace(src, `"u"`, `"u2"`, 1); string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
