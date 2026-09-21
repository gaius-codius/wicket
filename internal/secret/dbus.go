package secret

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	defer conn.Close()
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
	return fmt.Errorf("%w: %v", ErrUnavailable, err)
}

// dial connects to the session bus for one operation on ctx's behalf. Every
// call on the connection takes ctx itself, so it returns as soon as the
// caller gives up. The connection lasts dismissGrace longer, so a prompt can
// still be dismissed on the way out, and is then closed whatever it is doing:
// a wedged daemon cannot keep the goroutine behind a cancelled operation, or
// its socket, alive. close must be called when the operation is over.
func (d *DBus) dial(ctx context.Context) (conn *dbus.Conn, close func(), err error) {
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
	if len(locked) > 0 {
		var opened []dbus.ObjectPath
		var prompt dbus.ObjectPath
		if err := svc.CallWithContext(ctx, ssServiceIface+".Unlock", 0, locked).Store(&opened, &prompt); err == nil {
			// A locked keyring answers with a prompt path. Items that stay
			// locked simply yield no secret below, so a failed or dismissed
			// prompt is not fatal here -- unless the caller gave up, which
			// is not the same as the user saying no.
			if err := runPrompt(ctx, conn, prompt); err != nil && ctx.Err() != nil {
				return LookupResult{}, err
			}
		}
	}
	secrets := map[dbus.ObjectPath]ssSecret{}
	if err := svc.CallWithContext(ctx, ssServiceIface+".GetSecrets", 0, items, session).Store(&secrets); err != nil {
		return LookupResult{}, unavailable(ctx, err)
	}
	type hit struct {
		pw  Password
		mod uint64
	}
	var hits []hit
	for p, sec := range secrets {
		pw, err := NewPassword(string(sec.Value))
		// The transport buffer is ours and is zeroable, unlike the string
		// inside Password. Clear it as soon as it has been copied, including
		// for the matches we do not return.
		clear(sec.Value)
		if err != nil {
			continue
		}
		var mod uint64
		_ = conn.Object(ssBus, p).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, ssItemIface, "Modified").Store(&mod)
		hits = append(hits, hit{pw: pw, mod: mod})
	}
	if len(hits) == 0 {
		return LookupResult{}, ErrNotFound
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod > hits[j].mod })
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
	var item, prompt dbus.ObjectPath
	if err := conn.Object(ssBus, collPath).CallWithContext(ctx, ssCollectionIface+".CreateItem", 0, props, sec, true).Store(&item, &prompt); err != nil {
		return unavailable(ctx, err)
	}
	// Against a locked keyring the item is not written until the prompt is
	// answered. Returning nil here would tell the user the password was saved
	// when nothing was stored.
	return runPrompt(ctx, conn, prompt)
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
	items := append(unlocked, locked...)
	if len(items) == 0 {
		return ErrNotFound
	}
	var last error
	found := false
	for _, p := range items {
		var prompt dbus.ObjectPath
		if err := conn.Object(ssBus, p).CallWithContext(ctx, ssItemIface+".Delete", 0).Store(&prompt); err != nil {
			last = unavailable(ctx, err)
			continue
		}
		if err := runPrompt(ctx, conn, prompt); err != nil {
			last = err
			continue
		}
		found = true
	}
	if !found {
		if last != nil {
			return last
		}
		return ErrNotFound
	}
	return nil
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
	sigs := make(chan *dbus.Signal, 4)
	conn.Signal(sigs)
	defer conn.RemoveSignal(sigs)
	match := []dbus.MatchOption{
		dbus.WithMatchObjectPath(prompt),
		dbus.WithMatchInterface(ssPromptIface),
		dbus.WithMatchMember("Completed"),
	}
	if err := conn.AddMatchSignalContext(ctx, match...); err != nil {
		return unavailable(ctx, err)
	}
	defer func() { _ = conn.RemoveMatchSignal(match...) }()

	if call := conn.Object(ssBus, prompt).CallWithContext(ctx, ssPromptIface+".Prompt", 0, ""); call.Err != nil {
		return unavailable(ctx, call.Err)
	}
	dismiss := func() {
		// ctx may be over, so the dismissal gets a moment of its own; dial
		// keeps the connection open that long.
		dctx, cancel := context.WithTimeout(context.Background(), dismissGrace)
		defer cancel()
		_ = conn.Object(ssBus, prompt).CallWithContext(dctx, ssPromptIface+".Dismiss", 0).Err
	}
	deadline := time.NewTimer(promptTimeout)
	defer deadline.Stop()
	for {
		select {
		case sig := <-sigs:
			if sig == nil || sig.Path != prompt || sig.Name != ssPromptIface+".Completed" {
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
