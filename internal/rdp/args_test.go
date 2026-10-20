package rdp

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/testutil"
)

// The arguments reach the client a line each, so one with a line break in it
// would arrive as two, the second anything the line held: a share path
// ending "\n/cert:ignore" would switch certificate checks off. An empty one
// would end the arguments early. Both are refused before anything starts.
func TestEncodeArgs_RefusesWhatALineCannotHold(t *testing.T) {
	t.Parallel()
	for _, a := range []string{"", "/drive:x,/tmp\n/cert:ignore", "/v:h\r", "\n"} {
		if _, err := encodeArgs([]string{"/v:h", a}); !errors.Is(err, ErrArgument) {
			t.Errorf("%q: err = %v, want ErrArgument", a, err)
		}
	}
	got, err := encodeArgs([]string{"/v:h", "/drive:a b,/tmp/a b"})
	if err != nil || got != "/v:h\n/drive:a b,/tmp/a b\n" {
		t.Fatalf("encoded %q, %v", got, err)
	}
}

// What is written before the client reads must fit in a pipe's buffer.
func TestEncodeArgs_Bound(t *testing.T) {
	t.Parallel()
	long := "/drive:x," + strings.Repeat("a", maxArgsBytes)
	if _, err := encodeArgs([]string{long}); !errors.Is(err, ErrArgsTooLong) {
		t.Fatalf("err = %v, want ErrArgsTooLong", err)
	}
}

// An argument the pipe cannot carry stops the launch before the client
// runs.
func TestStart_RefusesAnArgumentWithALineBreak(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)
	l := &Launcher{Stdout: io.Discard, Stderr: io.Discard, OwnSignals: true}
	plan := Plan{Client: "sdl-freerdp3", Args: []string{"/v:h", "/drive:x,/tmp\n/cert:ignore"}}
	if _, err := l.Start(plan, lineCred("pw")); !errors.Is(err, ErrArgument) {
		t.Fatalf("err = %v, want ErrArgument", err)
	}
	if _, err := os.Stat(rec); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the client ran (record: %v)", err)
	}
}

// The command line holds nothing but the instruction to read fd 3, and the
// client reads the plan's arguments there, in order, with the password last.
func TestStart_ArgumentsTravelOnTheArgsPipe(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)
	l := &Launcher{Stdout: io.Discard, Stderr: io.Discard, OwnSignals: true}
	plan := testPlan(t)
	sess, err := l.Start(plan, lineCred("pw: with spaces, and a colon"))
	if err != nil {
		t.Fatal(err)
	}
	sess.Wait()
	r := testutil.ReadRecord(t, rec)
	if !slices.Equal(r.RawArgv[1:], []string{argsFrom}) {
		t.Fatalf("command line %q", r.RawArgv)
	}
	if !slices.Equal(r.Argv[1:], plan.Args) {
		t.Fatalf("arguments %q, want %q", r.Argv[1:], plan.Args)
	}
	if !r.HasPassword || r.Password != "pw: with spaces, and a colon" {
		t.Fatalf("password %q (%v)", r.Password, r.HasPassword)
	}
	if r.Stdin != "" || r.StdinTTY {
		t.Fatalf("stdin %q, tty %v: it is the null device", r.Stdin, r.StdinTTY)
	}
}

// A launch without a credential sends the arguments alone.
func TestStart_NoCredentialNoPasswordArgument(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)
	l := &Launcher{Stdout: io.Discard, Stderr: io.Discard, OwnSignals: true}
	sess, err := l.Start(testPlan(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.Wait()
	if r := testutil.ReadRecord(t, rec); r.HasPassword {
		t.Fatalf("password %q sent without a credential", r.Password)
	}
}
