package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

// exportConfig writes a config holding two profiles: a minimal one, and one
// with shares, the clipboard off and every monitor. The second's shares are
// real folders under a temporary one, so an import back keeps them.
func exportConfig(t *testing.T) string {
	t.Helper()
	shareDir := t.TempDir()
	for _, d := range []string{"plain", "docs"} {
		if err := os.Mkdir(filepath.Join(shareDir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := writeConnectConfig(t, fmt.Sprintf(`
[general]
[[profiles]]
id = "11111111-2222-4333-8444-555555555555"
name = "work"
host = "rdp.example.com"
user = "alice"
client = "sdl-freerdp3"

[[profiles]]
id = "66666666-7777-4888-9999-000000000000"
name = "lab"
host = "lab.example:3390"
user = "bob"
domain = "CORP"
client = "sdl-freerdp3"
size = "1600x900"
fullscreen = true
multimon = true
clipboard = false
shares = [{ path = %q }, { path = %q, name = "docs" }]
`, filepath.Join(shareDir, "plain"), filepath.Join(shareDir, "docs")))
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	return cfgPath
}

// noKeyring fails the test if anything opens the keyring.
func noKeyring(t *testing.T) {
	t.Helper()
	prev := openStore
	openStore = func() secret.Store {
		t.Error("export opened the keyring")
		return prev()
	}
	t.Cleanup(func() { openStore = prev })
}

// Every exported file imports back into wicket as the profile it came from,
// under a new id.
func TestExportRDP_ImportsBackAsTheSameProfile(t *testing.T) {
	noKeyring(t)
	cfgPath := exportConfig(t)
	out := t.TempDir()
	got := runCLI(t, []string{"export", "rdp", "--all", "--out", out})
	if got.code != 0 {
		t.Fatalf("export exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
	for _, name := range []string{"work", "lab"} {
		want := fmt.Sprintf("wrote %s (profile %q)\n", filepath.Join(out, name+".rdp"), name)
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout %q, want %q", got.stdout, want)
		}
	}
	if strings.Contains(got.stdout, "left out") {
		t.Errorf("a setting left out: %q", got.stdout)
	}

	before, err := config.Open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WICKET_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	got = runCLI(t, []string{"import", "rdp", filepath.Join(out, "work.rdp"), filepath.Join(out, "lab.rdp")})
	if got.code != 0 {
		t.Fatalf("import exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
	after, err := config.Open(os.Getenv("WICKET_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	for _, orig := range before.Profiles() {
		back, ok := after.Profile(orig.Name)
		if !ok {
			t.Fatalf("%s not imported: %+v", orig.Name, after.Profiles())
		}
		if back.ID == "" || back.ID == orig.ID {
			t.Errorf("%s: id %q, want a new one (was %q)", orig.Name, back.ID, orig.ID)
		}
		back.ID = orig.ID
		if !back.Equal(orig) {
			t.Errorf("%s imported back as\n%+v\nwant\n%+v", orig.Name, back, orig)
		}
	}
}

// The files hold no password, keyring entry or profile id, even when the
// config has a password typed into it, which its loader strips.
func TestExportRDP_WritesNoPassword(t *testing.T) {
	noKeyring(t)
	cfgPath := exportConfig(t)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `user = "alice"`, "user = \"alice\"\npassword = \"hunter2\"", 1))
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if got := runCLI(t, []string{"export", "rdp", "--all", "--out", out}); got.code != 0 {
		t.Fatalf("export exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
	for _, name := range []string{"work.rdp", "lab.rdp"} {
		text := readRDP(t, filepath.Join(out, name))
		for _, bad := range []string{"hunter2", "11111111-2222", "66666666-7777", "51:b"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s holds %q:\n%s", name, bad, text)
			}
		}
		// The shares' folders are under the test's own, which is named
		// after it, so only the keys are searched for the word.
		for line := range strings.Lines(text) {
			if key, _, _ := strings.Cut(line, ":"); strings.Contains(strings.ToLower(key), "password") {
				t.Errorf("%s has a password key: %q", name, line)
			}
		}
	}
}

// A name that would climb out of the folder, or that a file system would not
// take, gives a file of a safe name inside --out, and nothing else is made.
func TestExportRDP_UnsafeNameGivesSafeFile(t *testing.T) {
	cfgPath := writeConnectConfig(t, `
[general]
[[profiles]]
name = "../../x/CON: a*b?"
host = "h"
user = "u"
client = "sdl-freerdp3"
`)
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	parent := t.TempDir()
	out := filepath.Join(parent, "out", "deeper")
	got := runCLI(t, []string{"export", "rdp", "--out", out, "--", "../../x/CON: a*b?"})
	if got.code != 0 {
		t.Fatalf("export exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
	var files []string
	err := filepath.WalkDir(parent, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(parent, path)
			files = append(files, rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join("out", "deeper", "_.._x_CON_ a_b_.rdp")}
	if !slices.Equal(files, want) {
		t.Fatalf("files %q, want %q", files, want)
	}
	assertPrivateFile(t, filepath.Join(parent, want[0]))
}

// An existing file is kept, and the export exits 1, unless --force is given.
func TestExportRDP_KeepsExistingFileUnlessForced(t *testing.T) {
	exportConfig(t)
	out := t.TempDir()
	existing := filepath.Join(out, "work.rdp")
	if err := os.WriteFile(existing, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runCLI(t, []string{"export", "rdp", "work", "lab", "--out", out})
	if got.code != 1 || !strings.Contains(got.stderr, "kept "+existing) {
		t.Fatalf("exit %d stderr %q, want 1 and a kept file", got.code, got.stderr)
	}
	if data, _ := os.ReadFile(existing); string(data) != "mine" {
		t.Fatalf("existing file overwritten: %q", data)
	}
	if _, err := os.Stat(filepath.Join(out, "lab.rdp")); err != nil {
		t.Fatalf("lab not written beside the kept file: %v", err)
	}

	got = runCLI(t, []string{"export", "rdp", "work", "--out", out, "--force"})
	if got.code != 0 {
		t.Fatalf("--force exit %d stderr %q", got.code, got.stderr)
	}
	if text := readRDP(t, existing); !strings.Contains(text, "full address:s:rdp.example.com\r\n") {
		t.Fatalf("--force left %q", text)
	}
}

// Two profiles whose names give one file name get a file each.
func TestExportRDP_NamesThatClashGetTheirOwnFiles(t *testing.T) {
	cfgPath := writeConnectConfig(t, `
[general]
[[profiles]]
name = "a/b"
host = "one"
user = "u"
client = "sdl-freerdp3"

[[profiles]]
name = "a_b"
host = "two"
user = "u"
client = "sdl-freerdp3"
`)
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	out := t.TempDir()
	if got := runCLI(t, []string{"export", "rdp", "--all", "--out", out}); got.code != 0 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if text := readRDP(t, filepath.Join(out, "a_b.rdp")); !strings.Contains(text, "full address:s:one\r\n") {
		t.Errorf("a_b.rdp: %q", text)
	}
	if text := readRDP(t, filepath.Join(out, "a_b-2.rdp")); !strings.Contains(text, "full address:s:two\r\n") {
		t.Errorf("a_b-2.rdp: %q", text)
	}
}

// A setting .rdp cannot carry is named in the summary, and the file is still
// written.
func TestExportRDP_ReportsWhatIsLeftOut(t *testing.T) {
	cfgPath := writeConnectConfig(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
client = "sdl-freerdp3"
size = "80%"
share_home = true
`)
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	out := t.TempDir()
	got := runCLI(t, []string{"export", "rdp", "work", "--out=" + out})
	if got.code != 0 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	for _, want := range []string{"  left out size 80%: ", "  left out share_home: "} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout %q, want %q", got.stdout, want)
		}
	}
	readRDP(t, filepath.Join(out, "work.rdp"))
}

// An unknown profile stops the export before anything is written.
func TestExportRDP_UnknownProfileWritesNothing(t *testing.T) {
	exportConfig(t)
	out := filepath.Join(t.TempDir(), "out")
	got := runCLI(t, []string{"export", "rdp", "work", "nope", "--out", out})
	if got.code != 2 || !strings.Contains(got.stderr, `unknown profile "nope"`) {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("out folder made: %v", err)
	}
}

func TestExport_Usage(t *testing.T) {
	exportConfig(t)
	for _, args := range [][]string{
		{"export"},
		{"export", "rdp"},
		{"export", "remmina", "work"},
		{"export", "rdp", "--all", "work"},
		{"export", "rdp", "work", "--out"},
		{"export", "rdp", "work", "--out="},
		{"export", "rdp", "work", "--bogus"},
	} {
		if got := runCLI(t, args); got.code != 2 || !strings.Contains(got.stderr, "usage: wicket export") {
			t.Errorf("%q: exit %d stderr %q", args, got.code, got.stderr)
		}
	}
	if got := runCLI(t, []string{"export", "--help"}); got.code != 0 || !strings.Contains(got.stdout, "Passwords are never written") {
		t.Errorf("--help: exit %d stdout %q", got.code, got.stdout)
	}
}

func TestHelpMentionsExport(t *testing.T) {
	got := runCLI(t, []string{"--help"})
	if !strings.Contains(got.stdout, "export rdp") || strings.Contains(got.stdout, "no export") {
		t.Fatalf("help %q", got.stdout)
	}
}

// readRDP reads an exported file, which must be UTF-16LE with a byte-order
// mark, as the Windows client writes it.
func readRDP(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xFE || len(data)%2 != 0 {
		t.Fatalf("%s is not UTF-16LE with a byte-order mark", path)
	}
	units := make([]uint16, (len(data)-2)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[2+2*i:])
	}
	return string(utf16.Decode(units))
}

func assertPrivateFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode %o, want 600", path, perm)
	}
}
