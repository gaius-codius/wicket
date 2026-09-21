package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
)

// bg is the context tests hand the keyring.
var bg = context.Background()

func TestConnect_UnknownProfile(t *testing.T) {
	cfg := writeConnectConfig(t, validTOML())
	t.Setenv("WICKET_CONFIG", cfg)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	got := runCLI(t, []string{"connect", "nope"})
	if got.code != 2 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "unknown profile") {
		t.Fatalf("stderr %q", got.stderr)
	}
}

func TestConnect_MissingConfig(t *testing.T) {
	t.Setenv("WICKET_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 2 {
		t.Fatalf("exit %d", got.code)
	}
}

func TestConnect_InvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := []byte("not toml {{{")
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WICKET_CONFIG", path)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 2 {
		t.Fatalf("exit %d", got.code)
	}
	gotBytes, _ := os.ReadFile(path)
	if !bytes.Equal(gotBytes, orig) {
		t.Fatal("config mutated")
	}
}

func TestConnect_NonTTYNoSecret(t *testing.T) {
	cfg := writeConnectConfig(t, validTOML())
	t.Setenv("WICKET_CONFIG", cfg)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)
	oldStore := openStore
	openStore = func() secret.Store { return secret.NewMemory() }
	oldTTY := isTerminal
	isTerminal = func(int) bool { return false }
	t.Cleanup(func() {
		openStore = oldStore
		isTerminal = oldTTY
	})
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 2 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "TUI") {
		t.Fatalf("stderr %q", got.stderr)
	}
}

func TestConnect_StoredSecret(t *testing.T) {
	cfgPath := writeConnectConfig(t, validTOML())
	statePath := filepath.Join(t.TempDir(), "state.toml")
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", statePath)
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)
	t.Setenv("FAKERDP_EXIT", "4")

	cfg, err := config.Open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cfg.Profile("work")
	mem := secret.NewMemory()
	pw, _ := secret.NewPassword("s3cret")
	if err := mem.Upsert(bg, secret.IdentityFor(cfg.Path(), p), pw); err != nil {
		t.Fatal(err)
	}
	oldStore := openStore
	openStore = func() secret.Store { return mem }
	t.Cleanup(func() { openStore = oldStore })

	got := runCLI(t, []string{"connect", "work"})
	if got.code != 4 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	r := testutil.ReadRecord(t, rec)
	if r.Stdin != "s3cret\n" {
		t.Fatalf("stdin %q", r.Stdin)
	}
	joined := strings.Join(r.Argv, " ")
	if strings.Contains(joined, "s3cret") {
		t.Fatal("password on argv")
	}
	st, err := config.OpenState(statePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.LastUsed("work"); !ok {
		t.Fatal("last_used not written")
	}
}

func TestConnect_TTYEmptyPasswordRejected(t *testing.T) {
	cfg := writeConnectConfig(t, validTOML())
	t.Setenv("WICKET_CONFIG", cfg)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)
	oldStore, oldTTY, oldRead := openStore, isTerminal, readPassword
	openStore = func() secret.Store { return secret.NewMemory() }
	isTerminal = func(int) bool { return true }
	readPassword = func(int) ([]byte, error) { return []byte{}, nil }
	t.Cleanup(func() {
		openStore = oldStore
		isTerminal = oldTTY
		readPassword = oldRead
	})
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 2 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "password required") {
		t.Fatalf("stderr %q", got.stderr)
	}
}

func TestConnect_MissingClient(t *testing.T) {
	cfg := writeConnectConfig(t, validTOML())
	t.Setenv("WICKET_CONFIG", cfg)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	t.Setenv("PATH", t.TempDir())
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 2 {
		t.Fatalf("exit %d", got.code)
	}
	if !strings.Contains(got.stderr, "sdl-freerdp3") {
		t.Fatalf("stderr %q", got.stderr)
	}
}

func TestConnect_LoadErrorDuplicate(t *testing.T) {
	path := writeConnectConfig(t, `
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
	t.Setenv("WICKET_CONFIG", path)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 2 {
		t.Fatalf("exit %d", got.code)
	}
	var le *config.LoadError
	if !errors.As(errors.New(strings.TrimSpace(got.stderr)), &le) {
		if !strings.Contains(got.stderr, "duplicate") {
			t.Fatalf("stderr %q", got.stderr)
		}
	}
}

func validTOML() string {
	return `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
client = "sdl-freerdp3"
`
}

func writeConnectConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
