package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestConnect_PTY_SaveChoices(t *testing.T) {
	cfgPath := writeConnectConfig(t, validTOML())
	t.Setenv("WICKET_CONFIG", cfgPath)
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)

	cfg, err := config.Open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cfg.Profile("work")
	id := secret.IdentityFor(cfg.Path(), p)

	t.Run("N", func(t *testing.T) {
		mem := secret.NewMemory()
		code := runPTYConnect(t, mem, "pw-once\n", "N\n")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if _, err := mem.Lookup(bg, id); !errors.Is(err, secret.ErrNotFound) {
			t.Fatalf("stored on N: %v", err)
		}
	})
	t.Run("y", func(t *testing.T) {
		mem := secret.NewMemory()
		code := runPTYConnect(t, mem, "pw-save\n", "y\n")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if _, err := mem.Lookup(bg, id); err != nil {
			t.Fatalf("not stored on y: %v", err)
		}
	})
}

func runPTYConnect(t *testing.T, mem *secret.Memory, password, yn string) int {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })

	oldStore, oldIn, oldTTY := openStore, stdinFile, isTerminal
	openStore = func() secret.Store { return mem }
	stdinFile = func() *os.File { return slave }
	isTerminal = func(int) bool { return true }
	t.Cleanup(func() {
		openStore = oldStore
		stdinFile = oldIn
		isTerminal = oldTTY
	})

	go func() {
		time.Sleep(80 * time.Millisecond)
		_, _ = master.Write([]byte(password))
		time.Sleep(80 * time.Millisecond)
		_, _ = master.Write([]byte(yn))
	}()
	got := runCLI(t, []string{"connect", "work"})
	return got.code
}
