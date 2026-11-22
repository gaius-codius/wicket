package secret

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/testutil/fakesecret"
)

// kind is the answer an operation gives, in the four ways a caller tells
// apart (plus success): the keyring was locked, it was out of reach, the
// password is not there, or it worked.
type kind string

const (
	kSuccess     kind = "success"
	kLocked      kind = "ErrLocked"
	kUnavailable kind = "ErrUnavailable only"
	kNotFound    kind = "ErrNotFound"
)

// kindOf classifies err. A result that is more than one kind at once, or
// none of them, gets a name of its own so the table cannot match it.
func kindOf(err error) kind {
	locked := errors.Is(err, ErrLocked)
	notFound := errors.Is(err, ErrNotFound)
	unavailable := errors.Is(err, ErrUnavailable)
	switch {
	case err == nil:
		return kSuccess
	case locked && notFound:
		return "ErrLocked and ErrNotFound"
	case locked:
		return kLocked
	case notFound && unavailable:
		return "ErrNotFound and ErrUnavailable"
	case notFound:
		return kNotFound
	case unavailable:
		return kUnavailable
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
	srv *fakesecret.Server
	// stopBus takes the whole session bus down.
	stopBus func()
	cancel  context.CancelFunc
	// replied closes once the bus has passed on the fake's reply to Prompt.
	// It is set only for a fault with watchPrompt set.
	replied <-chan struct{}
}

// fault is one way the keyring goes wrong, with how to bring it about and
// what each operation must answer.
type fault struct {
	name  string
	style style
	// empty leaves nothing stored; otherwise a password is.
	empty  bool
	prompt fakesecret.PromptMode
	// hold is a method the fake never answers.
	hold string
	// failUnlock is the error name Unlock answers with.
	failUnlock string
	// promptTimeout shortens the wait for an answer, for a fault that is
	// that wait running out.
	promptTimeout time.Duration
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

	lookup, upsert, delete kind
}

func (f fault) want(op string) kind {
	switch op {
	case "lookup":
		return f.lookup
	case "upsert":
		return f.upsert
	}
	return f.delete
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

// loseAfterReply is a fault's step for a daemon that goes once its dialog is
// up: it waits for the reply to Prompt and then runs lose.
func loseAfterReply(lose func(*testing.T, *env)) func(*testing.T, *env) {
	return func(t *testing.T, e *env) {
		t.Helper()
		waitReplied(t, e)
		lose(t, e)
	}
}

func disconnect(_ *testing.T, e *env) { e.srv.Disconnect() }

func dropName(t *testing.T, e *env) {
	t.Helper()
	if err := e.srv.DropName(ssBus); err != nil {
		t.Fatal(err)
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

func stopBus(_ *testing.T, e *env)      { e.stopBus() }
func cancelCaller(_ *testing.T, e *env) { e.cancel() }

// disconnectAndWait loses the daemon and waits until the bus knows it is gone.
func disconnectAndWait(t *testing.T, e *env) {
	t.Helper()
	e.srv.Disconnect()
	waitNameGone(t)
}

// cancelAfterReply gives up on a dialog that is certainly on screen.
func cancelAfterReply(t *testing.T, e *env) {
	t.Helper()
	waitReplied(t, e)
	e.cancel()
}

// faultTable is the contract: for every fault the fake Secret Service can
// produce, what each operation must answer. TestDBus_FaultSweep runs every
// row against Lookup, Upsert and Delete and fails on any other kind.
//
// Two lost keyrings were once reported as a locked one, each found by
// reading the code. A fault the fake learns to produce must get a row here,
// so that every operation is checked against it, not only the one a test was
// written for. An answer that looks wrong is not a reason to change the row
// to match: fix the code, or mark the row known (see knownFailures).
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

	// The session bus is lost.
	{
		name: "bus-lost-before-call", silent: true, before: stopBus,
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "bus-lost-under-prompt", style: stylePrompts, prompt: fakesecret.PromptStall,
		under: waitCall("Prompt", stopBus), calls: []string{"Prompt"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "bus-lost-during-prompt-call", style: stylePrompts, prompt: fakesecret.PromptHang,
		under: waitCall("Prompt", stopBus), calls: []string{"Prompt"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "bus-lost-under-unlock-prompt", style: styleRefuses, prompt: fakesecret.PromptStall,
		under: waitCall("Prompt", stopBus), calls: []string{"Unlock", "Prompt"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},

	// The keyring daemon is lost while the bus stays up.
	{
		name: "daemon-lost-before-call", silent: true,
		before: disconnectAndWait,
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "daemon-lost-during-unlock", style: styleRefuses, prompt: fakesecret.PromptStall, hold: "Unlock",
		under: waitCall("Unlock", disconnect), calls: []string{"Unlock"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "daemon-lost-during-prompt-call", style: stylePrompts, prompt: fakesecret.PromptStall, hold: "Prompt",
		under: waitCall("Prompt", disconnect), calls: []string{"Prompt"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "daemon-lost-during-unlock-prompt-call", style: styleRefuses, prompt: fakesecret.PromptStall, hold: "Prompt",
		under: waitCall("Prompt", disconnect), calls: []string{"Unlock", "Prompt"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "daemon-lost-after-prompt-reply", style: stylePrompts, prompt: fakesecret.PromptStall, watchPrompt: true,
		under:  loseAfterReply(disconnect),
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "daemon-lost-after-unlock-prompt-reply", style: styleRefuses, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: loseAfterReply(disconnect), calls: []string{"Unlock"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "daemon-replaced-after-prompt-reply", style: stylePrompts, prompt: fakesecret.PromptStall, watchPrompt: true,
		under:  loseAfterReply(dropName),
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "daemon-replaced-after-unlock-prompt-reply", style: styleRefuses, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: loseAfterReply(dropName), calls: []string{"Unlock"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},

	// The user refuses the prompt, or nobody answers it.
	{
		name: "prompt-dismissed", style: stylePrompts, prompt: fakesecret.PromptDismiss, calls: []string{"Prompt"},
		lookup: kLocked, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "unlock-prompt-dismissed", style: styleRefuses, prompt: fakesecret.PromptDismiss, calls: []string{"Unlock", "Prompt"},
		lookup: kLocked, upsert: kLocked, delete: kLocked,
	},
	{
		name: "prompt-unanswered", style: stylePrompts, prompt: fakesecret.PromptStall, promptTimeout: 150 * time.Millisecond,
		calls:  []string{"Prompt", "Dismiss"},
		lookup: kLocked, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "unlock-prompt-unanswered", style: styleRefuses, prompt: fakesecret.PromptStall, promptTimeout: 150 * time.Millisecond,
		calls:  []string{"Unlock", "Prompt", "Dismiss"},
		lookup: kLocked, upsert: kLocked, delete: kLocked,
	},

	// The keyring answers Unlock with an error reply: its own is the lock
	// holding, one the bus sends in its place is not.
	{
		name: "unlock-error-reply-from-keyring", style: styleRefuses, failUnlock: ssErrIsLocked, calls: []string{"Unlock"},
		lookup: kLocked, upsert: kLocked, delete: kLocked,
	},
	{
		name: "unlock-error-reply-from-bus", style: styleRefuses, failUnlock: "org.freedesktop.DBus.Error.ServiceUnknown", calls: []string{"Unlock"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},

	// The caller gives up.
	{
		name: "caller-cancelled-before-call", before: func(_ *testing.T, e *env) { e.cancel() },
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "caller-cancelled-under-prompt", style: stylePrompts, prompt: fakesecret.PromptStall, watchPrompt: true,
		under:  cancelAfterReply,
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "caller-cancelled-before-prompt-reply", style: stylePrompts, prompt: fakesecret.PromptHang,
		under: waitCall("Prompt", cancelCaller), calls: []string{"Prompt", "Dismiss"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
	{
		name: "caller-cancelled-under-unlock-prompt", style: styleRefuses, prompt: fakesecret.PromptStall, watchPrompt: true,
		under: cancelAfterReply, calls: []string{"Unlock"},
		lookup: kUnavailable, upsert: kUnavailable, delete: kUnavailable,
	},
}

// knownFailures names a row and operation, as "fault/op", whose answer is
// wrong in the code and not yet fixed, with what is wrong, so the sweep
// skips it and says why instead of failing. A row must not stay here after
// its fix. There are none now.
var knownFailures = map[string]string{}

// TestDBus_FaultSweep runs Lookup, Upsert and Delete against every fault
// the fake keyring can produce and checks the kind of each answer against
// faultTable. No step waits on the clock: each waits on the fake, whether
// for a call to arrive or for the bus to pass on a reply, and the one fault
// that is a wait running out has its timeout shortened.
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
	for _, op := range ops {
		for _, f := range faultTable {
			name := op.name + "/" + f.name
			t.Run(name, func(t *testing.T) {
				if why, ok := knownFailures[name]; ok {
					t.Skipf("known failure: %s", why)
				}
				runFault(t, id, op, f)
			})
		}
	}
}

// sweepOp is one of the operations the sweep runs against each fault.
type sweepOp struct {
	name string
	run  func(context.Context, *DBus) error
}

// runFault brings about f on a fake of its own, runs op through it, and
// checks the fault was reached and the answer is the table's.
func runFault(t *testing.T, id Identity, op sweepOp, f fault) {
	t.Helper()
	_, srv, stop := fakesecret.Start(t)
	stopOnce := sync.OnceFunc(stop)
	defer stopOnce()
	setUpFault(t, srv, id, f)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	e := &env{srv: srv, stopBus: stopOnce, cancel: cancel}
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
	if got, want := kindOf(err), f.want(op.name); got != want {
		t.Fatalf("%s gave %s (err = %v), the table says %s", op.name, got, err, want)
	}
}

// setUpFault puts the fake in the state f starts from.
func setUpFault(t *testing.T, srv *fakesecret.Server, id Identity, f fault) {
	t.Helper()
	if f.promptTimeout > 0 {
		old := promptTimeout
		promptTimeout = f.promptTimeout
		t.Cleanup(func() { promptTimeout = old })
	}
	if !f.empty {
		srv.Seed(id.Attrs(), "s3cret", 100)
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
}
