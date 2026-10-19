package exporter

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/importer"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

func minimalProfile() config.Profile {
	p := config.DefaultProfile()
	p.ID = "11111111-2222-4333-8444-555555555555"
	p.Name = "work"
	p.Host = "rdp.example.com"
	p.User = "alice"
	return p
}

// sharesProfile has the clipboard off and shares in each form a profile
// holds: a bare folder, a named one, and one under ~.
func sharesProfile() config.Profile {
	p := minimalProfile()
	p.Name = "lab"
	p.Host = "[2001:db8::7]:3390"
	p.User = "bob"
	p.Domain = "CORP"
	p.Size = "1600x900"
	p.Fullscreen = true
	p.Multimon = true
	p.Clipboard = false
	p.Shares = []config.Share{
		{Path: "/srv/wicket-export-plain"},
		{Path: "/srv/wicket-export-docs", Name: "docs"},
		{Path: "~/projects", Name: "proj"},
	}
	return p
}

// The text of each exported file is fixed by a golden file, with the home
// folder a ~ share expands to written as $HOME.
func TestRDP_Golden(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		p    config.Profile
	}{
		{"minimal", minimalProfile()},
		{"shares-noclip", sharesProfile()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, omitted := RDP(tc.p)
			if len(omitted) != 0 {
				t.Errorf("omitted %v, want none", omitted)
			}
			golden(t, filepath.Join("testdata", tc.name+".rdp.txt"), strings.ReplaceAll(text, home, "$HOME"))
		})
	}
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An exported file imports back as the same profile: every field the file
// carries comes back as it went out, and the rest are the import's defaults,
// as for any other file. A ~ share comes back as the folder it names, which
// is what FreeRDP reads.
func TestRDP_ImportsBack(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []config.Profile{minimalProfile(), sharesProfile()} {
		text, _ := RDP(p)
		imp, skip, err := importer.ParseRDP(Encode(text), p.Name)
		if err != nil || skip.Reason != "" || len(imp.Refused) != 0 {
			t.Fatalf("%s: err=%v skip=%v refused=%v", p.Name, err, skip, imp.Refused)
		}
		want := p.Clone()
		want.ID = ""
		for i, s := range want.Shares {
			if strings.HasPrefix(s.Path, "~/") {
				want.Shares[i].Path = filepath.Join(home, s.Path[2:])
			}
		}
		if !imp.Equal(want) {
			t.Errorf("%s: imported\n%+v\nwant\n%+v", p.Name, imp.Profile, want)
		}
	}
}

// No password, keyring identity or profile id reaches the file: it holds
// only the keys export means to write, and the id appears nowhere.
func TestRDP_WritesNoSecretOrID(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		"full address": true, "username": true, "domain": true,
		"screen mode id": true, "desktopwidth": true, "desktopheight": true,
		"use multimon": true, "redirectclipboard": true, "drivestoredirect": true,
	}
	for _, p := range []config.Profile{minimalProfile(), sharesProfile()} {
		text, _ := RDP(p)
		if strings.Contains(text, p.ID) {
			t.Errorf("%s: id in the file:\n%s", p.Name, text)
		}
		for line := range strings.Lines(text) {
			key, _, _ := strings.Cut(line, ":")
			if !allowed[key] {
				t.Errorf("%s: unexpected key %q", p.Name, key)
			}
		}
	}
}

// What .rdp cannot hold is named, so the export can report it, and the rest
// of the profile is still written.
func TestRDP_Omissions(t *testing.T) {
	t.Parallel()
	p := minimalProfile()
	p.Size = "80%"
	p.ShareHome = true
	p.Scale = 140
	p.DynamicResolution = false
	p.Shares = []config.Share{
		{Path: "/srv/a;b"},
		{Path: "/srv/(x)"},
		{Path: "/srv/kept", Name: "kept"},
		{Path: "~"},
		{Path: "/"},
		{Path: "/proc/self/cwd"},
	}
	text, omitted := RDP(p)
	var got []string
	for _, o := range omitted {
		got = append(got, o.Setting)
	}
	want := []string{"size 80%", `share "/srv/a;b"`, `share "/srv/(x)"`, `share "~"`, `share "/"`, `share "/proc/self/cwd"`, "share_home", "scale 140", "dynamic resolution off"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("omitted %q, want %q", got, want)
	}
	if !strings.Contains(text, "drivestoredirect:s:/srv/kept(kept)\n") {
		t.Errorf("kept share missing:\n%s", text)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, home) || strings.Contains(text, ":/;") || strings.HasSuffix(text, ":/\n") || strings.Contains(text, "/proc") {
		t.Errorf("a refused share written:\n%s", text)
	}
	for _, bad := range []string{"desktopwidth", "%", "a;b", "(x)"} {
		if strings.Contains(text, bad) {
			t.Errorf("%q written:\n%s", bad, text)
		}
	}
}

// With the home folder unknown nothing tells home from any other folder, so
// no share is written, as connect would share none.
func TestRDP_NoHomeWritesNoShares(t *testing.T) {
	t.Setenv("HOME", "")
	p := minimalProfile()
	p.Shares = []config.Share{{Path: "/srv/data"}, {Path: "~/docs"}}
	text, omitted := RDP(p)
	if strings.Contains(text, "drivestoredirect") || len(omitted) != 2 {
		t.Fatalf("omitted %v, text:\n%s", omitted, text)
	}
}

// Multimon writes full screen even on a profile built without it, which only
// a caller in Go can make: a loaded config turns full screen on beside it.
// A size wicket's import would refuse is left out, so the file still
// imports back.
func TestRDP_OversizeIsLeftOut(t *testing.T) {
	t.Parallel()
	for _, size := range []string{"70000x1080", "1920x65536"} {
		p := minimalProfile()
		p.Size = size
		text, omitted := RDP(p)
		if strings.Contains(text, "desktopwidth") || len(omitted) != 1 || omitted[0].Setting != "size "+size {
			t.Errorf("%s: omitted %v, text:\n%s", size, omitted, text)
		}
	}
	p := minimalProfile()
	p.Size = "65535x65535"
	if text, omitted := RDP(p); !strings.Contains(text, "desktopwidth:i:65535\n") || len(omitted) != 0 {
		t.Errorf("65535x65535: omitted %v, text:\n%s", omitted, text)
	}
}

func TestRDP_MultimonIsFullScreen(t *testing.T) {
	t.Parallel()
	p := minimalProfile()
	p.Multimon = true
	text, _ := RDP(p)
	if !strings.Contains(text, "screen mode id:i:2\n") || !strings.Contains(text, "use multimon:i:1\n") {
		t.Fatalf("text:\n%s", text)
	}
}

func TestEncode_UTF16LEWithCRLF(t *testing.T) {
	t.Parallel()
	text := "full address:s:h\nusername:s:Zoë\n"
	got := Encode(text)
	if !bytes.HasPrefix(got, []byte{0xFF, 0xFE}) || len(got)%2 != 0 {
		t.Fatalf("no UTF-16LE byte-order mark: % x", got[:min(4, len(got))])
	}
	units := make([]uint16, (len(got)-2)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(got[2+2*i:])
	}
	if back := string(utf16.Decode(units)); back != strings.ReplaceAll(text, "\n", "\r\n") {
		t.Fatalf("decoded %q", back)
	}
}

func TestFileName(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"work":                         "work.rdp",
		"My Server":                    "My Server.rdp",
		"a.b":                          "a.b.rdp",
		"../../etc/passwd":             "_.._etc_passwd.rdp",
		`C:\Windows\x`:                 "C__Windows_x.rdp",
		"a/b":                          "a_b.rdp",
		"what?*<>|\"":                  "what______.rdp",
		".hidden":                      "hidden.rdp",
		"..":                           "profile.rdp",
		"trailing. ":                   "trailing.rdp",
		"CON":                          "_CON.rdp",
		"nul.txt":                      "_nul.txt.rdp",
		"com7":                         "_com7.rdp",
		"COM0":                         "COM0.rdp",
		"console":                      "console.rdp",
		"Zoë's box":                    "Zoë_s box.rdp",
		"\u202Eevil":                   "_evil.rdp",
		strings.Repeat("é", 150):       strings.Repeat("é", 100) + ".rdp",
		strings.Repeat("a", 199) + "é": strings.Repeat("a", 199) + ".rdp",
	} {
		if got := FileName(name); got != want {
			t.Errorf("FileName(%q) = %q, want %q", name, got, want)
		}
	}
}

// Two names that come out as one file name get one file each, also when
// they differ only in case, as they would on macOS or Windows.
func TestUnique(t *testing.T) {
	t.Parallel()
	taken := map[string]bool{}
	var got []string
	for _, name := range []string{"a/b", "a_b", "A_B", "c"} {
		got = append(got, Unique(name, taken))
	}
	want := []string{"a_b.rdp", "a_b-2.rdp", "A_B-3.rdp", "c.rdp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWriteFile_KeepsExistingUnlessForced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "work.rdp")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err %v, want fs.ErrExist", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatalf("file %q after a write without force", got)
	}
	if err := WriteFile(path, []byte("new"), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("file %q after a forced write", got)
	}
	assertPrivate(t, path)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("dir holds %v (err %v), want only work.rdp", entries, err)
	}
}

func TestWriteFile_New(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "work.rdp")
	if err := WriteFile(path, []byte("data"), false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "data" {
		t.Fatalf("file %q", got)
	}
	assertPrivate(t, path)
}

// A link at the path is neither followed nor written through: without force
// it is an existing file, and with force it is replaced by the file.
func TestWriteFile_DoesNotFollowALink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"dangling.rdp", "live.rdp"} {
		path := filepath.Join(dir, link)
		to := target
		if link == "dangling.rdp" {
			to = filepath.Join(dir, "missing")
		}
		if err := os.Symlink(to, path); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(path, []byte("new"), false); !errors.Is(err, fs.ErrExist) {
			t.Errorf("%s: err %v, want fs.ErrExist", link, err)
		}
		if err := WriteFile(path, []byte("new"), true); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			t.Errorf("%s: not replaced by a file: %v %v", link, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a dangling link's target was created")
	}
	if got, _ := os.ReadFile(target); string(got) != "target" {
		t.Errorf("link target rewritten: %q", got)
	}
}

func TestWriteFile_ForceDoesNotReplaceAFolder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "work.rdp")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("x"), true); err == nil {
		t.Fatal("a folder was replaced")
	}
}

func assertPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode %o, want 600", path, perm)
	}
}
