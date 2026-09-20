package secret

import (
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
var promptTimeout = 2 * time.Minute

type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// DBus is a libsecret adapter over org.freedesktop.secrets.
type DBus struct {
	connect func() (*dbus.Conn, error)
}

func NewDBus() *DBus {
	return &DBus{connect: func() (*dbus.Conn, error) { return dbus.ConnectSessionBus() }}
}

func (d *DBus) Lookup(id Identity) (LookupResult, error) {
	conn, err := d.connect()
	if err != nil {
		return LookupResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	svc := conn.Object(ssBus, ssServicePath)
	var unlocked, locked []dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".SearchItems", 0, id.Attrs()).Store(&unlocked, &locked); err != nil {
		return LookupResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	items := append(append([]dbus.ObjectPath{}, unlocked...), locked...)
	if len(items) == 0 {
		return LookupResult{}, ErrNotFound
	}
	session, err := openSession(svc)
	if err != nil {
		return LookupResult{}, err
	}
	if len(locked) > 0 {
		var opened []dbus.ObjectPath
		var prompt dbus.ObjectPath
		if err := svc.Call(ssServiceIface+".Unlock", 0, locked).Store(&opened, &prompt); err == nil {
			// A locked keyring answers with a prompt path. Items that stay
			// locked simply yield no secret below, so a failed or dismissed
			// prompt is not fatal here.
			_ = runPrompt(conn, prompt)
		}
	}
	secrets := map[dbus.ObjectPath]ssSecret{}
	if err := svc.Call(ssServiceIface+".GetSecrets", 0, items, session).Store(&secrets); err != nil {
		return LookupResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
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
		_ = conn.Object(ssBus, p).Call("org.freedesktop.DBus.Properties.Get", 0, ssItemIface, "Modified").Store(&mod)
		hits = append(hits, hit{pw: pw, mod: mod})
	}
	if len(hits) == 0 {
		return LookupResult{}, ErrNotFound
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod > hits[j].mod })
	return LookupResult{Password: hits[0].pw, Multiple: len(hits) > 1}, nil
}

func (d *DBus) Upsert(id Identity, pw Password) error {
	conn, err := d.connect()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	svc := conn.Object(ssBus, ssServicePath)
	session, err := openSession(svc)
	if err != nil {
		return err
	}
	collPath, err := defaultCollection(svc)
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
	if err := conn.Object(ssBus, collPath).Call(ssCollectionIface+".CreateItem", 0, props, sec, true).Store(&item, &prompt); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// Against a locked keyring the item is not written until the prompt is
	// answered. Returning nil here would tell the user the password was saved
	// when nothing was stored.
	return runPrompt(conn, prompt)
}

func (d *DBus) Delete(id Identity) error {
	conn, err := d.connect()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	svc := conn.Object(ssBus, ssServicePath)
	var unlocked, locked []dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".SearchItems", 0, id.Attrs()).Store(&unlocked, &locked); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	items := append(unlocked, locked...)
	if len(items) == 0 {
		return ErrNotFound
	}
	var last error
	found := false
	for _, p := range items {
		var prompt dbus.ObjectPath
		if err := conn.Object(ssBus, p).Call(ssItemIface+".Delete", 0).Store(&prompt); err != nil {
			last = err
			continue
		}
		if err := runPrompt(conn, prompt); err != nil {
			last = err
			continue
		}
		found = true
	}
	if !found {
		if last != nil {
			return fmt.Errorf("%w: %v", ErrUnavailable, last)
		}
		return ErrNotFound
	}
	return nil
}

// runPrompt completes a Secret Service prompt. The service hands back a prompt
// path whenever it needs the user before it will do the work; a caller that
// ignores it reports success for an operation that never happened.
func runPrompt(conn *dbus.Conn, prompt dbus.ObjectPath) error {
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
	if err := conn.AddMatchSignal(match...); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = conn.RemoveMatchSignal(match...) }()

	if call := conn.Object(ssBus, prompt).Call(ssPromptIface+".Prompt", 0, ""); call.Err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, call.Err)
	}
	deadline := time.After(promptTimeout)
	for {
		select {
		case sig := <-sigs:
			if sig == nil || sig.Path != prompt || sig.Name != ssPromptIface+".Completed" {
				continue
			}
			if len(sig.Body) > 0 {
				if dismissed, ok := sig.Body[0].(bool); ok && dismissed {
					return fmt.Errorf("%w: the keyring prompt was dismissed", ErrUnavailable)
				}
			}
			return nil
		case <-deadline:
			_ = conn.Object(ssBus, prompt).Call(ssPromptIface+".Dismiss", 0).Err
			return fmt.Errorf("%w: timed out waiting for the keyring prompt", ErrUnavailable)
		}
	}
}

func openSession(svc dbus.BusObject) (dbus.ObjectPath, error) {
	var out dbus.Variant
	var session dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &session); err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return session, nil
}

func defaultCollection(svc dbus.BusObject) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".ReadAlias", 0, ssDefaultAlias).Store(&path); err == nil && path != "" && path != "/" {
		return path, nil
	}
	return "/org/freedesktop/secrets/collection/login", nil
}
