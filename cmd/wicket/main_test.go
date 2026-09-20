package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/testutil"
)

type runResult struct {
	code     int
	stdout   string
	stderr   string
	tuiCalls int
}

// TestMain points config, state and the bus somewhere harmless so no test in
// this package can read the user's real config or keyring, or spawn FreeRDP.
// Tests that need a config still override with t.Setenv.
func TestMain(m *testing.M) { os.Exit(testutil.Sandbox(m)) }

func runCLI(t *testing.T, args []string) runResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	code := run(args, &stdout, &stderr, func() error {
		tuiCalls++
		return nil
	})
	return runResult{
		code:     code,
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		tuiCalls: tuiCalls,
	}
}

func TestNoArgsStartsTUI(t *testing.T) {
	t.Parallel()
	got := runCLI(t, nil)
	if got.code != 0 {
		t.Fatalf("exit = %d stderr=%q", got.code, got.stderr)
	}
	if got.tuiCalls != 1 {
		t.Fatalf("TUI calls = %d, want 1", got.tuiCalls)
	}
}

func TestHelpMentionsTUIAndConnect(t *testing.T) {
	t.Parallel()
	// "help" is the word a user reaches for first, and it used to be an
	// unknown command.
	for _, flag := range []string{"--help", "-h", "help"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			got := runCLI(t, []string{flag})
			if got.code != 0 {
				t.Fatalf("exit = %d, want 0; stderr=%q", got.code, got.stderr)
			}
			if got.tuiCalls != 0 {
				t.Fatalf("TUI started on %s", flag)
			}
			out := got.stdout
			if !strings.Contains(out, "TUI") {
				t.Fatalf("help does not mention TUI:\n%s", out)
			}
			if !strings.Contains(out, "connect") {
				t.Fatalf("help does not mention connect:\n%s", out)
			}
		})
	}
}

func TestUnknownSubcommandExit2(t *testing.T) {
	t.Parallel()
	got := runCLI(t, []string{"frobnicate"})
	if got.code != 2 {
		t.Fatalf("exit = %d, want 2", got.code)
	}
	if got.tuiCalls != 0 {
		t.Fatal("TUI started for unknown command")
	}
	if got.stdout != "" {
		t.Fatalf("stdout = %q, want empty", got.stdout)
	}
	if !strings.Contains(got.stderr, "unknown command") {
		t.Fatalf("stderr = %q, want unknown command", got.stderr)
	}
}

func TestUnknownFlagExit2(t *testing.T) {
	t.Parallel()
	got := runCLI(t, []string{"--nope"})
	if got.code != 2 {
		t.Fatalf("exit = %d, want 2", got.code)
	}
	if got.stdout != "" {
		t.Fatalf("stdout = %q, want empty", got.stdout)
	}
	if !strings.Contains(got.stderr, "unknown flag") {
		t.Fatalf("stderr = %q, want unknown flag", got.stderr)
	}
}

func TestConnectMissingNameExit2(t *testing.T) {
	t.Parallel()
	got := runCLI(t, []string{"connect"})
	if got.code != 2 {
		t.Fatalf("exit = %d, want 2", got.code)
	}
	if got.tuiCalls != 0 {
		t.Fatal("TUI started for connect without name")
	}
	if got.stdout != "" {
		t.Fatalf("stdout = %q, want empty", got.stdout)
	}
	if !strings.Contains(got.stderr, "usage: wicket connect") {
		t.Fatalf("stderr = %q, want usage", got.stderr)
	}
}

func TestConnectNamedProfileExit2NoSpawn(t *testing.T) {
	t.Setenv("WICKET_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 2 {
		t.Fatalf("exit = %d, want 2", got.code)
	}
	if got.tuiCalls != 0 {
		t.Fatal("TUI started for connect")
	}
	if got.stdout != "" {
		t.Fatalf("stdout = %q, want empty", got.stdout)
	}
	if got.stderr == "" {
		t.Fatal("stderr empty; want an error")
	}
}

func TestGoModuleFloor(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(moduleRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "module github.com/gaius-codius/wicket") {
		t.Fatal("go.mod missing module github.com/gaius-codius/wicket")
	}
	found := false
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "go ") {
			continue
		}
		found = true
		ver := strings.TrimSpace(strings.TrimPrefix(line, "go "))
		if ver < "1.24" {
			t.Fatalf("Go version %q; want 1.24+", ver)
		}
	}
	if !found {
		t.Fatal("go.mod missing go directive")
	}
}

func TestNoLicenseFile(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	_, err := os.Stat(filepath.Join(root, "LICENSE"))
	if err == nil {
		t.Fatal("LICENSE file must not exist (BIZ-006)")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("stat LICENSE: %v", err)
	}
}
