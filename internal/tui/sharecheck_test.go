package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
)

// shareHarness is a profile that shares a folder. The share check is
// counted, and once it has passed, a link to / is put in the folder: any
// later read of the share in the same save or connect would find it and
// refuse, as every save and connect once read each share twice.
func shareHarness(t *testing.T, store secret.Store) (*harness, *atomic.Int32) {
	t.Helper()
	dir := t.TempDir()
	body := fixtureTOML("work", "192.168.1.20", "jdoe") + "shares = [{ path = " + strconv.Quote(dir) + " }]\n"
	h := newHarness(t, body, store)
	var reads atomic.Int32
	var plant sync.Once
	h.m.app.CheckShares = func(p config.Profile) (config.CheckedShares, error) {
		reads.Add(1)
		checked, err := config.CheckShares(p)
		plant.Do(func() {
			if err := os.Symlink("/", filepath.Join(dir, "z:")); err != nil {
				t.Error(err)
			}
		})
		return checked, err
	}
	return h, &reads
}

func TestShareCheck_SaveReadsEachShareOnce(t *testing.T) {
	h, reads := shareHarness(t, secret.NewMemory())
	m := press(h.m, "e", "ctrl+s")
	// The fixture's inline shares are a layout the patch does not follow,
	// so the save may warn that it rewrote the file; it must not refuse.
	if m.view != viewList || !strings.HasPrefix(m.status, "Saved work") {
		t.Fatalf("view %v, status %q", m.view, m.status)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("shares read %d times, want 1", n)
	}
}

func TestShareCheck_ConnectReadsEachShareOnce(t *testing.T) {
	for _, stored := range []bool{true, false} {
		t.Run("stored="+strconv.FormatBool(stored), func(t *testing.T) {
			rec := withFakeRDP(t)
			store := secret.NewMemory()
			h, reads := shareHarness(t, store)
			if stored {
				p, _ := h.m.app.Cfg.Profile("work")
				_ = store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, sentinel))
			}
			m := press(h.m, "enter")
			if !stored {
				// No saved password: the dialog asks, and connects with
				// what the check found before it opened.
				if m.view != viewModal {
					t.Fatalf("view %v, want the password dialog; status %q", m.view, m.status)
				}
				m = press(typeInto(m, sentinel), "enter")
			}
			if m.statusKind == statusError {
				t.Fatalf("status %q", m.status)
			}
			if got := testutil.ReadRecord(t, rec); got.Stdin != sentinel+"\n" {
				t.Fatalf("stdin %q", got.Stdin)
			}
			if n := reads.Load(); n != 1 {
				t.Fatalf("shares read %d times, want 1", n)
			}
		})
	}
}

// slowShares makes the share check wait until the test lets it go.
func slowShares(t *testing.T, h *harness) {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h.m.app.CheckShares = func(p config.Profile) (config.CheckedShares, error) {
		<-release
		return config.CheckShares(p)
	}
}

// A share on an automount or NFS can take a long time to read. The read runs
// off the loop: Wicket still draws, says what it is waiting for, and Esc
// stops the wait.
func TestShareCheck_SlowCheckDoesNotHoldTheLoop(t *testing.T) {
	t.Run("save", func(t *testing.T) {
		h, _ := shareHarness(t, secret.NewMemory())
		slowShares(t, h)
		m := press(h.m, "e")
		m, cmd := updateKey(m, "ctrl+s")
		if m.keyring == nil {
			t.Fatal("save read the shares on the update loop")
		}
		runCmd(cmd)
		if out := screen(m); !strings.Contains(out, "Checking the shared folders of work") {
			t.Fatalf("screen:\n%s", out)
		}
		m, _ = updateKey(m, "esc")
		if m.keyring != nil || m.view != viewForm || !strings.Contains(m.status, "Not saved: stopped checking") {
			t.Fatalf("view %v, status %q", m.view, m.status)
		}
	})
	t.Run("connect", func(t *testing.T) {
		withFakeRDP(t)
		h, _ := shareHarness(t, secret.NewMemory())
		slowShares(t, h)
		m, cmd := updateKey(h.m, "enter")
		if m.keyring == nil {
			t.Fatal("connect read the shares on the update loop")
		}
		runCmd(cmd)
		if out := screen(m); !strings.Contains(out, "Checking the shared folders of work") {
			t.Fatalf("screen:\n%s", out)
		}
		m, _ = updateKey(m, "esc")
		if m.keyring != nil || m.view != viewList || !strings.Contains(m.status, "Not connected: stopped checking") {
			t.Fatalf("view %v, status %q", m.view, m.status)
		}
	})
}

// A share the check refuses is reported before the password is asked for,
// as it was when the check ran on the loop.
func TestShareCheck_RefusalComesBeforeThePrompt(t *testing.T) {
	withFakeRDP(t)
	dir := t.TempDir()
	if err := os.Symlink("/", filepath.Join(dir, "z:")); err != nil {
		t.Fatal(err)
	}
	body := fixtureTOML("work", "h", "u") + "shares = [{ path = " + strconv.Quote(dir) + " }]\n"
	h := newHarness(t, body, secret.NewMemory())
	m := press(h.m, "enter")
	if m.view != viewList || m.statusKind != statusError || !strings.Contains(m.status, "FreeRDP follows links") {
		t.Fatalf("view %v, status %q", m.view, m.status)
	}
}

// A save the user stopped waiting for leaves its check reading on, and the
// form can be edited meanwhile: the check reads a copy of the shares, not
// the form's own, or the race detector finds the two touching one slice.
func TestShareCheck_StoppedSaveDoesNotShareTheFormsShares(t *testing.T) {
	h, _ := shareHarness(t, secret.NewMemory())
	read := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h.m.app.CheckShares = func(p config.Profile) (config.CheckedShares, error) {
		checked, err := config.CheckShares(p)
		close(read)
		// It goes on reading, as a slow walk would, while the form is
		// edited.
		for {
			select {
			case <-release:
				return checked, err
			default:
				_ = len(p.Shares[0].Path)
			}
		}
	}
	m := press(h.m, "e")
	m, cmd := updateKey(m, "ctrl+s")
	runCmd(cmd)
	<-read
	m, _ = updateKey(m, "esc")
	other := t.TempDir()
	m = focusField(t, m, shareRow(0))
	m = press(m, "enter", "ctrl+u")
	m, _ = act(m, tea.PasteMsg{Content: other})
	m = press(m, "enter")
	if m.form.p.Shares[0].Path != other {
		t.Fatalf("shares %+v", m.form.p.Shares)
	}
}
