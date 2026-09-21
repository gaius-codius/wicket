package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
	"github.com/gaius-codius/wicket/internal/testutil/fakesecret"
)

var (
	wicketOnce sync.Once
	wicketBin  string
	wicketErr  error
)

// buildWicket builds the wicket binary once per test run, for tests that
// must signal a real Wicket process rather than the test binary.
func buildWicket(t *testing.T) string {
	t.Helper()
	wicketOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wicket-bin-")
		if err != nil {
			wicketErr = err
			return
		}
		wicketBin = filepath.Join(dir, "wicket")
		out, err := exec.Command("go", "build", "-o", wicketBin, "github.com/gaius-codius/wicket/cmd/wicket").CombinedOutput()
		if err != nil {
			wicketErr = errors.New(string(out))
		}
	})
	if wicketErr != nil {
		t.Fatal(wicketErr)
	}
	return wicketBin
}

func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

// alive reports whether pid is running rather than gone or a zombie.
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] != 'Z'
}

// wicket connect is what launchers and keybindings run, and they end it with
// SIGTERM, or a closed terminal ends it with SIGHUP. FreeRDP runs in a
// process group of its own, so it never hears of either: Wicket used to exit
// and leave the client and its helpers running. Now it stops the group --
// SIGTERM, then SIGKILL for a client that ignores it -- and exits as a
// program killed by the signal does. This runs the real binary, with and
// without a terminal.
func TestConnect_SignalStopsTheClientGroup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sig      syscall.Signal
		tty      bool
		stubborn bool
	}{
		{"SIGTERM-no-tty", syscall.SIGTERM, false, false},
		{"SIGHUP-tty-stubborn", syscall.SIGHUP, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildWicket(t)
			_, _, cleanup := fakesecret.Start(t)
			defer cleanup()
			cfgPath := writeConnectConfig(t, validTOML())
			t.Setenv("WICKET_CONFIG", cfgPath)
			t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
			testutil.PrependPATH(t, testutil.FakeRDPDir(t))
			dir := t.TempDir()
			log := filepath.Join(dir, "signals")
			ready := filepath.Join(dir, "ready")
			helperPID := filepath.Join(dir, "helper.pid")
			t.Setenv("FAKERDP_TRAP", log)
			t.Setenv("FAKERDP_TRAP_READY", ready)
			t.Setenv("FAKERDP_SPAWN", "1")
			t.Setenv("FAKERDP_HELPER_PID", helperPID)
			if tc.stubborn {
				t.Setenv("FAKERDP_STUBBORN", "1")
			}

			cfg, err := config.Open(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := cfg.Profile("work")
			pw, _ := secret.NewPassword("s3cret")
			if err := secret.NewDBus().Upsert(bg, secret.IdentityFor(cfg.Path(), p), pw); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(bin, "connect", "work")
			if tc.tty {
				master, slave, err := pty.Open()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
				cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			t.Cleanup(func() { _ = cmd.Process.Kill() })

			waitForFile(t, ready)
			helper, _ := strconv.Atoi(waitForFile(t, helperPID))
			if err := cmd.Process.Signal(tc.sig); err != nil {
				t.Fatal(err)
			}
			select {
			case <-exited:
			case <-time.After(15 * time.Second):
				t.Fatalf("wicket connect still running after %v", tc.sig)
			}
			if got, want := cmd.ProcessState.ExitCode(), 128+int(tc.sig); got != want {
				t.Fatalf("exit status %d, want %d", got, want)
			}
			if b, _ := os.ReadFile(log); !strings.Contains(string(b), "client:terminated") {
				t.Fatalf("client was not asked to terminate; signal log %q", b)
			}
			deadline := time.Now().Add(5 * time.Second)
			for alive(helper) {
				if time.Now().After(deadline) {
					_ = syscall.Kill(helper, syscall.SIGKILL)
					t.Fatalf("helper %d outlived wicket connect", helper)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
