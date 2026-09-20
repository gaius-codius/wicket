package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
	return m.Run()
}
