package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
	"github.com/gaius-codius/wicket/internal/testutil/fakesecret"
)

const sweepSentinel = "s3cret-SENTINEL"

func TestCLISecretSweep_TTY(t *testing.T) {
	cfgPath := writeConnectConfig(t, validTOML())
	t.Setenv("WICKET_CONFIG", cfgPath)
	statePath := filepath.Join(t.TempDir(), "state.toml")
	t.Setenv("WICKET_STATE", statePath)
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)

	tmp := t.TempDir()
	rec := filepath.Join(tmp, "rec.json")
	pidPath := filepath.Join(tmp, "pid")
	hold := filepath.Join(tmp, "go")
	t.Setenv("FAKERDP_RECORD", rec)
	t.Setenv("FAKERDP_PID", pidPath)
	t.Setenv("FAKERDP_HOLD_FILE", hold)
	t.Setenv("FAKERDP_EXIT", "0")

	mem := secret.NewMemory()
	type snap struct {
		cmd, env []byte
		err      error
	}
	seen := make(chan snap, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		var pid string
		for time.Now().Before(deadline) {
			b, err := os.ReadFile(pidPath)
			if err == nil && len(b) > 0 {
				pid = strings.TrimSpace(string(b))
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if pid == "" {
			close(seen)
			return
		}
		// These reads must be checked. Discarding the error would hand
		// assertNoSweep an empty slice, and the sweep would pass without ever
		// having looked at the running client.
		cmd, err := os.ReadFile("/proc/" + pid + "/cmdline")
		if err != nil {
			seen <- snap{err: err}
			_ = os.WriteFile(hold, []byte("x"), 0o600)
			return
		}
		env, err := os.ReadFile("/proc/" + pid + "/environ")
		if err != nil {
			seen <- snap{err: err}
			_ = os.WriteFile(hold, []byte("x"), 0o600)
			return
		}
		seen <- snap{cmd: cmd, env: env}
		_ = os.WriteFile(hold, []byte("x"), 0o600)
	}()

	var stdout, stderr bytes.Buffer
	code := runPTYConnectCapture(t, mem, sweepSentinel+"\n", "N\n", &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr.String())
	}

	select {
	case s, ok := <-seen:
		if !ok {
			t.Fatal("child pid never appeared")
		}
		if s.err != nil {
			t.Fatalf("read /proc for the running client: %v", s.err)
		}
		if len(s.cmd) == 0 || len(s.env) == 0 {
			t.Fatalf("empty /proc snapshot (cmdline %d bytes, environ %d bytes); the sweep would pass vacuously", len(s.cmd), len(s.env))
		}
		assertNoSweep(t, s.cmd, "cmdline")
		assertNoSweep(t, s.env, "environ")
	case <-time.After(6 * time.Second):
		t.Fatal("timeout waiting for /proc snapshot")
	}
	assertNoSweep(t, stdout.Bytes(), "stdout")
	assertNoSweep(t, stderr.Bytes(), "stderr")
	cfgb, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	assertNoSweep(t, cfgb, "config.toml")
	stb, err := os.ReadFile(statePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read state: %v", err)
	}
	assertNoSweep(t, stb, "state.toml")
	got := testutil.ReadRecord(t, rec)
	for _, a := range got.Argv {
		assertNoSweep(t, []byte(a), "argv")
	}
	for k, v := range got.Environ {
		assertNoSweep(t, []byte(k+"="+v), "environ-record")
	}
	if got.Stdin != sweepSentinel+"\n" {
		t.Fatalf("stdin %q", got.Stdin)
	}
}

func runPTYConnectCapture(t *testing.T, mem *secret.Memory, password, yn string, stdout, stderr *bytes.Buffer) int {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	oldStore, oldIn, oldTTY := openStore, stdinFile, isTerminal
	openStore = func() secret.Store { return mem }
	stdinFile = func() *os.File { return slave }
	isTerminal = func(int) bool { return true }
	t.Cleanup(func() {
		openStore = oldStore
		stdinFile = oldIn
		isTerminal = oldTTY
		_ = master.Close()
		_ = slave.Close()
	})
	go func() {
		time.Sleep(80 * time.Millisecond)
		_, _ = master.Write([]byte(password))
		time.Sleep(80 * time.Millisecond)
		_, _ = master.Write([]byte(yn))
	}()
	return run([]string{"connect", "work"}, stdout, stderr, func() error { return nil })
}

func TestAC017_IsolatedSecretService(t *testing.T) {
	userBus := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	addr, _, cleanup := fakesecret.Start(t)
	defer cleanup()
	if addr == "" {
		t.Fatal("empty bus")
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == userBus && userBus != "" {
		t.Fatal("must not use the user session bus")
	}
	store := secret.NewDBus()
	p := config.Profile{Name: "work", Host: "h", User: "u"}
	pw, err := secret.NewPassword(sweepSentinel)
	if err != nil {
		t.Fatal(err)
	}
	idA := secret.IdentityFor("/tmp/a.toml", p)
	idB := secret.IdentityFor("/tmp/b.toml", p)
	if err := store.Upsert(bg, idA, pw); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(bg, idB); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("shared item: %v", err)
	}
}

func assertNoSweep(t *testing.T, b []byte, label string) {
	t.Helper()
	if strings.Contains(string(b), sweepSentinel) {
		t.Fatalf("sentinel leaked in %s", label)
	}
}
