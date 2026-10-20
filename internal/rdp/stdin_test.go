package rdp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestPasswordOnlyOnArgsPipe(t *testing.T) {
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)
	const sentinel = "sentinel-password-value"
	pw, err := secret.NewPassword(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	p := config.Profile{
		Name: "work", Host: "192.168.1.20", User: "jdoe", Client: "sdl-freerdp3",
		Scale: 100, DynamicResolution: true,
	}
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	l := &Launcher{Stdout: os.Stdout, Stderr: os.Stderr}
	sess, err := l.Start(plan, pw)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Wait()
	r := testutil.ReadRecord(t, rec)
	if !r.HasPassword || r.Password != sentinel {
		t.Fatalf("password %q (present %v), want it on the args pipe", r.Password, r.HasPassword)
	}
	if len(r.RawArgv) < 2 || !slices.Equal(r.RawArgv[1:], []string{"/args-from:fd:3"}) {
		t.Fatalf("command line %q, want only /args-from:fd:3", r.RawArgv)
	}
	if strings.Contains(r.Stdin, sentinel) {
		t.Fatal("sentinel on the client's stdin")
	}
	blob := strings.Join(r.RawArgv, " ") + " "
	for k, v := range r.Environ {
		blob += k + "=" + v + " "
	}
	if strings.Contains(blob, sentinel) {
		t.Fatal("sentinel leaked to argv/environ")
	}
}
