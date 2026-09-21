package testutil

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

type FakeRecord struct {
	Argv    []string          `json:"argv"`
	Environ map[string]string `json:"environ"`
	Stdin   string            `json:"stdin"`
}

var (
	fakeOnce sync.Once
	fakeDir  string
	fakeErr  error
)

// FakeRDPDir returns a directory containing a `sdl-freerdp3` (and `xfreerdp3`)
// recorder on PATH, and the same recorder as `myrdp`, a client that is not
// FreeRDP's.
func FakeRDPDir(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wicket-fakerdp-")
		if err != nil {
			fakeErr = err
			return
		}
		fakeDir = dir
		bin := filepath.Join(dir, "fakerdp")
		cmd := exec.Command("go", "build", "-o", bin, "github.com/gaius-codius/wicket/internal/testutil/fakerdp")
		cmd.Dir = moduleRoot(t)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fakeErr = err
			t.Logf("go build fakerdp: %s", out)
			return
		}
		for _, name := range []string{"sdl-freerdp3", "xfreerdp3", "wlfreerdp3", "myrdp"} {
			link := filepath.Join(dir, name)
			if err := os.Symlink(bin, link); err != nil {
				fakeErr = err
				return
			}
		}
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	return fakeDir
}

func PrependPATH(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func ReadRecord(t *testing.T, path string) FakeRecord {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r FakeRecord
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
