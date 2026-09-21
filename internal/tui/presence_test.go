package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/secret"
)

// presenceReplies runs cmd, and any batch it stands for, and returns the
// presence replies among the messages.
func presenceReplies(cmd tea.Cmd) []presenceMsg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case presenceMsg:
		return []presenceMsg{msg}
	case tea.BatchMsg:
		var out []presenceMsg
		for _, c := range msg {
			out = append(out, presenceReplies(c)...)
		}
		return out
	}
	return nil
}

func onlyReply(t *testing.T, cmd tea.Cmd) presenceMsg {
	t.Helper()
	rs := presenceReplies(cmd)
	if len(rs) != 1 {
		t.Fatalf("want one presence check, got %d", len(rs))
	}
	return rs[0]
}

func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}

// Painting the list must never reach the keyring: panicStore panics on any
// call. The check is a command for the runtime to run off the UI goroutine,
// and only running it reaches the store.
func TestPresence_ListPaintDoesNoKeyringIO(t *testing.T) {
	h := newHarness(t, twoProfiles(), panicStore{})
	m, cmd := update(h.m, teaWin(80, 24))
	for _, k := range []string{"j", "k", "s", "/", "l"} {
		_ = screen(m)
		m, _ = update(m, keyMsg(k))
	}
	for _, size := range [][2]int{{50, 20}, {80, 24}, {140, 30}} {
		m, _ = update(m, teaWin(size[0], size[1]))
		if out := screen(m); size[0] >= widthCompact && !strings.Contains(out, "checking…") {
			t.Fatalf("%dx%d: want the unanswered check shown:\n%s", size[0], size[1], out)
		}
	}
	if cmd == nil {
		t.Fatal("selecting a profile should schedule a presence check")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("the check should be what reaches the store")
		}
	}()
	cmd()
}

func TestPresence_ShowsSavedAndNotSaved(t *testing.T) {
	store := secret.NewMemory()
	h := newHarness(t, twoProfiles(), store)
	p, _ := h.m.app.Cfg.Profile("work")
	if err := store.Upsert(bg, h.m.identity(p), mustPassword(t, "pw")); err != nil {
		t.Fatal(err)
	}
	m, cmd := update(h.m, teaWin(80, 24))
	m, _ = update(m, onlyReply(t, cmd))
	if out := screen(m); !strings.Contains(out, "password   ● saved in keyring") {
		t.Fatalf("want saved:\n%s", out)
	}
	m, cmd = update(m, keyMsg("j"))
	m, _ = update(m, onlyReply(t, cmd))
	if out := screen(m); !strings.Contains(out, "password   asks when connecting") {
		t.Fatalf("want not saved:\n%s", out)
	}
	// Going back is answered from the cache.
	m, cmd = update(m, keyMsg("k"))
	if rs := presenceReplies(cmd); len(rs) != 0 {
		t.Fatalf("a known identity was checked again: %v", rs)
	}
	if out := screen(m); !strings.Contains(out, "● saved in keyring") {
		t.Fatalf("cached answer lost:\n%s", out)
	}
}

// unavailableStore has a keyring that never answers a presence check.
type unavailableStore struct{ *secret.Memory }

func (unavailableStore) Presence(context.Context, secret.Identity) (secret.Presence, error) {
	return secret.NotSaved, fmt.Errorf("%w: no bus", secret.ErrUnavailable)
}

// Presence answers NotSaved alongside its error. A keyring that did not
// answer has not said there is no password, so the line must not say the
// connection will ask for one.
func TestPresence_UnavailableNeverSaysAsks(t *testing.T) {
	h := newHarness(t, twoProfiles(), unavailableStore{secret.NewMemory()})
	for _, size := range [][2]int{{80, 24}, {140, 30}} {
		m, cmd := update(h.m, teaWin(size[0], size[1]))
		m, _ = update(m, onlyReply(t, cmd))
		out := screen(m)
		if strings.Contains(out, "asks") {
			t.Fatalf("%dx%d: unavailable keyring drawn as asks:\n%s", size[0], size[1], out)
		}
		if !strings.Contains(out, "keyring unavailable") {
			t.Fatalf("%dx%d: want keyring unavailable:\n%s", size[0], size[1], out)
		}
	}
}

// deadlineStore records the context each check is given.
type deadlineStore struct {
	*secret.Memory
	deadline time.Time
	ok       bool
}

func (d *deadlineStore) Presence(ctx context.Context, id secret.Identity) (secret.Presence, error) {
	d.deadline, d.ok = ctx.Deadline()
	return d.Memory.Presence(ctx, id)
}

func TestPresence_CheckHasShortTimeout(t *testing.T) {
	store := &deadlineStore{Memory: secret.NewMemory()}
	h := newHarness(t, twoProfiles(), store)
	_, cmd := update(h.m, teaWin(80, 24))
	onlyReply(t, cmd)
	if left := time.Until(store.deadline); !store.ok || left > presenceTimeout || presenceTimeout > 5*time.Second {
		t.Fatalf("check deadline in %v (set %v), want a short one", left, store.ok)
	}
}

// savePassword types pw into the edit form of the selected profile and saves.
func savePassword(t *testing.T, m Model, pw string) (Model, tea.Cmd) {
	t.Helper()
	m = press(m, "e")
	m = focusField(t, m, fieldPassword)
	m = typeInto(m, pw)
	return act(m, keyMsg("ctrl+s"))
}

// A save can put a password where the list had seen none, so what the list
// knew is dropped and asked again.
func TestPresence_SaveInvalidatesCache(t *testing.T) {
	h := newHarness(t, twoProfiles(), secret.NewMemory())
	m, cmd := update(h.m, teaWin(80, 24))
	m, _ = update(m, onlyReply(t, cmd))
	if out := screen(m); !strings.Contains(out, "asks when connecting") {
		t.Fatalf("setup:\n%s", out)
	}
	m, cmd = savePassword(t, m, "pw")
	if out := screen(m); strings.Contains(out, "asks when connecting") {
		t.Fatalf("stale answer survived the save:\n%s", out)
	}
	m, _ = update(m, onlyReply(t, cmd))
	if out := screen(m); !strings.Contains(out, "● saved in keyring") {
		t.Fatalf("want saved after the save:\n%s", out)
	}
}

// A reply to a check made before a save describes the keyring as it was.
// Arriving after the save, it must not overwrite what the new check finds.
func TestPresence_StaleReplyIgnored(t *testing.T) {
	h := newHarness(t, twoProfiles(), secret.NewMemory())
	m, cmd := update(h.m, teaWin(80, 24))
	before := onlyReply(t, cmd) // not saved, but not delivered yet
	m, cmd = savePassword(t, m, "pw")
	m, _ = update(m, before)
	if out := screen(m); strings.Contains(out, "asks when connecting") {
		t.Fatalf("stale reply accepted:\n%s", out)
	}
	m, _ = update(m, onlyReply(t, cmd))
	m, _ = update(m, before)
	if out := screen(m); !strings.Contains(out, "● saved in keyring") {
		t.Fatalf("stale reply overwrote the fresh answer:\n%s", out)
	}
}

// Forgetting the password, renaming the profile or deleting it all change
// what the keyring holds for an identity, and each is checked again.
func TestPresence_ForgetRenameAndDeleteInvalidate(t *testing.T) {
	store := secret.NewMemory()
	h := newHarness(t, twoProfiles(), store)
	p, _ := h.m.app.Cfg.Profile("work")
	_ = store.Upsert(bg, h.m.identity(p), mustPassword(t, "pw"))
	m, cmd := update(h.m, teaWin(80, 24))
	m, _ = update(m, onlyReply(t, cmd))

	// Forget.
	m = press(m, "e")
	m = focusField(t, m, fieldForget)
	m = press(m, "space")
	m, cmd = act(m, keyMsg("ctrl+s"))
	m, _ = update(m, onlyReply(t, cmd))
	if out := screen(m); !strings.Contains(out, "asks when connecting") {
		t.Fatalf("after forget:\n%s", out)
	}

	// Rename: a new identity, which is not known yet.
	_ = store.Upsert(bg, m.identity(p), mustPassword(t, "pw"))
	m = press(m, "e")
	m = focusField(t, m, fieldName)
	m = press(m, "ctrl+u")
	m = typeInto(m, "renamed")
	m, cmd = act(m, keyMsg("ctrl+s"))
	if name := selectedName(t, m); name != "renamed" {
		t.Fatalf("selected %q after rename", name)
	}
	m, _ = update(m, onlyReply(t, cmd))
	if out := screen(m); !strings.Contains(out, "● saved in keyring") {
		t.Fatalf("after rename the carried password should show:\n%s", out)
	}

	// Delete drops the identity from the cache.
	np, _ := m.app.Cfg.Profile("renamed")
	id := m.identity(np)
	m = press(m, "D", "y")
	if _, ok := m.presence[id]; ok {
		t.Fatal("deleted identity still cached")
	}
}

// A keyring error is reported, not swallowed into "asks".
func TestPresence_ErrorWinsOverAnswer(t *testing.T) {
	h := newHarness(t, twoProfiles(), secret.NewMemory())
	m, cmd := update(h.m, teaWin(80, 24))
	r := onlyReply(t, cmd)
	r.got, r.err = secret.Saved, errors.New("boom")
	m, _ = update(m, r)
	if out := screen(m); !strings.Contains(out, "keyring unavailable") {
		t.Fatalf("an error must read as unavailable:\n%s", out)
	}
}

// The compact layout shows no details, so it does not ask the keyring.
func TestPresence_CompactDoesNotCheck(t *testing.T) {
	h := newHarness(t, twoProfiles(), panicStore{})
	_, cmd := update(h.m, teaWin(50, 20))
	if cmd != nil {
		t.Fatal("compact layout scheduled a check it has nowhere to show")
	}
}

// recoveringStore is a keyring that cannot answer presence checks until it
// is told it has recovered.
type recoveringStore struct {
	*secret.Memory
	down bool
}

func (r *recoveringStore) Presence(ctx context.Context, id secret.Identity) (secret.Presence, error) {
	if r.down {
		return secret.NotSaved, fmt.Errorf("%w: stalled", secret.ErrUnavailable)
	}
	return r.Memory.Presence(ctx, id)
}

// "keyring unavailable" is not the last word on a profile: once it has stood
// for a while, the next update asks again, and a keyring that has recovered
// says what it holds. It used to stand until something else cleared it.
func TestPresence_UnavailableIsAskedAgain(t *testing.T) {
	store := &recoveringStore{Memory: secret.NewMemory(), down: true}
	h := newHarness(t, twoProfiles(), store)
	p, _ := h.m.app.Cfg.Profile("work")
	if err := store.Memory.Upsert(bg, h.m.identity(p), mustPassword(t, "pw")); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	h.m.now = func() time.Time { return now }
	m, cmd := update(h.m, teaWin(80, 24))
	m, _ = update(m, onlyReply(t, cmd))
	if !strings.Contains(screen(m), "keyring unavailable") {
		t.Fatalf("setup: want keyring unavailable:\n%s", screen(m))
	}
	store.down = false
	// Soon after, the answer stands: a keyring that is down is not asked
	// on every keypress.
	now = now.Add(time.Second)
	if _, cmd := update(m, teaWin(80, 24)); len(presenceReplies(cmd)) != 0 {
		t.Fatal("asked again at once")
	}
	now = now.Add(presenceRetryAfter)
	m, cmd = update(m, teaWin(80, 24))
	m, _ = update(m, onlyReply(t, cmd))
	if out := screen(m); !strings.Contains(out, "saved in keyring") {
		t.Fatalf("a recovered keyring is still drawn as unavailable:\n%s", out)
	}
}
