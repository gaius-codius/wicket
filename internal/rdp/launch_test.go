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
	// A failure is a failure however long it took: FreeRDP gives up on an
	// unreachable host after about fifteen seconds.
	for _, o := range []Outcome{
		{ExitCode: 131, Duration: 16 * time.Second},
		{ExitCode: 1, Duration: time.Hour},
		{ExitCode: 131, Duration: time.Second},
		{ExitCode: 128 + 9, Signaled: true, Duration: time.Minute},
	} {
		if got := Classify(o); got != ClassFailed {
			t.Fatalf("%+v classed %v, want a failure", o, got)
		}
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
	if Classify(o) != ClassFailed {
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

// FreeRDP's own clients say with their exit code how a session ended, and
// most endings that exit non-zero are not failures: the SDL client exits 1
// when its window is closed, and 2 when the user logs off. Only the failures
// among them say the password may be wrong.
func TestClassify_FreeRDPExitCodes(t *testing.T) {
	t.Parallel()
	long := time.Minute
	for _, tc := range []struct {
		client      string
		code        int
		dur         time.Duration
		class       Class
		reason      string
		credentials bool
	}{
		{"sdl-freerdp3", 0, long, ClassEnded, "", false},
		{"sdl-freerdp3", 1, long, ClassEnded, "disconnected", false},
		{"sdl-freerdp3", 1, time.Second, ClassShortSession, "disconnected", false},
		{"sdl-freerdp3", 2, long, ClassEnded, "logged off", false},
		{"sdl-freerdp3", 2, time.Second, ClassEnded, "logged off", false},
		{"xfreerdp3", 3, long, ClassEnded, "idle timeout", false},
		{"xfreerdp3", 5, long, ClassEnded, "another session took over", false},
		{"sdl-freerdp3", 11, long, ClassEnded, "disconnected by the user", false},
		{"xfreerdp3", 12, long, ClassEnded, "logged off", false},
		{"sdl-freerdp", 131, 16 * time.Second, ClassFailed, "connection failed", false},
		{"sdl-freerdp3", 132, time.Second, ClassFailed, "authentication failed", true},
		{"sdl-freerdp3", 134, time.Second, ClassFailed, "logon failed", true},
		{"sdl-freerdp3", 135, time.Second, ClassFailed, "account locked out", false},
		{"xfreerdp", 141, 15 * time.Second, ClassFailed, "could not connect", false},
		{"sdl-freerdp3", 148, time.Second, ClassFailed, "password expired", false},
		{"sdl-freerdp3", 154, time.Second, ClassFailed, "wrong password", true},
		// A code FreeRDP does not document says nothing either way.
		{"sdl-freerdp3", 99, long, ClassFailed, "", true},
		{"sdl-freerdp3", 255, long, ClassFailed, "", true},
		// Another client's codes mean something else: every non-zero
		// status is a failure, and none of them has a reason.
		{"myrdp", 2, long, ClassFailed, "", true},
		{"myrdp", 132, long, ClassFailed, "", true},
		{"myrdp", 0, long, ClassEnded, "", true},
	} {
		o := Outcome{Client: tc.client, ExitCode: tc.code, Duration: tc.dur}
		if got := Classify(o); got != tc.class {
			t.Errorf("%s %d after %v: class %v, want %v", tc.client, tc.code, tc.dur, got, tc.class)
		}
		if got := o.Reason(); got != tc.reason {
			t.Errorf("%s %d: reason %q, want %q", tc.client, tc.code, got, tc.reason)
		}
		if got := o.MaybeCredentials(); got != tc.credentials {
			t.Errorf("%s %d: credentials %v, want %v", tc.client, tc.code, got, tc.credentials)
		}
	}
	// A client killed by a signal failed, whatever its number reads as.
	if o := (Outcome{Client: "sdl-freerdp3", ExitCode: 128 + 2, Signaled: true}); Classify(o) != ClassFailed || o.Reason() != "" {
		t.Fatalf("signalled client: class %v reason %q", Classify(o), o.Reason())
	}
}

// The outcome names the client that ran, so its exit code can be read.
func TestStart_OutcomeNamesTheClient(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	t.Setenv("FAKERDP_EXIT", "2")
	l := &Launcher{Stdout: io.Discard, Stderr: io.Discard, OwnSignals: true}
	sess, err := l.Start(testPlan(t), lineCred("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if o := sess.Wait(); o.Client != "sdl-freerdp3" || Classify(o) != ClassEnded || o.Reason() != "logged off" {
		t.Fatalf("outcome %+v class %v", o, Classify(o))
	}
}
