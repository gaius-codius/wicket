package secret

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	ssBus             = "org.freedesktop.secrets"
	ssServicePath     = "/org/freedesktop/secrets"
	ssServiceIface    = "org.freedesktop.Secret.Service"
	ssCollectionIface = "org.freedesktop.Secret.Collection"
	ssItemIface       = "org.freedesktop.Secret.Item"
	ssDefaultAlias    = "default"
	ssItemAttrs       = "org.freedesktop.Secret.Item.Attributes"
	ssItemLabel       = "org.freedesktop.Secret.Item.Label"
	ssPromptIface     = "org.freedesktop.Secret.Prompt"
	ssErrIsLocked     = "org.freedesktop.Secret.Error.IsLocked"
)

// promptTimeout bounds the wait for the user to answer a keyring prompt.
var promptTimeout = promptTimeout0

// OpTimeout is how long a caller should let one Lookup, Upsert or Delete run
// before giving up on the keyring. It is long rather than short on purpose: an
// operation may be waiting on a person, not on the daemon -- an unlock dialog,
// or a keyring such as KeePassXC that holds a search open until its database
// is unlocked -- and cutting that off after a few seconds would make a locked
// keyring unusable. It outlasts promptTimeout, so an unanswered prompt is
// dismissed and reported as such rather than cut off by the caller. A caller
// that must stay responsive, like the TUI, runs the operation off its main
// loop and lets the user cancel it well before this.
const OpTimeout = promptTimeout0 + 30*time.Second

// promptTimeout0 is promptTimeout's default, a constant so OpTimeout can be.
const promptTimeout0 = 2 * time.Minute

// dismissGrace is how long a connection outlives the operation that opened
// it once the caller has given up, so that a prompt the operation raised can
// still be dismissed rather than left on screen.
const dismissGrace = time.Second

var (
	// ErrPromptDismissed means the user refused a keyring prompt.
	ErrPromptDismissed = errors.New("the keyring prompt was dismissed")
	// ErrPromptTimeout means nobody answered a keyring prompt in time.
	ErrPromptTimeout = errors.New("timed out waiting for the keyring prompt")
	// ErrLocked means the keyring stayed locked: a lookup found a stored
	// password it could not read, or a write or delete the keyring refused
	// while locked could not unlock it. On a lookup another match may have
	// been readable, but the locked one may be newer, so none is returned.
	// It wraps why the unlock failed, usually ErrPromptDismissed, and that
	// cause wraps ErrUnavailable.
	ErrLocked = errors.New("the keyring is locked")

	// errBusLost means the session bus connection closed under a prompt. It
	// is the bus that failed, not the unlock, so it is never ErrLocked.
	errBusLost = errors.New("lost the session bus connection")
)

type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// DBus is a libsecret adapter over org.freedesktop.secrets.
type DBus struct {
	connect func(...dbus.ConnOption) (*dbus.Conn, error)
}

func NewDBus() *DBus {
	return &DBus{connect: dbus.ConnectSessionBus}
}

// Presence reports whether an item matches id without reading it. It calls
// SearchItems and nothing else: no session, no Unlock, no GetSecrets. That is
// what makes it safe to run while the list is on screen -- a locked keyring
// would otherwise pop an unlock dialog just because the cursor moved. A locked
// match counts as saved, because the password is there; only reading it needs
// the user.
func (d *DBus) Presence(ctx context.Context, id Identity) (Presence, error) {
	if err := ctx.Err(); err != nil {
		return NotSaved, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	// Tying the connection to ctx closes it when the caller gives up, so a
	// wedged bus cannot keep the goroutine behind a timed-out check alive.
	conn, err := d.connect(dbus.WithContext(ctx))
	if err != nil {
		return NotSaved, unavailable(ctx, err)
	}
	defer func() { _ = conn.Close() }()
	svc := conn.Object(ssBus, ssServicePath)
	var unlocked, locked []dbus.ObjectPath
	if err := svc.CallWithContext(ctx, ssServiceIface+".SearchItems", 0, id.Attrs()).Store(&unlocked, &locked); err != nil {
		return NotSaved, unavailable(ctx, err)
	}
	if len(unlocked)+len(locked) == 0 {
		return NotSaved, nil
	}
	return Saved, nil
}

// unavailable wraps a failed keyring call. When the caller's deadline or
// cancellation is the cause it says so, rather than passing on whatever the
// torn-down connection happened to report.
func unavailable(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, ctxErr)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// dial connects to the session bus for one operation on ctx's behalf. Every
// call on the connection takes ctx itself, so it returns as soon as the
// caller gives up. The connection lasts dismissGrace longer, so a prompt can
// still be dismissed on the way out, and is then closed whatever it is doing:
// a wedged daemon cannot keep the goroutine behind a cancelled operation, or
// its socket, alive. done must be called when the operation is over.
func (d *DBus) dial(ctx context.Context) (conn *dbus.Conn, done func(), err error) {
	connCtx, closeConn := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, func() { time.AfterFunc(dismissGrace, closeConn) })
	conn, err = d.connect(dbus.WithContext(connCtx))
	if err != nil {
		stop()
		closeConn()
		return nil, nil, unavailable(ctx, err)
	}
	return conn, func() {
		stop()
		closeConn()
		_ = conn.Close()
	}, nil
}

func (d *DBus) Lookup(ctx context.Context, id Identity) (LookupResult, error) {
	conn, done, err := d.dial(ctx)
	if err != nil {
		return LookupResult{}, err
	}
	defer done()
	svc := conn.Object(ssBus, ssServicePath)
	var unlocked, locked []dbus.ObjectPath
	if err := svc.CallWithContext(ctx, ssServiceIface+".SearchItems", 0, id.Attrs()).Store(&unlocked, &locked); err != nil {
		return LookupResult{}, unavailable(ctx, err)
	}
	items := append(append([]dbus.ObjectPath{}, unlocked...), locked...)
	if len(items) == 0 {
		return LookupResult{}, ErrNotFound
	}
	session, err := openSession(ctx, svc)
	if err != nil {
		return LookupResult{}, err
	}
	// A locked keyring answers Unlock with a prompt path. If the prompt is
	// refused or fails, the lookup is ErrLocked even when another match is
	// readable: the locked one may be the newer, and reading around it would
	// send a stale password with no word of why (issue #80). Nor is it "not
	// found", which would send the user off to save one into the same locked
	// keyring. A caller that gave up is told so instead, which is not the
	// same as the user saying no. Stopping here also keeps locked items out
	// of GetSecrets, which some backends fail outright, hiding the lock
	// behind a generic "unavailable".
	if len(locked) > 0 {
		if err := unlock(ctx, conn, svc, locked); err != nil {
			return LookupResult{}, err
		}
	}
	secrets := map[dbus.ObjectPath]ssSecret{}
	if err := svc.CallWithContext(ctx, ssServiceIface+".GetSecrets", 0, items, session).Store(&secrets); err != nil {
		return LookupResult{}, unavailable(ctx, err)
	}
	// GetSecrets leaves out any match it cannot read, without a word. An
	// unlock can complete and still leave one locked (a backend may let the
	// user open some items and not others), and a match that was open at
	// SearchItems can be locked again before GetSecrets (Seahorse,
	// secret-tool lock, KeePassXC's auto-lock). Either way the older match
	// that is left must not stand in for it. A match deleted in between
	// lands here too and could have been read around, but nothing tells the
	// two apart, and calling it locked costs only a retry.
	for _, p := range items {
		if _, ok := secrets[p]; !ok {
			for _, sec := range secrets {
				clear(sec.Value)
			}
			return LookupResult{}, fmt.Errorf("%w (%w: a match could not be read)", ErrLocked, ErrUnavailable)
		}
	}
	// A match whose password Wicket cannot use stays in the running: the
	// newest item is the one the user saved last, and falling back to an
	// older one would send a stale password with no word of why. Its error
	// is returned instead, which is not the same as "not found" either.
	type hit struct {
		pw  Password
		err error
		mod uint64
	}
	var hits []hit
	for p, sec := range secrets {
		pw, err := NewPassword(string(sec.Value))
		// The transport buffer is ours and is zeroable, unlike the string
		// inside Password. Clear it as soon as it has been copied, including
		// for the matches we do not return.
		clear(sec.Value)
		var mod uint64
		_ = conn.Object(ssBus, p).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, ssItemIface, "Modified").Store(&mod)
		hits = append(hits, hit{pw: pw, err: err, mod: mod})
	}
	if len(hits) == 0 {
		return LookupResult{}, ErrNotFound
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod > hits[j].mod })
	if hits[0].err != nil {
		return LookupResult{}, hits[0].err
	}
	return LookupResult{Password: hits[0].pw, Multiple: len(hits) > 1}, nil
}

func (d *DBus) Upsert(ctx context.Context, id Identity, pw Password) error {
	conn, done, err := d.dial(ctx)
	if err != nil {
		return err
	}
	defer done()
	svc := conn.Object(ssBus, ssServicePath)
	session, err := openSession(ctx, svc)
	if err != nil {
		return err
	}
	collPath, err := defaultCollection(ctx, svc)
	if err != nil {
		return err
	}
	props := map[string]dbus.Variant{
		ssItemLabel: dbus.MakeVariant(id.Label()),
		ssItemAttrs: dbus.MakeVariant(id.Attrs()),
	}
	sec := ssSecret{Session: session, Value: []byte(pw.v), ContentType: "text/plain"}
	defer clear(sec.Value)
	create := func() (prompt dbus.ObjectPath, err error) {
		var item dbus.ObjectPath
		err = conn.Object(ssBus, collPath).CallWithContext(ctx, ssCollectionIface+".CreateItem", 0, props, sec, true).Store(&item, &prompt)
		return prompt, err
	}
	prompt, err := create()
	// KeePassXC answers a write to a locked collection with a prompt, but
	// gnome-keyring refuses it with IsLocked: the client has to unlock the
	// collection and ask again, as libsecret does.
	if isLockedErr(err) {
		if err := unlock(ctx, conn, svc, []dbus.ObjectPath{collPath}); err != nil {
			return err
		}
		prompt, err = create()
	}
	if err != nil {
		return writeFailed(ctx, err)
	}
	// Against a locked keyring the item is not written until the prompt is
	// answered. Returning nil here would tell the user the password was saved
	// when nothing was stored.
	return runPrompt(ctx, conn, prompt)
}

// isLockedErr reports whether a keyring call was refused because its target
// is locked.
func isLockedErr(err error) bool {
	var e dbus.Error
	return errors.As(err, &e) && e.Name == ssErrIsLocked
}

// writeFailed wraps a failed CreateItem or Item.Delete. One still refused as
// locked after an unlock that went through is ErrLocked: the backend kept
// the lock, which is not the keyring being out of reach.
func writeFailed(ctx context.Context, err error) error {
	if isLockedErr(err) && ctx.Err() == nil {
		return fmt.Errorf("%w (%w)", ErrLocked, unavailable(ctx, err))
	}
	return unavailable(ctx, err)
}

// unlock opens paths, through the keyring's prompt when it raises one. A
// refused, unanswered or failed unlock is ErrLocked, wrapping why. A caller
// that gave up is told so instead, which is not the same as the user saying
// no, and so is a lost bus or keyring daemon (an error the bus itself
// sent), which says nothing about the lock.
func unlock(ctx context.Context, conn *dbus.Conn, svc dbus.BusObject, paths []dbus.ObjectPath) error {
	var opened []dbus.ObjectPath
	var prompt dbus.ObjectPath
	err := svc.CallWithContext(ctx, ssServiceIface+".Unlock", 0, paths).Store(&opened, &prompt)
	if err != nil {
		err = unavailable(ctx, err)
	} else {
		err = runPrompt(ctx, conn, prompt)
	}
	if err == nil {
		return nil
	}
	// Only the keyring's own answer says the lock held: the prompt refused
	// or left unanswered, or an error reply from the daemon. Anything else
	// is the transport (the bus dropped mid-call, or under the prompt), and
	// says nothing about the lock.
	if ctx.Err() == nil && (errors.Is(err, ErrPromptDismissed) || errors.Is(err, ErrPromptTimeout) || isKeyringReply(err)) {
		return fmt.Errorf("%w (%w)", ErrLocked, err)
	}
	return err
}

// busErrPrefix names the errors of D-Bus itself. The bus sends them in the
// keyring's place: NoReply when the daemon disconnects under a call,
// ServiceUnknown or NameHasNoOwner when it is gone.
const busErrPrefix = "org.freedesktop.DBus.Error."

// isKeyringReply reports whether err is an error reply from the keyring
// rather than one the bus generated. godbus does not say who sent a reply,
// so the name decides. A keyring can send a D-Bus name too (godbus answers
// Failed for a handler's plain error, UnknownObject for a path a restarted
// daemon never had), but none of them says the user kept it locked.
func isKeyringReply(err error) bool {
	var reply dbus.Error
	return errors.As(err, &reply) && !strings.HasPrefix(reply.Name, busErrPrefix)
}

func (d *DBus) Delete(ctx context.Context, id Identity) error {
	conn, done, err := d.dial(ctx)
	if err != nil {
		return err
	}
	defer done()
	svc := conn.Object(ssBus, ssServicePath)
	var unlocked, locked []dbus.ObjectPath
	if err := svc.CallWithContext(ctx, ssServiceIface+".SearchItems", 0, id.Attrs()).Store(&unlocked, &locked); err != nil {
		return unavailable(ctx, err)
	}
	items := slices.Concat(unlocked, locked)
	if len(items) == 0 {
		return ErrNotFound
	}
	// Every match must go. One left behind -- its unlock refused while
	// another was open -- is still a stored password, and reporting success
	// would have the TUI say it was forgotten.
	var last, refused error
	for _, p := range items {
		del := func() (prompt dbus.ObjectPath, err error) {
			err = conn.Object(ssBus, p).CallWithContext(ctx, ssItemIface+".Delete", 0).Store(&prompt)
			return prompt, err
		}
		prompt, err := del()
		// gnome-keyring refuses to delete a locked item rather than
		// prompting: unlock it and ask again, as Upsert does. A user who
		// refused one unlock is not asked again for each duplicate.
		if isLockedErr(err) {
			if refused != nil {
				last = refused
				continue
			}
			if err := unlock(ctx, conn, svc, []dbus.ObjectPath{p}); err != nil {
				if errors.Is(err, ErrLocked) {
					refused = err
				}
				last = err
				continue
			}
			prompt, err = del()
		}
		if err != nil {
			last = writeFailed(ctx, err)
			continue
		}
		if err := runPrompt(ctx, conn, prompt); err != nil {
			last = err
		}
	}
	return last
}

// runPrompt completes a Secret Service prompt. The service hands back a prompt
// path whenever it needs the user before it will do the work; a caller that
// ignores it reports success for an operation that never happened. A prompt
// nobody answers, or one the caller stops waiting for, is dismissed so no
// dialog is left behind.
func runPrompt(ctx context.Context, conn *dbus.Conn, prompt dbus.ObjectPath) error {
	if prompt == "" || prompt == "/" {
		return nil
	}
	// Completed decides whether a write went through, so only the keyring
	// may send it: any process of the same user can emit a signal on any
	// path. The bus delivers a signal addressed to us whatever our match
	// rules say, so the sender is checked here as well as in the rule.
	var owner string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, ssBus).Store(&owner); err != nil {
		return unavailable(ctx, err)
	}
	sigs := make(chan *dbus.Signal, 4)
	conn.Signal(sigs)
	defer conn.RemoveSignal(sigs)
	match := []dbus.MatchOption{
		dbus.WithMatchSender(ssBus),
		dbus.WithMatchObjectPath(prompt),
		dbus.WithMatchInterface(ssPromptIface),
		dbus.WithMatchMember("Completed"),
	}
	if err := conn.AddMatchSignalContext(ctx, match...); err != nil {
		return unavailable(ctx, err)
	}
	defer func() { _ = conn.RemoveMatchSignal(match...) }()

	dismiss := func() {
		// ctx may be over, so the dismissal gets a moment of its own; dial
		// keeps the connection open that long. It goes to the daemon that
		// showed the dialog: sent to the well-known name after that daemon
		// has gone, it would start a new one to dismiss a prompt it never
		// had.
		dctx, cancel := context.WithTimeout(context.Background(), dismissGrace)
		defer cancel()
		_ = conn.Object(owner, prompt).CallWithContext(dctx, ssPromptIface+".Dismiss", 0).Err
	}
	if call := conn.Object(ssBus, prompt).CallWithContext(ctx, ssPromptIface+".Prompt", 0, ""); call.Err != nil {
		// The service shows the dialog when it receives Prompt, before it
		// replies, so a caller that gave up while the reply was on its way
		// leaves the dialog on screen unless it is dismissed here. After a
		// lost bus or a lost daemon the dismissal fails at once.
		dismiss()
		return unavailable(ctx, call.Err)
	}
	deadline := time.NewTimer(promptTimeout)
	defer deadline.Stop()
	for {
		select {
		case sig, ok := <-sigs:
			if !ok {
				// godbus closes the channel when the connection drops, and a
				// closed channel would otherwise spin here until the timeout
				// and then be blamed on the user.
				return unavailable(ctx, errBusLost)
			}
			if sig.Sender != owner || sig.Path != prompt || sig.Name != ssPromptIface+".Completed" {
				continue
			}
			if len(sig.Body) > 0 {
				if dismissed, ok := sig.Body[0].(bool); ok && dismissed {
					return fmt.Errorf("%w: %w", ErrUnavailable, ErrPromptDismissed)
				}
			}
			return nil
		case <-deadline.C:
			dismiss()
			return fmt.Errorf("%w: %w", ErrUnavailable, ErrPromptTimeout)
		case <-ctx.Done():
			dismiss()
			return fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
		}
	}
}

func openSession(ctx context.Context, svc dbus.BusObject) (dbus.ObjectPath, error) {
	var out dbus.Variant
	var session dbus.ObjectPath
	if err := svc.CallWithContext(ctx, ssServiceIface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &session); err != nil {
		return "", unavailable(ctx, err)
	}
	return session, nil
}

func defaultCollection(ctx context.Context, svc dbus.BusObject) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	if err := svc.CallWithContext(ctx, ssServiceIface+".ReadAlias", 0, ssDefaultAlias).Store(&path); err == nil && path != "" && path != "/" {
		return path, nil
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return "/org/freedesktop/secrets/collection/login", nil
}
