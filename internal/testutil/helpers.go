package testutil

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// FakeRecord is what the fake client was given; see fakerdp's record.
type FakeRecord struct {
	// RawArgv is the command line as the process table shows it.
	RawArgv []string `json:"raw_argv"`
	// Argv is the arguments as FreeRDP reads them, from the command line or
	// fd 3, less the password.
	Argv []string `json:"argv"`
	// Password is the /p: argument read from fd 3; HasPassword says there
	// was one.
	Password    string            `json:"password"`
	HasPassword bool              `json:"has_password"`
	Environ     map[string]string `json:"environ"`
	Stdin       string            `json:"stdin"`
	StdinTTY    bool              `json:"stdin_tty"`
	Foreground  bool              `json:"foreground"`
	// CertAnswer is the answer the client read when it asked about a
	// certificate (FAKERDP_CERT), "EOF" for none, or "" when it trusted the
	// certificate without asking.
	CertAnswer string `json:"cert_answer"`
}

var (
	fakeOnce sync.Once
	fakeDir  string
	errFake  error
)

// FakeRDPDir returns a directory containing a `sdl-freerdp3` (and `xfreerdp3`)
// recorder on PATH, and the same recorder as `myrdp`, a client that is not
// FreeRDP's.
func FakeRDPDir(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wicket-fakerdp-") //nolint:usetesting // built once and shared by every test in the run; a per-test t.TempDir would be removed after the first
		if err != nil {
			errFake = err
			return
		}
		fakeDir = dir
		bin := filepath.Join(dir, "fakerdp")
		cmd := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "github.com/gaius-codius/wicket/internal/testutil/fakerdp")
		cmd.Dir = moduleRoot(t)
		out, err := cmd.CombinedOutput()
		if err != nil {
			errFake = err
			t.Logf("go build fakerdp: %s", out)
			return
		}
		for _, name := range []string{"sdl-freerdp3", "xfreerdp3", "wlfreerdp3", "myrdp"} {
			link := filepath.Join(dir, name)
			if err := os.Symlink(bin, link); err != nil {
				errFake = err
				return
			}
		}
	})
	if errFake != nil {
		t.Fatal(errFake)
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
