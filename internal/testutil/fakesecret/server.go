package fakesecret

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	busName         = "org.freedesktop.secrets"
	servicePath     = "/org/freedesktop/secrets"
	collectionPath  = "/org/freedesktop/secrets/collection/login"
	aliasPath       = "/org/freedesktop/secrets/aliases/default"
	serviceIface    = "org.freedesktop.Secret.Service"
	collectionIface = "org.freedesktop.Secret.Collection"
	itemIface       = "org.freedesktop.Secret.Item"
	promptPath      = "/org/freedesktop/secrets/prompt/p1"
	promptIface     = "org.freedesktop.Secret.Prompt"
	// maxItems bounds the item object paths exported up front. Exporting on
	// demand raced: CreateItem runs on a D-Bus dispatch goroutine, and
	// godbus mutates its handler map without a lock.
	maxItems = 64
)

// PromptMode controls whether writes need the user to answer a prompt first,
// which is what a locked keyring does.
type PromptMode int

const (
	// PromptNone completes writes immediately.
	PromptNone PromptMode = iota
	// PromptAccept defers the write until Prompt is called, then applies it.
	PromptAccept
	// PromptDismiss defers the write and then refuses it.
	PromptDismiss
	// PromptStall accepts the Prompt call and then never completes it, as a
	// dialog nobody answers does.
	PromptStall
)

type secretBlob struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type item struct {
	path     dbus.ObjectPath
	label    string
	attrs    map[string]string
	value    []byte
	created  int64
	modified int64
}

// Server is an in-process Secret Service on a private bus.
type Server struct {
	mu        sync.Mutex
	conn      *dbus.Conn
	items     map[dbus.ObjectPath]*item
	next      int
	session   dbus.ObjectPath
	prompt    PromptMode
	pending   func()
	locked    bool
	dismissed int
	calls     []string
	stall     bool
	stallAll  bool
	stop      chan struct{}
}

// Calls lists the Secret Service methods clients have invoked, in order, so a
// test can prove what a client did not do: open a session, unlock, prompt or
// fetch a secret.
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// StallSearches makes SearchItems hang until the server shuts down, as a
// wedged keyring daemon does, so a test can prove the client gives up.
func (s *Server) StallSearches() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stall = true
}

// StallEverything makes every Secret Service method hang until the server
// shuts down, so a test can prove that no keyring operation -- a write or a
// delete as well as a search -- can hold the client up for good.
func (s *Server) StallEverything() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stall = true
	s.stallAll = true
}

// record logs a call and, when StallEverything is on, holds it until the
// server shuts down.
func (s *Server) record(method string) {
	s.mu.Lock()
	s.calls = append(s.calls, method)
	stall := s.stallAll
	s.mu.Unlock()
	if stall {
		<-s.stop
	}
}

// SetLocked makes the collection answer searches with locked items, which
// yield no secret until Unlock has been through its prompt.
func (s *Server) SetLocked(locked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locked = locked
}

// Dismissed reports how many times a prompt was dismissed by the client,
// which is how a caller gives up on a dialog nobody answered.
func (s *Server) Dismissed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dismissed
}

// Seed stores an item directly, bypassing the replace-on-match that CreateItem
// does, so a test can build the duplicate-attribute state a real keyring
// reaches through two clients or a restored backup.
func (s *Server) Seed(attrs map[string]string, value string, modified int64) dbus.ObjectPath {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	p := dbus.ObjectPath(fmt.Sprintf("%s/%d", collectionPath, s.next))
	s.items[p] = &item{path: p, label: "wicket", attrs: attrs,
		value: []byte(value), created: modified, modified: modified}
	return p
}

// SetPrompt makes later writes go through a prompt, as a locked keyring does.
func (s *Server) SetPrompt(mode PromptMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompt = mode
}

// Stored reports how many items the collection holds.
func (s *Server) Stored() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func Start(t *testing.T) (addr string, srv *Server, cleanup func()) {
	t.Helper()
	userAddr := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	dir := t.TempDir()
	sock := filepath.Join(dir, "bus")
	addr = "unix:path=" + sock
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--nopidfile", "--address="+addr)
	if err := cmd.Start(); err != nil {
		t.Fatalf("dbus-daemon: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
	t.Cleanup(func() {
		if userAddr == "" {
			_ = os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")
		} else {
			_ = os.Setenv("DBUS_SESSION_BUS_ADDRESS", userAddr)
		}
	})
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == userAddr && userAddr != "" {
		t.Fatal("isolated bus address equals user session bus")
	}

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("connect fake bus: %v", err)
	}
	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		_ = conn.Close()
		_ = cmd.Process.Kill()
		t.Fatalf("request name: %v %v", err, reply)
	}
	srv = &Server{
		conn:    conn,
		items:   map[dbus.ObjectPath]*item{},
		session: "/org/freedesktop/secrets/session/s1",
		stop:    make(chan struct{}),
	}
	svc := &serviceObj{s: srv}
	coll := &collectionObj{s: srv}
	if err := conn.Export(svc, servicePath, serviceIface); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(coll, collectionPath, collectionIface); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(coll, aliasPath, collectionIface); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(&promptObj{s: srv}, promptPath, promptIface); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= maxItems; i++ {
		p := dbus.ObjectPath(fmt.Sprintf("%s/%d", collectionPath, i))
		obj := &itemObj{s: srv, path: p}
		if err := conn.Export(obj, p, itemIface); err != nil {
			t.Fatal(err)
		}
		if err := conn.Export(obj, p, "org.freedesktop.DBus.Properties"); err != nil {
			t.Fatal(err)
		}
	}
	cleanup = func() {
		close(srv.stop)
		_ = conn.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return addr, srv, cleanup
}

type serviceObj struct{ s *Server }
type collectionObj struct{ s *Server }

func (o *serviceObj) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	o.s.record("OpenSession")
	return o.s.OpenSession(algorithm, input)
}
func (o *serviceObj) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	o.s.record("SearchItems")
	o.s.mu.Lock()
	stall := o.s.stall
	o.s.mu.Unlock()
	if stall {
		<-o.s.stop
	}
	return o.s.SearchItems(attributes)
}
func (o *serviceObj) GetSecrets(items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]secretBlob, *dbus.Error) {
	o.s.record("GetSecrets")
	return o.s.GetSecrets(items, session)
}
func (o *serviceObj) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	o.s.record("ReadAlias")
	return o.s.ReadAlias(name)
}
func (o *serviceObj) Unlock(paths []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	o.s.record("Unlock")
	return o.s.Unlock(paths)
}
func (o *collectionObj) CreateItem(properties map[string]dbus.Variant, secret secretBlob, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	o.s.record("CreateItem")
	return o.s.CreateItem(properties, secret, replace)
}

type promptObj struct{ s *Server }

// Prompt answers the outstanding prompt and emits Completed, as the Secret
// Service does once the user has dealt with the dialog.
func (o *promptObj) Prompt(window string) *dbus.Error {
	o.s.record("Prompt")
	o.s.mu.Lock()
	mode, apply := o.s.prompt, o.s.pending
	if mode == PromptStall {
		// Leave pending in place: the dialog is still up.
		o.s.mu.Unlock()
		return nil
	}
	o.s.pending = nil
	if mode == PromptAccept && apply != nil {
		apply()
	}
	o.s.mu.Unlock()
	o.s.emitCompleted(mode == PromptDismiss)
	return nil
}

func (o *promptObj) Dismiss() *dbus.Error {
	o.s.record("Dismiss")
	o.s.mu.Lock()
	o.s.pending = nil
	o.s.dismissed++
	o.s.mu.Unlock()
	o.s.emitCompleted(true)
	return nil
}

func (s *Server) emitCompleted(dismissed bool) {
	_ = s.conn.Emit(promptPath, promptIface+".Completed", dismissed, dbus.MakeVariant(""))
}

func (o *collectionObj) Delete() (dbus.ObjectPath, *dbus.Error) {
	return "/", nil
}

func (s *Server) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	return dbus.MakeVariant(""), s.session, nil
}

func (s *Server) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var unlocked, locked []dbus.ObjectPath
	for _, it := range s.items {
		if !match(it.attrs, attributes) {
			continue
		}
		if s.locked {
			locked = append(locked, it.path)
			continue
		}
		unlocked = append(unlocked, it.path)
	}
	return unlocked, locked, nil
}

// Unlock opens the collection, through a prompt when one is configured. A
// dismissed prompt leaves everything locked, which is what the client sees as
// an item with no secret.
func (s *Server) Unlock(paths []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prompt != PromptNone {
		s.pending = func() { s.locked = false }
		return nil, promptPath, nil
	}
	s.locked = false
	return paths, "/", nil
}

func (s *Server) GetSecrets(items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]secretBlob, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[dbus.ObjectPath]secretBlob{}
	for _, p := range items {
		it, ok := s.items[p]
		if !ok || s.locked {
			continue
		}
		out[p] = secretBlob{Session: session, Value: it.value, ContentType: "text/plain"}
	}
	return out, nil
}

func (s *Server) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	if name == "default" {
		return collectionPath, nil
	}
	return "/", dbus.NewError("org.freedesktop.Secret.Error.NoSuchObject", nil)
}

func (s *Server) CreateItem(properties map[string]dbus.Variant, secret secretBlob, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attrs := map[string]string{}
	if v, ok := properties["org.freedesktop.Secret.Item.Attributes"]; ok {
		switch m := v.Value().(type) {
		case map[string]string:
			attrs = m
		case map[string]any:
			for k, val := range m {
				if s, ok := val.(string); ok {
					attrs[k] = s
				}
			}
		}
	}
	label := "wicket"
	if v, ok := properties["org.freedesktop.Secret.Item.Label"]; ok {
		if s, ok := v.Value().(string); ok {
			label = s
		}
	}
	if s.next >= maxItems {
		return "/", "/", dbus.NewError("org.freedesktop.Secret.Error.IsLocked", []any{"fake keyring is full"})
	}
	write := func() dbus.ObjectPath {
		if replace {
			for p, it := range s.items {
				if match(it.attrs, attrs) && len(attrs) > 0 {
					it.value = secret.Value
					it.modified = time.Now().Unix()
					it.label = label
					s.items[p] = it
					return p
				}
			}
		}
		s.next++
		p := dbus.ObjectPath(fmt.Sprintf("%s/%d", collectionPath, s.next))
		now := time.Now().Unix()
		it := &item{path: p, label: label, attrs: attrs, value: secret.Value, created: now, modified: now}
		s.items[p] = it
		return p
	}
	if s.prompt != PromptNone {
		s.pending = func() { write() }
		return "/", promptPath, nil
	}
	return write(), "/", nil
}

func (s *Server) Delete() (dbus.ObjectPath, *dbus.Error) {
	return "/", nil
}

type itemObj struct {
	s    *Server
	path dbus.ObjectPath
}

func (o *itemObj) GetSecret(session dbus.ObjectPath) (secretBlob, *dbus.Error) {
	o.s.record("GetSecret")
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	it, ok := o.s.items[o.path]
	if !ok {
		return secretBlob{}, dbus.NewError("org.freedesktop.Secret.Error.NoSuchObject", nil)
	}
	return secretBlob{Session: session, Value: it.value, ContentType: "text/plain"}, nil
}

// Get answers org.freedesktop.DBus.Properties.Get, which is how the client
// reads an item's Modified time to pick the newest of several matches.
func (o *itemObj) Get(iface, prop string) (dbus.Variant, *dbus.Error) {
	o.s.record("Get")
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	it, ok := o.s.items[o.path]
	if !ok {
		return dbus.MakeVariant(""), dbus.NewError("org.freedesktop.Secret.Error.NoSuchObject", nil)
	}
	switch prop {
	case "Modified":
		return dbus.MakeVariant(uint64(it.modified)), nil
	case "Created":
		return dbus.MakeVariant(uint64(it.created)), nil
	case "Label":
		return dbus.MakeVariant(it.label), nil
	case "Locked":
		return dbus.MakeVariant(o.s.locked), nil
	}
	return dbus.MakeVariant(""), dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)
}

func (o *itemObj) Delete() (dbus.ObjectPath, *dbus.Error) {
	o.s.record("Delete")
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	if o.s.prompt != PromptNone {
		path := o.path
		o.s.pending = func() { delete(o.s.items, path) }
		return promptPath, nil
	}
	delete(o.s.items, o.path)
	return "/", nil
}

func match(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}
