package secret

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/testutil/fakesecret"
)

// kind is the answer an operation gives, as finely as a caller tells answers
// apart: the TUI and the CLI word a dismissed prompt, an unanswered one and a
// keyring out of reach differently, inside ErrLocked and outside it.
type kind string

const (
	kSuccess  kind = "success"
	kNotFound kind = "ErrNotFound"
	// kLost is ErrUnavailable with no prompt answer in it: the bus, the
	// daemon or the caller went away.
	kLost             kind = "ErrUnavailable, keyring out of reach"
	kDismissed        kind = "ErrUnavailable, prompt dismissed"
	kUnanswered       kind = "ErrUnavailable, prompt unanswered"
	kLocked           kind = "ErrLocked"
	kLockedDismissed  kind = "ErrLocked, unlock dismissed"
	kLockedUnanswered kind = "ErrLocked, unlock unanswered"
	// kNotReached marks an operation that never makes the call a fault
	// needs, so the fault cannot happen to it. Its subtest is skipped.
	kNotReached kind = "not reached"
)

// kindOf classifies err. A result that is more than one kind at once, or
// none of them, gets a name of its own so the table cannot match it.
func kindOf(err error) kind {
	locked := errors.Is(err, ErrLocked)
	notFound := errors.Is(err, ErrNotFound)
	unavailable := errors.Is(err, ErrUnavailable)
	dismissed := errors.Is(err, ErrPromptDismissed)
	unanswered := errors.Is(err, ErrPromptTimeout)
	switch {
	case err == nil:
		return kSuccess
	case notFound && (locked || unavailable):
		return "ErrNotFound with another kind"
	case notFound:
		return kNotFound
	case dismissed && unanswered:
		return "both dismissed and unanswered"
	case locked && !unavailable:
		// ErrLocked always wraps why the unlock failed, which is
		// ErrUnavailable.
		return "ErrLocked without ErrUnavailable"
	case locked && dismissed:
		return kLockedDismissed
	case locked && unanswered:
		return kLockedUnanswered
	case locked:
		return kLocked
	case unavailable && dismissed:
		return kDismissed
	case unavailable && unanswered:
		return kUnanswered
	case unavailable:
		return kLost
	}
	return "none of the kinds"
}

// style is how the fake keyring behaves when it needs the user.
type style int

const (
	// styleOpen is an unlocked keyring: nothing needs the user.
	styleOpen style = iota
	// stylePrompts is a locked keyring that answers a write or a delete
	// with a prompt, as KeePassXC does. A lookup unlocks through Unlock.
	stylePrompts
	// styleRefuses is a locked keyring that refuses a write or a delete
	// with IsLocked, so the client unlocks and asks again, as gnome-keyring
	// does. Every operation reaches the keyring through Unlock.
	styleRefuses
)

// env is what a fault's steps can act on.
type env struct {
	srv    *fakesecret.Server
	cancel context.CancelFunc
	// replied closes once the bus has passed on the fake's reply to Prompt.
	// It is set only for a fault with watchPrompt set.
	replied <-chan struct{}
}

// fault is one way the keyring goes wrong, with how to bring it about and
// what each operation must answer.
type fault struct {
	name  string
	style style
	// empty leaves nothing stored; otherwise one password is.
	empty  bool
	prompt fakesecret.PromptMode
	// hold is a method the fake never answers.
	hold string
	// failUnlock is the error name Unlock answers with.
	failUnlock string
	// prepare sets up a fault of the stored item itself.
	prepare func(srv *fakesecret.Server, item dbus.ObjectPath)
	// promptTimeout shortens the wait for an answer, for a fault that is
	// that wait running out.
	promptTimeout time.Duration
	// deadline gives the caller a deadline, for a fault that is the caller
	// running out of time.
	deadline time.Duration
	// watchPrompt has the sweep watch for the bus passing on Prompt's reply,
	// so under can act on a dialog that is certainly on screen.
	watchPrompt bool
	// before acts before the operation starts.
	before func(*testing.T, *env)
	// under acts once the operation is running, after it has reached the
	// state the fault needs.
	under func(*testing.T, *env)
	// calls are methods the fake must have seen, which proves the fault was
	// reached rather than the operation failing early for another reason.
	calls []string
	// silent means the fake must have seen no call at all.
	silent bool
	// cause is an error the answer must also wrap, where the kind alone
	// does not show the fault was the one reached.
	cause error

	lookup, upsert, delete kind
	// known names an operation, by its sweep name, whose answer is wrong in
	// the code and not yet fixed, with what is wrong. Its subtest is skipped,
	// and fails once the answer matches the row, so the entry cannot outlive
	// its fix.
	known map[string]string
}

func (f fault) want(op string) kind {
	switch op {
	case "lookup":
		return f.lookup
	case "upsert":
		return f.upsert
	case "delete":
		return f.delete
	}
	return "unknown operation " + kind(op)
}

// waitReplied waits until the bus has passed on the fake's reply to Prompt.
func waitReplied(t *testing.T, e *env) {
	t.Helper()
	select {
	case <-e.replied:
	case <-time.After(5 * time.Second):
		t.Fatalf("the fake never replied to Prompt; calls: %v", e.srv.Calls())
	}
}

// waitNameGone waits until the bus no longer has an owner for the Secret
// Service's name, so a call made after a Disconnect finds it gone rather than
// racing the bus noticing.
func waitNameGone(t *testing.T) {
	t.Helper()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var has bool
		if err := conn.BusObject().Call(busName+".NameHasOwner", 0, ssBus).Store(&has); err != nil {
			t.Fatal(err)
		}
		if !has {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the bus still has an owner for the Secret Service name")
}

// afterReply is a fault's step for a dialog that is certainly on screen: it
// waits for the reply to Prompt and then runs act.
func afterReply(act func(*testing.T, *env)) func(*testing.T, *env) {
	return func(t *testing.T, e *env) {
		t.Helper()
		waitReplied(t, e)
		act(t, e)
	}
}

// waitCall is a fault's step that waits for the client to reach method on the
// fake and then runs act.
func waitCall(method string, act func(*testing.T, *env)) func(*testing.T, *env) {
	return func(t *testing.T, e *env) {
		t.Helper()
		waitForCall(t, e.srv, method)
		act(t, e)
	}
}

func disconnect(_ *testing.T, e *env)   { e.srv.Disconnect() }
func killBus(_ *testing.T, e *env)      { e.srv.KillBus() }
func cancelCaller(_ *testing.T, e *env) { e.cancel() }

func dropName(t *testing.T, e *env) {
	t.Helper()
	if err := e.srv.DropName(ssBus); err != nil {
		t.Fatal(err)
	}
}

// disconnectAndWait loses the daemon and waits until the bus knows it is gone.
func disconnectAndWait(t *testing.T, e *env) {
	t.Helper()
	e.srv.Disconnect()
	waitNameGone(t)
}

// lostDuring is a row for the daemon going while method, which the fake
// holds, is in flight, with what each operation answers; one that
// never calls method is kNotReached.
func lostDuring(name, method string, lookup, upsert, del kind) fault {
	return fault{
		name: name, hold: method, under: waitCall(method, disconnect), calls: []string{method},
		lookup: lookup, upsert: upsert, delete: del,
	}
}

// faultTable is the contract: for every way the fake Secret Service can make
// the keyring behind one stored password go wrong, what each operation must
// answer. TestDBus_FaultSweep runs every row against Lookup, Upsert and
// Delete and fails on any other kind.
//
// Two lost keyrings were once reported as a locked one, each found by
// reading the code. A fault the fake learns to produce must get a row here,
// so that every operation is checked against it, not only the one a test was
// written for. An answer that looks wrong is not a reason to change the row
// to match: fix the code, or name the operation in the row's known.
//
// The prompt rows come in pairs, because a keyring that prompts for a write
// (stylePrompts) and one that makes the client unlock first (styleRefuses)
// give different answers when the prompt is refused: the first is a write
// that failed, the second a keyring that stayed locked.
var faultTable = []fault{
	// No fault: what success and "not there" look like.
	{name: "healthy", lookup: kSuccess, upsert: kSuccess, delete: kSuccess},
	{name: "nothing-stored", empty: true, lookup: kNotFound, upsert: kSuccess, delete: kNotFound},
	{
		name: "prompt-accepted", style: stylePrompts, prompt: fakesecret.PromptAccept, calls: []string{"Prompt"},
		lookup: kSuccess, upsert: kSuccess, delete: kSuccess,
	},
	{
		name: "unlock-prompt-accepted", style: styleRefuses, prompt: fakesecret.PromptAccept, calls: []string{"Unlock", "Prompt"},
		lookup: kSuccess, upsert: kSuccess, delete: kSuccess,
	},

	// The session bus is lost. Killing it leaves a held call held, so the
	// loss is certainly during the call.
	{
		name: "bus-lost-before-call", silent: true, before: killBus,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "bus-lost-under-prompt", style: stylePrompts, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: afterReply(killBus), calls: []string{"Prompt"},
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "bus-lost-during-prompt-call", style: stylePrompts, prompt: fakesecret.PromptHang,
		under: waitCall("Prompt", killBus), calls: []string{"Prompt"},
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "bus-lost-under-unlock-prompt", style: styleRefuses, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: afterReply(killBus), calls: []string{"Unlock", "Prompt"},
		lookup: kLost, upsert: kLost, delete: kLost,
	},

	// The keyring daemon is lost while the bus stays up.
	{
		name: "daemon-lost-before-call", silent: true,
		before: disconnectAndWait,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	lostDuring("daemon-lost-during-search", "SearchItems", kLost, kNotReached, kLost),
	lostDuring("daemon-lost-during-open-session", "OpenSession", kLost, kLost, kNotReached),
	lostDuring("daemon-lost-during-get-secrets", "GetSecrets", kLost, kNotReached, kNotReached),
	lostDuring("daemon-lost-during-create-item", "CreateItem", kNotReached, kLost, kNotReached),
	lostDuring("daemon-lost-during-item-delete", "Delete", kNotReached, kNotReached, kLost),
	{
		name: "daemon-lost-during-unlock", style: styleRefuses, prompt: fakesecret.PromptStall, hold: "Unlock",
		under: waitCall("Unlock", disconnect), calls: []string{"Unlock"},
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "daemon-lost-during-prompt-call", style: stylePrompts, prompt: fakesecret.PromptStall, hold: "Prompt",
		under: waitCall("Prompt", disconnect), calls: []string{"Prompt"},
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "daemon-lost-during-unlock-prompt-call", style: styleRefuses, prompt: fakesecret.PromptStall, hold: "Prompt",
		under: waitCall("Prompt", disconnect), calls: []string{"Unlock", "Prompt"},
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "daemon-lost-after-prompt-reply", style: stylePrompts, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: afterReply(disconnect), cause: errDaemonLost,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "daemon-lost-after-unlock-prompt-reply", style: styleRefuses, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: afterReply(disconnect), calls: []string{"Unlock"}, cause: errDaemonLost,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "daemon-replaced-after-prompt-reply", style: stylePrompts, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: afterReply(dropName), cause: errDaemonLost,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "daemon-replaced-after-unlock-prompt-reply", style: styleRefuses, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: afterReply(dropName), calls: []string{"Unlock"}, cause: errDaemonLost,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		// A daemon that never answers anything, against a caller with a
		// deadline: the deadline is the fault, so its length cannot change
		// the answer.
		name: "daemon-wedged-past-deadline", deadline: 100 * time.Millisecond, cause: context.DeadlineExceeded,
		prepare: func(srv *fakesecret.Server, _ dbus.ObjectPath) { srv.StallEverything() },
		lookup:  kLost, upsert: kLost, delete: kLost,
	},

	// The user refuses the prompt, or nobody answers it.
	{
		name: "prompt-dismissed", style: stylePrompts, prompt: fakesecret.PromptDismiss, calls: []string{"Prompt"},
		lookup: kLockedDismissed, upsert: kDismissed, delete: kDismissed,
	},
	{
		name: "unlock-prompt-dismissed", style: styleRefuses, prompt: fakesecret.PromptDismiss, calls: []string{"Unlock", "Prompt"},
		lookup: kLockedDismissed, upsert: kLockedDismissed, delete: kLockedDismissed,
	},
	{
		name: "prompt-unanswered", style: stylePrompts, prompt: fakesecret.PromptStall, promptTimeout: 150 * time.Millisecond,
		calls:  []string{"Prompt", "Dismiss"},
		lookup: kLockedUnanswered, upsert: kUnanswered, delete: kUnanswered,
	},
	{
		name: "unlock-prompt-unanswered", style: styleRefuses, prompt: fakesecret.PromptStall, promptTimeout: 150 * time.Millisecond,
		calls:  []string{"Unlock", "Prompt", "Dismiss"},
		lookup: kLockedUnanswered, upsert: kLockedUnanswered, delete: kLockedUnanswered,
	},

	// The keyring keeps the item locked when it should not.
	{
		// An accepted unlock that leaves the item locked. The fake's
		// collection lock, which a write checks, is not the item's, so a
		// write never meets it.
		name: "lock-kept-after-unlock", style: styleRefuses, prompt: fakesecret.PromptAccept, calls: []string{"Unlock"},
		prepare: func(srv *fakesecret.Server, item dbus.ObjectPath) { srv.KeepLocked(item) },
		lookup:  kLocked, upsert: kNotReached, delete: kLocked,
	},
	{
		// Locked again by another program between the search and the read.
		name: "relocked-before-read", calls: []string{"GetSecrets"},
		prepare: func(srv *fakesecret.Server, item dbus.ObjectPath) { srv.RelockBeforeRead(item) },
		lookup:  kLocked, upsert: kNotReached, delete: kNotReached,
	},

	// The keyring answers Unlock with an error reply: its own is the lock
	// holding, one the bus sends in its place is not.
	{
		name: "unlock-error-reply-from-keyring", style: styleRefuses, failUnlock: ssErrIsLocked, calls: []string{"Unlock"},
		lookup: kLocked, upsert: kLocked, delete: kLocked,
	},
	{
		name: "unlock-error-reply-from-bus", style: styleRefuses, failUnlock: "org.freedesktop.DBus.Error.ServiceUnknown", calls: []string{"Unlock"},
		lookup: kLost, upsert: kLost, delete: kLost,
	},

	// The caller gives up.
	{
		name: "caller-cancelled-before-call", before: cancelCaller, cause: context.Canceled,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "caller-cancelled-under-prompt", style: stylePrompts, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: afterReply(cancelCaller), cause: context.Canceled,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "caller-cancelled-before-prompt-reply", style: stylePrompts, prompt: fakesecret.PromptHang,
		under: waitCall("Prompt", cancelCaller), calls: []string{"Prompt", "Dismiss"}, cause: context.Canceled,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
	{
		name: "caller-cancelled-under-unlock-prompt", style: styleRefuses, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: afterReply(cancelCaller), calls: []string{"Unlock"}, cause: context.Canceled,
		lookup: kLost, upsert: kLost, delete: kLost,
	},
}

// sweepOp is one of the operations the sweep runs against each fault.
type sweepOp struct {
	name string
	run  func(context.Context, *DBus) error
}

// TestDBus_FaultSweep runs Lookup, Upsert and Delete against every fault in
// faultTable and checks the kind of each answer. No step waits on the clock
// to line a fault up: each waits on the fake, whether for a call to arrive
// or for the bus to pass on a reply. The faults that are a wait running out
// shorten that wait.
func TestDBus_FaultSweep(t *testing.T) {
	id := IdentityFor("/tmp/a.toml", config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"})
	pw, err := NewPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	ops := []sweepOp{
		{"lookup", func(ctx context.Context, d *DBus) error { _, err := d.Lookup(ctx, id); return err }},
		{"upsert", func(ctx context.Context, d *DBus) error { return d.Upsert(ctx, id, pw) }},
		{"delete", func(ctx context.Context, d *DBus) error { return d.Delete(ctx, id) }},
	}
	for _, f := range faultTable {
		for op := range f.known {
			if !slices.ContainsFunc(ops, func(o sweepOp) bool { return o.name == op }) {
				t.Errorf("%s: known names %q, which is no operation", f.name, op)
			}
		}
	}
	for _, op := range ops {
		for _, f := range faultTable {
			t.Run(op.name+"/"+f.name, func(t *testing.T) {
				want := f.want(op.name)
				if want == kNotReached {
					t.Skip("this operation never makes the call the fault needs")
				}
				got, err := runFault(t, id, op, f)
				why, known := f.known[op.name]
				switch {
				case known && got == want:
					t.Fatalf("listed as a known failure (%s), but now gives %s: remove it from known", why, want)
				case known:
					t.Skipf("known failure: %s (gave %s, err = %v)", why, got, err)
				case got != want:
					t.Fatalf("%s gave %s (err = %v), the table says %s", op.name, got, err, want)
				}
			})
		}
	}
}

// runFault brings about f on a fake of its own, runs op through it, checks
// the fault was reached, and returns the kind of the answer.
func runFault(t *testing.T, id Identity, op sweepOp, f fault) (kind, error) {
	t.Helper()
	_, srv, stop := fakesecret.Start(t)
	defer stop()
	setUpFault(t, srv, id, f)
	ctx, cancel := context.WithCancel(bg)
	if f.deadline > 0 {
		ctx, cancel = context.WithTimeout(bg, f.deadline)
	}
	defer cancel()
	e := &env{srv: srv, cancel: cancel}
	if f.watchPrompt {
		e.replied = srv.WatchReply(t, "Prompt")
	}
	if f.before != nil {
		f.before(t, e)
	}

	errc := make(chan error, 1)
	go func() { errc <- op.run(ctx, NewDBus()) }()
	if f.under != nil {
		f.under(t, e)
	}
	var err error
	select {
	case err = <-errc:
	case <-time.After(5 * time.Second):
		t.Fatalf("still waiting after the fault; calls: %v", srv.Calls())
	}

	calls := srv.Calls()
	if f.silent && len(calls) > 0 {
		t.Fatalf("the fake saw %v, but the fault should come before any call", calls)
	}
	for _, c := range f.calls {
		if !slices.Contains(calls, c) {
			t.Fatalf("the fake never saw %s, so the fault was not reached; calls: %v", c, calls)
		}
	}
	if f.cause != nil && !errors.Is(err, f.cause) {
		t.Fatalf("err = %v, want it to wrap %v, the fault the row brings about", err, f.cause)
	}
	return kindOf(err), err
}

// setUpFault puts the fake in the state f starts from.
func setUpFault(t *testing.T, srv *fakesecret.Server, id Identity, f fault) {
	t.Helper()
	if f.promptTimeout > 0 {
		old := promptTimeout
		promptTimeout = f.promptTimeout
		t.Cleanup(func() { promptTimeout = old })
	}
	var item dbus.ObjectPath
	if !f.empty {
		item = srv.Seed(id.Attrs(), "s3cret", 100)
	}
	if f.style != styleOpen {
		srv.SetLocked(true)
	}
	if f.style == styleRefuses {
		srv.RefuseLockedWrites()
	}
	srv.SetPrompt(f.prompt)
	if f.hold != "" {
		srv.Hold(f.hold)
	}
	if f.failUnlock != "" {
		srv.FailUnlock(f.failUnlock)
	}
	if f.prepare != nil {
		f.prepare(srv, item)
	}
}
