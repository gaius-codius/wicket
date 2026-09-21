package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Sandbox runs a package's tests with config, state and the Secret Service
// pointed somewhere harmless, and returns the exit code for TestMain.
//
// It is the package-wide floor for hermeticity: a test that forgets to set its
// own paths reads a config that does not exist and a bus that does not answer,
// instead of the developer's real ~/.config/wicket or their live keyring.
// Tests that need either still override with t.Setenv.
//
// Usage:
//
//	func TestMain(m *testing.M) { os.Exit(testutil.Sandbox(m)) }
func Sandbox(m *testing.M) int {
	dir, err := os.MkdirTemp("", "wicket-sandbox-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	for k, v := range map[string]string{
		"WICKET_CONFIG":   filepath.Join(dir, "missing.toml"),
		"WICKET_STATE":    filepath.Join(dir, "state.toml"),
		"XDG_CONFIG_HOME": filepath.Join(dir, "config"),
		"XDG_STATE_HOME":  filepath.Join(dir, "state"),
		// A path that cannot be a listening socket, so an accidental
		// NewDBus() fails to connect rather than reaching the real keyring.
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=" + filepath.Join(dir, "no-such-bus"),
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(fmt.Sprintf("sandbox %s: %v", k, err))
		}
	}
	// A theme picked in the developer's shell would change what the TUI
	// tests render.
	if err := os.Unsetenv("WICKET_THEME"); err != nil {
		panic(fmt.Sprintf("sandbox WICKET_THEME: %v", err))
	}
	// Every fake client inherits this, which is how the check below tells
	// this package's clients from any other test binary's.
	tag := filepath.Join(dir, "fakerdp")
	if err := os.Setenv("FAKERDP_SANDBOX", tag); err != nil {
		panic(fmt.Sprintf("sandbox FAKERDP_SANDBOX: %v", err))
	}
	code := m.Run()
	if leaked := StopLeakedClients(tag, 2*time.Second); len(leaked) > 0 {
		fmt.Fprintln(os.Stderr, "FAIL: tests left fake clients running (now killed):")
		for _, l := range leaked {
			fmt.Fprintln(os.Stderr, "  "+l)
		}
		if code == 0 {
			code = 1
		}
	}
	return code
}

// StopLeakedClients finds every fake client or helper started under the
// sandbox tag that is still running once grace has passed, kills it and its
// process group, and describes each. A test that starts a client must stop
// it; ones that did not used to leave clients running for hours after go test
// had finished. Processes are found through /proc, by the FAKERDP_SANDBOX
// they inherited, so nothing depends on the client registering itself before
// the check runs. Elsewhere than Linux it finds nothing.
func StopLeakedClients(tag string, grace time.Duration) []string {
	deadline := time.Now().Add(grace)
	pids := fakeClients(tag)
	for len(pids) > 0 && time.Now().Before(deadline) {
		// A client stopping as the tests finish gets a moment to go.
		time.Sleep(20 * time.Millisecond)
		pids = fakeClients(tag)
	}
	var leaked []string
	for _, pid := range pids {
		leaked = append(leaked, fmt.Sprintf("pid %d: %s", pid, fakeEnv(pid)))
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 1 && pgid != syscall.Getpgrp() {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return leaked
}

// fakeClients lists the running, non-zombie processes that carry tag.
func fakeClients(tag string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	want := "FAKERDP_SANDBOX=" + tag
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		env, err := os.ReadFile("/proc/" + e.Name() + "/environ")
		if err != nil || !slices.Contains(strings.Split(string(env), "\x00"), want) {
			continue
		}
		if !running(pid) {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// fakeEnv is the FAKERDP_ settings pid was started with, which say which
// test started it.
func fakeEnv(pid int) string {
	env, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	var out []string
	for _, kv := range strings.Split(string(env), "\x00") {
		if strings.HasPrefix(kv, "FAKERDP_") && !strings.HasPrefix(kv, "FAKERDP_SANDBOX=") {
			out = append(out, kv)
		}
	}
	return strings.Join(out, " ")
}

// running reports whether pid is alive rather than gone or a zombie.
func running(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] != 'Z'
}
