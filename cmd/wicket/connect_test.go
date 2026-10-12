package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestConnect_EmptyProfileID(t *testing.T) {
	cfg := writeConnectConfig(t, validTOML())
	t.Setenv("WICKET_CONFIG", cfg)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)
	oldLookup := lookupProfile
	lookupProfile = func(c *config.Config, name string) (config.Profile, bool) {
		p, ok := c.Profile(name)
		p.ID = ""
		return p, ok
	}
	t.Cleanup(func() { lookupProfile = oldLookup })
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 2 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "no id after load") {
		t.Fatalf("stderr %q", got.stderr)
	}
	if !strings.Contains(got.stderr, "corrupt") {
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

// failingStore answers every Lookup with err and counts the saves it is asked
// for, so a test can tell whether connect offered to save a typed password.
type failingStore struct {
	err     error
	upserts int
}

func (f *failingStore) Lookup(context.Context, secret.Identity) (secret.LookupResult, error) {
	return secret.LookupResult{}, f.err
}

func (f *failingStore) Upsert(context.Context, secret.Identity, secret.Password) error {
	f.upserts++
	return nil
}

func (f *failingStore) Delete(context.Context, secret.Identity) error { return nil }

func (f *failingStore) Presence(context.Context, secret.Identity) (secret.Presence, error) {
	return secret.NotSaved, f.err
}

func TestConnect_KeyringErrors(t *testing.T) {
	unavailable := fmt.Errorf("%w: no session bus", secret.ErrUnavailable)
	cases := []struct {
		name    string
		err     error
		tty     bool
		code    int
		want    []string
		notWant []string
		upserts int
	}{
		{"not found/no tty", secret.ErrNotFound, false, 2,
			[]string{"no stored password", "TUI"}, nil, 0},
		{"unavailable/no tty", unavailable, false, 2,
			[]string{"keyring", "no session bus"}, []string{"no stored password", "TUI"}, 0},
		{"not found/tty", secret.ErrNotFound, true, 0,
			[]string{"Save password"}, []string{"keyring"}, 1},
		{"unavailable/tty", unavailable, true, 0,
			[]string{"cannot reach the keyring", "no session bus"}, []string{"Save password"}, 0},
		// A saved password that cannot be used was read fine, so it takes
		// neither path above: no prompt, no keyring warning, no client.
		{"too long/no tty", secret.ErrTooLong, false, 2,
			[]string{"saved password cannot be used", "511 bytes"}, []string{"keyring", "no stored password"}, 0},
		{"too long/tty", secret.ErrTooLong, true, 2,
			[]string{"saved password cannot be used", "511 bytes"}, []string{"keyring", "Password:", "Save password"}, 0},
		{"line break/tty", secret.ErrCRLF, true, 2,
			[]string{"saved password cannot be used", "CR or LF"}, []string{"keyring", "Password:", "Save password"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WICKET_CONFIG", writeConnectConfig(t, validTOML()))
			t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
			testutil.PrependPATH(t, testutil.FakeRDPDir(t))
			rec := filepath.Join(t.TempDir(), "rec.json")
			t.Setenv("FAKERDP_RECORD", rec)

			// Stdin already holds a "y", so a save offer that should not
			// have been made is answered and shows up as an upsert.
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Close() })
			if _, err := w.Write([]byte("y\n")); err != nil {
				t.Fatal(err)
			}
			_ = w.Close()

			store := &failingStore{err: tc.err}
			oldStore, oldTTY, oldRead, oldIn := openStore, isTerminal, readPassword, stdinFile
			openStore = func() secret.Store { return store }
			isTerminal = func(int) bool { return tc.tty }
			readPassword = func(int) ([]byte, error) { return []byte("typed"), nil }
			stdinFile = func() *os.File { return r }
			t.Cleanup(func() {
				openStore, isTerminal, readPassword, stdinFile = oldStore, oldTTY, oldRead, oldIn
			})

			got := runCLI(t, []string{"connect", "work"})
			if got.code != tc.code {
				t.Fatalf("exit %d stderr %q", got.code, got.stderr)
			}
			for _, s := range tc.want {
				if !strings.Contains(got.stderr, s) {
					t.Errorf("stderr %q lacks %q", got.stderr, s)
				}
			}
			for _, s := range tc.notWant {
				if strings.Contains(got.stderr, s) {
					t.Errorf("stderr %q has %q", got.stderr, s)
				}
			}
			if store.upserts != tc.upserts {
				t.Errorf("upserts %d, want %d", store.upserts, tc.upserts)
			}
			if tc.code != 0 {
				if _, err := os.Stat(rec); !os.IsNotExist(err) {
					t.Errorf("client started: %v", err)
				}
			} else if tc.tty {
				if rr := testutil.ReadRecord(t, rec); rr.Stdin != "typed\n" {
					t.Errorf("client stdin %q, want the typed password", rr.Stdin)
				}
			}
		})
	}
}

// The sandbox points DBUS_SESSION_BUS_ADDRESS at a socket that does not
// exist, so the real Secret Service store fails the way it does on a machine
// with no session bus. This checks that the real store's error, not only the
// failingStore's, reaches the user as a keyring problem.
func TestConnect_NoSessionBus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Secret Service store is the default only on Linux")
	}
	t.Setenv("WICKET_CONFIG", writeConnectConfig(t, validTOML()))
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	oldTTY := isTerminal
	isTerminal = func(int) bool { return false }
	t.Cleanup(func() { isTerminal = oldTTY })

	got := runCLI(t, []string{"connect", "work"})
	if got.code != 2 {
		t.Fatalf("exit %d stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "keyring") || strings.Contains(got.stderr, "TUI") {
		t.Fatalf("stderr %q", got.stderr)
	}
}
