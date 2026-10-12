package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestImportRemmina_DryRunAndWrite(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))

	dir := filepath.Join("..", "..", "internal", "importer", "testdata")
	got := runCLI(t, []string{"import", "remmina", dir, "--dry-run"})
	if got.code != 0 {
		t.Fatalf("dry-run exit %d stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "imported ") {
		t.Fatalf("stdout %q", got.stdout)
	}
	if !strings.Contains(got.stdout, "old-vnc") {
		t.Fatalf("expected vnc skip in %q", got.stdout)
	}
	if !strings.Contains(got.stdout, "passwords are never imported") {
		t.Fatalf("missing password notice: %q", got.stdout)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Fatal("dry-run must not create config")
	}

	got = runCLI(t, []string{"import", "remmina", dir})
	if got.code != 0 {
		t.Fatalf("import exit %d stderr %q", got.code, got.stderr)
	}
	c, err := config.Open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Profile("work"); !ok {
		t.Fatalf("profiles %+v", c.Profiles())
	}
	if _, ok := c.Profile("old-vnc"); ok {
		t.Fatal("vnc must not be imported")
	}
}

func TestImportRDP_RenameCollision(t *testing.T) {
	cfgPath := writeConnectConfig(t, validTOML())
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))

	rdp := filepath.Join("..", "..", "internal", "importer", "testdata", "work.rdp")
	got := runCLI(t, []string{"import", "rdp", rdp})
	// First import of basename "work" collides with existing profile, and
	// nothing imported is exit 1.
	if got.code != 1 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "name already used") {
		t.Fatalf("expected collision skip: %q", got.stdout)
	}

	got = runCLI(t, []string{"import", "rdp", rdp, "--rename"})
	if got.code != 0 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "work-2") {
		t.Fatalf("expected rename: %q", got.stdout)
	}
	c, err := config.Open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := c.Profile("work-2")
	if !ok {
		t.Fatal("work-2 missing")
	}
	if p.Host != "rdp.example.com:3390" || p.User != "jdoe" || p.Domain != "CORP" {
		t.Fatalf("%+v", p)
	}
}

func TestImport_MissingRemminaDir(t *testing.T) {
	t.Setenv("WICKET_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	got := runCLI(t, []string{"import", "remmina", filepath.Join(t.TempDir(), "nope")})
	if got.code != 2 {
		t.Fatalf("exit %d", got.code)
	}
}

func TestImport_Usage(t *testing.T) {
	got := runCLI(t, []string{"import"})
	if got.code != 2 {
		t.Fatalf("exit %d", got.code)
	}
	got = runCLI(t, []string{"import", "--help"})
	if got.code != 0 || !strings.Contains(got.stdout, "import remmina") {
		t.Fatalf("help: code=%d out=%q", got.code, got.stdout)
	}
}

func TestHelpMentionsImport(t *testing.T) {
	got := runCLI(t, []string{"--help"})
	if got.code != 0 {
		t.Fatal(got.code)
	}
	if !strings.Contains(got.stdout, "import") {
		t.Fatalf("help missing import:\n%s", got.stdout)
	}
}

// After "--" an argument is a path even when it starts with '-'.
func TestImport_DoubleDashEndsFlags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WICKET_CONFIG", filepath.Join(dir, "config.toml"))
	t.Setenv("WICKET_STATE", filepath.Join(dir, "state.toml"))
	t.Chdir(dir)
	if err := os.WriteFile("-odd.rdp", []byte("full address:s:odd.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := runCLI(t, []string{"import", "rdp", "-odd.rdp"}); got.code != 2 {
		t.Fatalf("without --: exit %d", got.code)
	}
	// The file is read, and its name, "-odd", is then refused as a profile name.
	got := runCLI(t, []string{"import", "rdp", "--dry-run", "--", "-odd.rdp"})
	if got.code != 1 || !strings.Contains(got.stdout, "-odd (name: must not start with '-')") {
		t.Fatalf("exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
}

// A file with no user, and one with a UPN user beside a domain, import
// rather than being skipped; the profile with no user then will not connect
// until it has one, and says why.
func TestImportRDP_NoUserAndUPN(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	dir := filepath.Join("..", "..", "internal", "importer", "testdata")
	files := []string{filepath.Join(dir, "nouser.rdp"), filepath.Join(dir, "upn.rdp")}

	got := runCLI(t, append([]string{"import", "rdp", "--dry-run"}, files...))
	if got.code != 0 {
		t.Fatalf("dry-run exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "imported 2: nouser (no user), upn (alice@corp.com)\n") {
		t.Fatalf("stdout %q", got.stdout)
	}

	got = runCLI(t, append([]string{"import", "rdp"}, files...))
	if got.code != 0 {
		t.Fatalf("import exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	got = runCLI(t, []string{"connect", "nouser"})
	if got.code != 2 || !strings.Contains(got.stderr, `profile "nouser" has no user`) {
		t.Fatalf("connect exit %d stderr %q", got.code, got.stderr)
	}
}

// An .rdp file's clipboard and folder settings come through: the dry run
// shows them, and the import writes them, with no password in the config.
func TestImportRDP_ClipboardAndShares(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	file := filepath.Join("..", "..", "internal", "importer", "testdata", "drives.rdp")

	got := runCLI(t, []string{"import", "rdp", "--dry-run", file})
	want := `imported 1: drives (bob; clipboard off; share "/tmp/wicket-rdp-plain"; ` +
		`share "/tmp/wicket-rdp-docs" as docs; share "~/wicket-rdp-proj" as projects; share "/tmp/wicket-rdp-nolabel")` + "\n"
	if got.code != 0 || !strings.Contains(got.stdout, want) {
		t.Fatalf("dry-run exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}

	if got = runCLI(t, []string{"import", "rdp", file}); got.code != 0 {
		t.Fatalf("import exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
	c, err := config.Open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := c.Profile("drives")
	if !ok {
		t.Fatalf("profiles %+v", c.Profiles())
	}
	if p.Clipboard || len(p.Shares) != 4 || p.ShareHome {
		t.Fatalf("%+v", p)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.ToLower(string(data)); strings.Contains(s, "password") || strings.Contains(s, "secret") {
		t.Fatalf("password in config:\n%s", data)
	}
}

// A skipped file's name reaches the summary without its control characters,
// so a crafted file cannot rewrite the terminal.
func TestImportRemmina_SkipEscapesControlCharacters(t *testing.T) {
	t.Setenv("WICKET_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	dir := t.TempDir()
	for file, doc := range map[string]string{
		"a.remmina": "[remmina]\nname=clear\x1b[2J\nprotocol=RDP\nserver=h\nusername=u\n",
		"b.remmina": "[remmina]\nname=vnc\x1b]0;x\x07\nprotocol=VNC\nserver=h\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := runCLI(t, []string{"import", "remmina", "--dry-run", dir})
	if got.code != 1 {
		t.Fatalf("exit %d stdout %q stderr %q", got.code, got.stdout, got.stderr)
	}
	if strings.ContainsAny(got.stdout+got.stderr, "\x1b\x07") {
		t.Fatalf("control character reached the terminal: stdout %q stderr %q", got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "clear?[2J (name: ") || !strings.Contains(got.stdout, "vnc?]0;x? (not RDP (VNC))") {
		t.Fatalf("stdout %q", got.stdout)
	}
}

// Imported profiles take the preferred FreeRDP 3 client: an unsuffixed
// xfreerdp counts only when its --version says 3, exit status aside.
func TestImport_ClientNeedsFreeRDP3(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
	}{
		{"freerdp 2", `echo "This is FreeRDP version 2.11.7 (2.11.7)"`, config.DefaultClient},
		{"freerdp 3", `echo "This is FreeRDP version 3.31.1 (63b948ca5c)"`, "xfreerdp"},
		{"freerdp 3, exit 1", `echo "This is FreeRDP version 3.31.1 (63b948ca5c)"; exit 1`, "xfreerdp"},
		{"no version", `echo "usage: xfreerdp [options]"; exit 1`, config.DefaultClient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.toml")
			t.Setenv("WICKET_CONFIG", cfgPath)
			t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "xfreerdp"), []byte("#!/bin/sh\n"+tc.script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			// Only the stand-in is on PATH, whatever this machine has.
			t.Setenv("PATH", bin)
			file := filepath.Join("..", "..", "internal", "importer", "testdata", "upn.rdp")
			if got := runCLI(t, []string{"import", "rdp", file}); got.code != 0 {
				t.Fatalf("import exit %d stderr %q", got.code, got.stderr)
			}
			c, err := config.Open(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if p, _ := c.Profile("upn"); p.Client != tc.want {
				t.Fatalf("client %q, want %q", p.Client, tc.want)
			}
		})
	}
}
