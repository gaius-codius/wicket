package exporter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(run(m)) }

// run points HOME at a folder of its own as well: the share checks stat the
// home folder and every folder above it, and ~ in a share expands to it, so
// no test depends on the real one.
func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "wicket-exporter-")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	home := filepath.Join(dir, "home", "alice")
	if err := os.MkdirAll(home, 0o700); err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	return testutil.Sandbox(m)
}
