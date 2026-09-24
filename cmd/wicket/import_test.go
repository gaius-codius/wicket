package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
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
