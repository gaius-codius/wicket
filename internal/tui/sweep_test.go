package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestTUISecretSweep(t *testing.T) {
	t.Run("use-once", func(t *testing.T) {
		assertTUISweep(t, false)
	})
	t.Run("save", func(t *testing.T) {
		assertTUISweep(t, true)
	})
}

func assertTUISweep(t *testing.T, save bool) {
	t.Helper()
	dir := testutil.FakeRDPDir(t)
	testutil.PrependPATH(t, dir)
	tmp := t.TempDir()
	rec := filepath.Join(tmp, "rec.json")
	pidPath := filepath.Join(tmp, "pid")
	hold := filepath.Join(tmp, "go")
	t.Setenv("FAKERDP_RECORD", rec)
	t.Setenv("FAKERDP_PID", pidPath)
	t.Setenv("FAKERDP_HOLD_FILE", hold)
	t.Setenv("FAKERDP_EXIT", "0")

	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	type snap struct {
		cmd, env []byte
		err      error
	}
	seen := make(chan snap, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		var pid string
		for time.Now().Before(deadline) {
			b, err := os.ReadFile(pidPath)
			if err == nil && len(b) > 0 {
				pid = strings.TrimSpace(string(b))
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if pid == "" {
			seen <- snap{err: errors.New("child pid never appeared")}
			return
		}
		cmd, err := os.ReadFile("/proc/" + pid + "/cmdline")
		if err != nil {
			seen <- snap{err: fmt.Errorf("cmdline: %w", err)}
			return
		}
		if len(cmd) == 0 {
			seen <- snap{err: errors.New("cmdline empty")}
			return
		}
		env, err := os.ReadFile("/proc/" + pid + "/environ")
		if err != nil {
			seen <- snap{err: fmt.Errorf("environ: %w", err)}
			return
		}
		seen <- snap{cmd: cmd, env: env}
		_ = os.WriteFile(hold, []byte("x"), 0o600)
	}()

	if save {
		p, _ := h.m.app.Cfg.Profile("work")
		h.m.form = formState{oldName: "work", p: p, password: sentinel}
		h.m.view = viewForm
		h.m = press(h.m, "ctrl+s")
	} else {
		h.m = press(h.m, "enter")
		h.m = typeInto(h.m, sentinel)
	}
	// The last enter starts the client. The session view it leaves up is
	// swept while the client runs, then again once it has ended.
	running, cmd := act(h.m, keyMsg("enter"))
	if running.session == nil {
		t.Fatalf("no session: view %v status %q", running.view, running.status)
	}
	assertNoSentinel(t, []byte(running.View().Content), "session view")
	assertNoSentinel(t, []byte(running.status), "session status")
	assertNoSentinel(t, []byte(fmt.Sprintf("%#v", *running.session)), "session state")
	s := running.session.s
	h.m = settle(running, cmd)
	assertNoSentinel(t, s.output.Bytes(), "captured client output")

	select {
	case s := <-seen:
		if s.err != nil {
			t.Fatal(s.err)
		}
		assertNoSentinel(t, s.cmd, "cmdline")
		assertNoSentinel(t, s.env, "environ")
	case <-time.After(6 * time.Second):
		t.Fatal("timeout waiting for /proc snapshot")
	}

	cfgb, _ := os.ReadFile(h.cfg)
	stb, _ := os.ReadFile(h.state)
	assertNoSentinel(t, cfgb, "config.toml")
	assertNoSentinel(t, stb, "state.toml")
	assertNoSentinel(t, []byte(screen(h.m)), "tui status")
	assertNoSentinel(t, []byte(h.m.status), "status field")
	assertNoSentinel(t, h.stdout.Bytes(), "stdout")
	assertNoSentinel(t, h.stderr.Bytes(), "stderr")

	got := testutil.ReadRecord(t, rec)
	for _, a := range got.Argv {
		assertNoSentinel(t, []byte(a), "argv")
	}
	for k, v := range got.Environ {
		assertNoSentinel(t, []byte(k+"="+v), "environ-record")
	}
	if got.Stdin != sentinel+"\n" {
		t.Fatalf("stdin not delivered: %q", got.Stdin)
	}
}

func assertNoSentinel(t *testing.T, b []byte, label string) {
	t.Helper()
	if strings.Contains(string(b), sentinel) {
		t.Fatalf("sentinel leaked in %s: %q", label, b)
	}
}

func TestTUISweep_TwoConfigsDoNotShare(t *testing.T) {
	store := secret.NewMemory()
	h1 := newHarness(t, fixtureTOML("work", "h", "u"), store)
	h2 := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p1, _ := h1.m.app.Cfg.Profile("work")
	_ = store.Upsert(bg, secret.IdentityFor(h1.m.app.Cfg.Path(), p1), mustPassword(t, sentinel))
	p2, _ := h2.m.app.Cfg.Profile("work")
	if _, err := store.Lookup(bg, secret.IdentityFor(h2.m.app.Cfg.Path(), p2)); err == nil {
		t.Fatal("configs shared an item")
	}
}

// lookupPanics fails the test if anything reads a stored secret.
type lookupPanics struct{ *secret.Memory }

func (lookupPanics) Lookup(context.Context, secret.Identity) (secret.LookupResult, error) {
	panic("the presence check must not read the secret")
}

// The password line runs a keyring check on every new selection. It must be
// answered from metadata alone -- never by reading the secret -- and nothing
// it carries back or draws may hold the password.
func TestTUISweep_PresenceNeverCarriesPassword(t *testing.T) {
	store := lookupPanics{secret.NewMemory()}
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	if err := store.Upsert(bg, h.m.identity(p), mustPassword(t, sentinel)); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{80, 24}, {140, 30}} {
		m, cmd := update(h.m, teaWin(size[0], size[1]))
		reply := onlyReply(t, cmd)
		assertNoSentinel(t, []byte(fmt.Sprintf("%#v", reply)), "presence reply")
		m, _ = update(m, reply)
		out := screen(m)
		if !strings.Contains(out, "● saved in keyring") {
			t.Fatalf("%dx%d: want saved:\n%s", size[0], size[1], out)
		}
		assertNoSentinel(t, []byte(out), "screen")
		assertNoSentinel(t, []byte(m.View().Content), "raw view")
		assertNoSentinel(t, []byte(m.status), "status field")
		assertNoSentinel(t, []byte(fmt.Sprintf("%#v", m.presence)), "presence cache")
	}
}
