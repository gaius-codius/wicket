package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
	"github.com/gaius-codius/wicket/internal/testutil/fakesecret"
)

// stallStore is a keyring that never answers a read, write or delete until
// the caller gives up, as a wedged daemon or an unanswered unlock prompt
// does. Presence, which has its own short deadline, answers from memory.
type stallStore struct {
	*secret.Memory
	mu      sync.Mutex
	started []string
	// gaveUp receives each operation once its context has ended it.
	gaveUp chan string
}

func newStallStore() *stallStore {
	return &stallStore{Memory: secret.NewMemory(), gaveUp: make(chan string, 16)}
}

func (s *stallStore) stall(ctx context.Context, op string) error {
	s.mu.Lock()
	s.started = append(s.started, op)
	s.mu.Unlock()
	<-ctx.Done()
	s.gaveUp <- op
	return fmt.Errorf("%w: %w", secret.ErrUnavailable, ctx.Err())
}

func (s *stallStore) Lookup(ctx context.Context, _ secret.Identity) (secret.LookupResult, error) {
	return secret.LookupResult{}, s.stall(ctx, "Lookup")
}
func (s *stallStore) Upsert(ctx context.Context, _ secret.Identity, _ secret.Password) error {
	return s.stall(ctx, "Upsert")
}
func (s *stallStore) Delete(ctx context.Context, _ secret.Identity) error {
	return s.stall(ctx, "Delete")
}

func (s *stallStore) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.started...)
}

// runCmd runs cmd, and any batch it stands for, in the background, as Bubble
// Tea would, and drops what it returns: the tests below care that a keyring
// call was made and then cancelled, not what it answered.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if b, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range b {
				runCmd(c)
			}
		}
	}()
}

// updateWithin is Update, failing the test rather than hanging it when
// Update blocks: on a stalled keyring, a blocked Update is the defect.
func updateWithin(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	type result struct {
		m   Model
		cmd tea.Cmd
	}
	done := make(chan *result, 1)
	go func() {
		nm, cmd := m.Update(msg)
		done <- &result{nm.(Model), cmd}
	}()
	select {
	case r := <-done:
		return r.m, r.cmd
	case <-time.After(2 * time.Second):
		t.Fatal("Update blocked on the keyring")
		return m, nil
	}
}

// waitGaveUp waits for the stalled operation op to see its context end.
func waitGaveUp(t *testing.T, s *stallStore, op string) {
	t.Helper()
	select {
	case got := <-s.gaveUp:
		if got != op {
			t.Fatalf("%s gave up, want %s", got, op)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was never cancelled", op)
	}
}

// Enter used to read the keyring inside Update, so a keyring that stopped
// answering froze Wicket: no key, not even Ctrl+C, and no signal could end
// it. The lookup now runs off the loop, the status line says so, keys other
// than the ones that stop the wait do nothing -- a second Enter included --
// and Esc stops waiting and asks for the password instead.
func TestKeyring_StalledLookupKeepsTheUIResponsive(t *testing.T) {
	store := newStallStore()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)

	m, cmd := updateWithin(t, h.m, keyMsg("enter"))
	if m.keyring == nil || cmd == nil {
		t.Fatal("Enter did not hand the keyring lookup off the update loop")
	}
	runCmd(cmd)
	out := screen(m)
	for _, want := range []string{"Checking the keyring for work…", "esc type the password", "ctrl+c cancel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q while waiting:\n%s", want, out)
		}
	}
	id := m.keyring.id
	for _, k := range []string{"enter", "enter", "q", "j", "e", "D", "n", "?"} {
		var c tea.Cmd
		m, c = updateKey(m, k)
		if c != nil || m.quit || m.keyring == nil || m.keyring.id != id || m.view != viewList {
			t.Fatalf("%q acted while the keyring was busy", k)
		}
	}
	if calls := store.calls(); len(calls) != 1 {
		t.Fatalf("keyring calls %v, want the one lookup", calls)
	}

	m, _ = updateKey(m, "esc")
	waitGaveUp(t, store, "Lookup")
	if m.keyring != nil || m.view != viewModal {
		t.Fatalf("esc: view %v, want the password dialog", m.view)
	}
	if !strings.Contains(screen(m), "stopped waiting for the keyring") {
		t.Fatalf("the dialog does not say why it is asking:\n%s", screen(m))
	}
	// Nothing was read, so nothing can be saved on top of the wait.
	if strings.Contains(screen(m), "save and connect") {
		t.Fatalf("ctrl+s offered without a keyring:\n%s", screen(m))
	}

	// The cancelled lookup's reply, when it comes, changes nothing.
	nm, c := m.Update(keyringReplyMsg{id: id, reply: credResolvedMsg{res: credResult{NeedModal: true}}})
	if after := nm.(Model); c != nil || after.view != viewModal || after.modal.lookupErr != m.modal.lookupErr {
		t.Fatal("a stale keyring reply was acted on")
	}
}

// Ctrl+C, or a SIGINT, stops the wait and goes back to the list; SIGTERM and
// SIGHUP quit, and cancel the lookup on the way.
func TestKeyring_CtrlCAndSignalsWhileWaiting(t *testing.T) {
	for _, stop := range []tea.Msg{keyMsg("ctrl+c"), signalMsg{sig: syscall.SIGINT}} {
		store := newStallStore()
		h := newHarness(t, fixtureTOML("work", "h", "u"), store)
		m, cmd := updateKey(h.m, "enter")
		runCmd(cmd)
		nm, _ := m.Update(stop)
		m = nm.(Model)
		waitGaveUp(t, store, "Lookup")
		if m.quit || m.keyring != nil || m.view != viewList || !strings.Contains(m.status, "Stopped waiting") {
			t.Fatalf("%v: view %v quit %v status %q", stop, m.view, m.quit, m.status)
		}
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		store := newStallStore()
		h := newHarness(t, fixtureTOML("work", "h", "u"), store)
		m, cmd := updateKey(h.m, "enter")
		runCmd(cmd)
		nm, quit := m.Update(signalMsg{sig: sig})
		m = nm.(Model)
		if !m.quit || quit == nil {
			t.Fatalf("%v while waiting on the keyring did not quit", sig)
		}
		waitGaveUp(t, store, "Lookup")
	}
}

// Once the wait has gone on a while, the status line says what Wicket may be
// waiting for.
func TestKeyring_SlowWaitMentionsTheUnlockPrompt(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), newStallStore())
	m, cmd := updateKey(h.m, "enter")
	defer func() { m, _ = updateKey(m, "ctrl+c") }()
	runCmd(cmd)
	stale, _ := m.Update(keyringSlowMsg{id: m.keyring.id + 1})
	if strings.Contains(stale.(Model).status, "unlock prompt") {
		t.Fatal("a timer from another operation changed the status")
	}
	nm, _ := m.Update(keyringSlowMsg{id: m.keyring.id})
	m = nm.(Model)
	if !strings.Contains(m.status, "answer its unlock prompt") {
		t.Fatalf("status %q", m.status)
	}
}

// A save that must move a password waits for the keyring before the config
// moves. Stopping the wait leaves the form, and the config, as they were.
func TestKeyring_StalledSaveCanBeStopped(t *testing.T) {
	store := newStallStore()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	m := press(h.m, "e")
	m = focusField(t, m, fieldName)
	m = press(m, "ctrl+u")
	m = typeInto(m, "office")
	m, cmd := updateKey(m, "ctrl+s")
	if m.keyring == nil || m.view != viewForm {
		t.Fatalf("save did not wait for the keyring off the loop: view %v", m.view)
	}
	runCmd(cmd)
	if !strings.Contains(screen(m), "esc stop waiting") {
		t.Fatalf("no way out offered:\n%s", screen(m))
	}
	m, _ = updateKey(m, "esc")
	waitGaveUp(t, store, "Lookup")
	if m.view != viewForm || m.form.p.Name != "office" || !strings.Contains(m.status, "Not saved") {
		t.Fatalf("view %v name %q status %q", m.view, m.form.p.Name, m.status)
	}
	if _, ok := m.app.Cfg.Profile("work"); !ok {
		t.Fatal("the config moved although the save was stopped")
	}
}

// A save whose config write is done but whose keyring work is not closes the
// form when the user stops waiting, and says what may be left undone.
func TestKeyring_StoppedAfterTheConfigWriteSaysWhatIsLeft(t *testing.T) {
	store := newStallStore()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	m := press(h.m, "e")
	m = focusField(t, m, fieldForget)
	m = press(m, "space")
	m, cmd := updateKey(m, "ctrl+s")
	runCmd(cmd)
	m, _ = updateKey(m, "esc")
	waitGaveUp(t, store, "Delete")
	if m.view != viewList || m.statusKind != statusError || !strings.Contains(m.status, "may be left in place") {
		t.Fatalf("view %v status %q", m.view, m.status)
	}
}

// Deleting removes the profile at once and its password off the loop.
func TestKeyring_StalledDeleteCanBeStopped(t *testing.T) {
	store := newStallStore()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	m := press(h.m, "D")
	m, cmd := updateKey(m, "y")
	if m.keyring == nil {
		t.Fatal("delete did not hand the keyring off the loop")
	}
	runCmd(cmd)
	if _, ok := m.app.Cfg.Profile("work"); ok {
		t.Fatal("profile not deleted")
	}
	m, _ = updateKey(m, "esc")
	waitGaveUp(t, store, "Delete")
	if m.statusKind != statusError || !strings.Contains(m.status, "Deleted work, but stopped waiting") {
		t.Fatalf("status %q", m.status)
	}
}

// The password dialog's ctrl+s stays on the dialog while the keyring works,
// and stopping the wait leaves the typed password there to connect with.
func TestKeyring_StalledDialogSaveFallsBackToConnectOnce(t *testing.T) {
	rec := withFakeRDP(t)
	store := newStallStore()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	// Open the dialog as a missing password would.
	p, _ := h.m.app.Cfg.Profile("work")
	nm, _ := h.m.openModal(p, nil, false)
	m := typeInto(nm.(Model), sentinel)
	m, cmd := updateKey(m, "ctrl+s")
	if m.keyring == nil || m.view != viewModal {
		t.Fatalf("view %v, want the dialog while the keyring works", m.view)
	}
	runCmd(cmd)
	m, _ = updateKey(m, "esc")
	waitGaveUp(t, store, "Upsert")
	// The write may have landed all the same, so the dialog does not claim
	// it did not: "enter connects without saving" was said of passwords the
	// keyring had already stored.
	if out := screen(m); m.view != viewModal || !strings.Contains(out, "may have been saved") || strings.Contains(out, "without saving") {
		t.Fatalf("view %v:\n%s", m.view, out)
	}
	m = press(m, "enter")
	if got := testutil.ReadRecord(t, rec); got.Stdin != sentinel+"\n" {
		t.Fatalf("stdin %q", got.Stdin)
	}
}

// The whole program, against a Secret Service that stops answering: Enter
// must not freeze it, and SIGTERM and SIGHUP must still end it and give the
// terminal back. Before the lookup left the update loop, only SIGKILL could
// end Wicket here, and that left the terminal in raw mode.
func TestRun_SignalsEndAWaitOnAStalledKeyring(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			_, srv, cleanup := fakesecret.Start(t)
			defer cleanup()
			srv.StallSearches()
			h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewDBus())

			inR, inW := io.Pipe()
			defer inW.Close()
			var out lockedBuffer
			done := make(chan error, 1)
			go func() {
				done <- runProgram(h.m, tea.WithInput(inR), tea.WithOutput(&out), tea.WithWindowSize(80, 24))
			}()
			if _, err := inW.Write([]byte("\r")); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for !strings.Contains(stripANSI(out.String()), "Checking the keyring") {
				if time.Now().After(deadline) {
					t.Fatalf("never drew the wait:\n%s", stripANSI(out.String()))
				}
				time.Sleep(20 * time.Millisecond)
			}
			// Keys still reach the loop: j moves nothing while waiting, but
			// the loop must take it rather than queue behind the keyring.
			if _, err := inW.Write([]byte("j")); err != nil {
				t.Fatal(err)
			}

			if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				wantStopped(t, err, sig)
			case <-time.After(5 * time.Second):
				t.Fatalf("Wicket did not quit on %v while the keyring was stalled", sig)
			}
			// The alternate screen is left, as it is on any clean exit.
			if !strings.Contains(out.String(), "\x1b[?1049l") {
				t.Fatalf("terminal not restored: %q", out.String())
			}
		})
	}
}

// landingStore is a keyring whose writes land even when the caller has
// given up on them, as a daemon that received the request before the cancel
// does: Upsert stores the password, then holds its answer until the caller's
// context ends.
type landingStore struct {
	*secret.Memory
	upserting chan struct{}
}

func (s *landingStore) Upsert(ctx context.Context, id secret.Identity, pw secret.Password) error {
	if err := s.Memory.Upsert(context.Background(), id, pw); err != nil {
		return err
	}
	s.upserting <- struct{}{}
	<-ctx.Done()
	return fmt.Errorf("%w: %w", secret.ErrUnavailable, ctx.Err())
}

// editHost opens the edit form on work and changes its host, ready to save.
func editHost(t *testing.T, m Model, host string) Model {
	t.Helper()
	m = press(m, "e")
	m = focusField(t, m, fieldHost)
	m = press(m, "ctrl+u")
	return typeInto(m, host)
}

// A save whose carry had already copied the password when the user stopped
// waiting -- its reply on the way, not yet taken -- puts the keyring back:
// the config was never written, so the copy is under details no profile
// has. It used to be left there, and dropped along with the reply.
func TestKeyring_StoppedCarryRemovesACopyThatFinished(t *testing.T) {
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	old, _ := h.m.app.Cfg.Profile("work")
	_ = store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), old), mustPassword(t, "secret"))
	m := editHost(t, h.m, "other")
	m, cmd := updateKey(m, "ctrl+s")
	if m.keyring == nil {
		t.Fatal("save did not carry the password off the loop")
	}
	moved := old
	moved.Host = "other"
	// The carry runs to the end, and its reply waits behind the esc.
	reply := awaitKeyring(cmd, m.keyring.id)
	if got := storedAs(t, store, m.app, moved); got != "secret" {
		t.Fatalf("setup: carry stored %q under the new host", got)
	}
	m, cmd = updateKey(m, "esc")
	m, _ = drainKeyring(m, cmd)
	nm, _ := m.Update(reply)
	m = nm.(Model)

	if got := storedAs(t, store, m.app, moved); got != "" {
		t.Fatal("the copy under the new host was left in the keyring")
	}
	if got := storedAs(t, store, m.app, old); got != "secret" {
		t.Fatalf("old entry %q, want it untouched", got)
	}
	if p, _ := m.app.Cfg.Profile("work"); p.Host != "h" {
		t.Fatalf("config moved to %q although the save was stopped", p.Host)
	}
	if m.view != viewForm || !strings.Contains(m.status, "Not saved") || strings.Contains(m.status, "may be left") {
		t.Fatalf("view %v status %q", m.view, m.status)
	}
}

// A copy that lands after the user stopped waiting -- sent before the
// cancel, confirmed never -- is removed too, once the carry has ended.
func TestKeyring_StoppedCarryRemovesACopyThatLandsLate(t *testing.T) {
	store := &landingStore{Memory: secret.NewMemory(), upserting: make(chan struct{}, 1)}
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	old, _ := h.m.app.Cfg.Profile("work")
	_ = store.Memory.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), old), mustPassword(t, "secret"))
	m := editHost(t, h.m, "other")
	m, cmd := updateKey(m, "ctrl+s")
	runCmd(cmd)
	select {
	case <-store.upserting:
	case <-time.After(5 * time.Second):
		t.Fatal("the carry never wrote the copy")
	}
	m, cmd = updateKey(m, "esc")
	m, _ = drainKeyring(m, cmd)

	moved := old
	moved.Host = "other"
	if got := storedAs(t, store.Memory, m.app, moved); got != "" {
		t.Fatal("the copy under the new host was left in the keyring")
	}
	if got := storedAs(t, store.Memory, m.app, old); got != "secret" {
		t.Fatalf("old entry %q, want it untouched", got)
	}
	if m.keyring != nil || m.view != viewForm || !strings.Contains(m.status, "Not saved") {
		t.Fatalf("view %v status %q", m.view, m.status)
	}
}
