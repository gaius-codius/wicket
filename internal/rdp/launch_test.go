package rdp

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestLookPath_IllegalBasename(t *testing.T) {
	t.Parallel()
	l := &Launcher{Runner: OSRunner{}, Stdout: os.Stdout, Stderr: os.Stderr}
	_, err := l.Start(Plan{Client: "/tmp/x", Args: []string{"/v:h"}}, nil)
	if !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("err = %v", err)
	}
	_, err = l.Start(Plan{Client: "sdl freerdp", Args: []string{"/v:h"}}, nil)
	if !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestLookPath_Missing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	l := &Launcher{Stdout: os.Stdout, Stderr: os.Stderr}
	_, err := l.Start(Plan{Client: "sdl-freerdp3", Args: []string{"/v:h"}}, nil)
	if !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestClassify_Boundaries(t *testing.T) {
	t.Parallel()
	if Classify(Outcome{StartErr: errors.New("x")}) != ClassStartError {
		t.Fatal("start")
	}
	if Classify(Outcome{Duration: 2999 * time.Millisecond}) != ClassShortSession {
		t.Fatal("2.999s")
	}
	if Classify(Outcome{Duration: 3 * time.Second}) != ClassEnded {
		t.Fatal("3.0s")
	}
	if Classify(Outcome{Duration: 3001 * time.Millisecond}) != ClassEnded {
		t.Fatal("3.001s")
	}
}

func TestStart_WaitExitCodeAndStdin(t *testing.T) {
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)
	t.Setenv("FAKERDP_EXIT", "7")
	p := config.Profile{Name: "n", Host: "h", User: "u", Client: "sdl-freerdp3", Scale: 100, DynamicResolution: true}
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	l := &Launcher{Stdout: os.Stdout, Stderr: os.Stderr}
	sess, err := l.Start(plan, lineCred("secret"))
	if err != nil {
		t.Fatal(err)
	}
	o := sess.Wait()
	if o.StartErr != nil {
		t.Fatal(o.StartErr)
	}
	if o.ExitCode != 7 {
		t.Fatalf("exit %d", o.ExitCode)
	}
	if Classify(o) != ClassShortSession {
		t.Fatalf("class %v", Classify(o))
	}
	r := testutil.ReadRecord(t, rec)
	if r.Stdin != "secret\n" {
		t.Fatalf("stdin %q", r.Stdin)
	}
	joined := strings.Join(r.Argv, "\x00")
	if strings.Contains(joined, "secret") {
		t.Fatal("password on argv")
	}
	for k, v := range r.Environ {
		if strings.Contains(k, "secret") || strings.Contains(v, "secret") {
			t.Fatalf("password in environ %s=%s", k, v)
		}
	}
}

type lineCred string

func (l lineCred) WriteLine(w io.Writer) error {
	_, err := io.WriteString(w, string(l)+"\n")
	return err
}
