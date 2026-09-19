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
	mu      sync.Mutex
	conn    *dbus.Conn
	items   map[dbus.ObjectPath]*item
	next    int
	session dbus.ObjectPath
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
	cleanup = func() {
		_ = conn.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return addr, srv, cleanup
}

type serviceObj struct{ s *Server }
type collectionObj struct{ s *Server }

func (o *serviceObj) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	return o.s.OpenSession(algorithm, input)
}
func (o *serviceObj) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	return o.s.SearchItems(attributes)
}
func (o *serviceObj) GetSecrets(items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]secretBlob, *dbus.Error) {
	return o.s.GetSecrets(items, session)
}
func (o *serviceObj) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	return o.s.ReadAlias(name)
}
func (o *collectionObj) CreateItem(properties map[string]dbus.Variant, secret secretBlob, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	return o.s.CreateItem(properties, secret, replace)
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
	var unlocked []dbus.ObjectPath
	for _, it := range s.items {
		if match(it.attrs, attributes) {
			unlocked = append(unlocked, it.path)
		}
	}
	return unlocked, nil, nil
}

func (s *Server) GetSecrets(items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]secretBlob, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[dbus.ObjectPath]secretBlob{}
	for _, p := range items {
		it, ok := s.items[p]
		if !ok {
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
	if replace {
		for p, it := range s.items {
			if match(it.attrs, attrs) && len(attrs) > 0 {
				it.value = secret.Value
				it.modified = time.Now().Unix()
				it.label = label
				s.items[p] = it
				return p, "/", nil
			}
		}
	}
	s.next++
	p := dbus.ObjectPath(fmt.Sprintf("%s/%d", collectionPath, s.next))
	now := time.Now().Unix()
	it := &item{path: p, label: label, attrs: attrs, value: secret.Value, created: now, modified: now}
	s.items[p] = it
	_ = s.conn.Export(&itemObj{s: s, path: p}, p, itemIface)
	return p, "/", nil
}

func (s *Server) Delete() (dbus.ObjectPath, *dbus.Error) {
	return "/", nil
}

type itemObj struct {
	s    *Server
	path dbus.ObjectPath
}

func (o *itemObj) GetSecret(session dbus.ObjectPath) (secretBlob, *dbus.Error) {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	it, ok := o.s.items[o.path]
	if !ok {
		return secretBlob{}, dbus.NewError("org.freedesktop.Secret.Error.NoSuchObject", nil)
	}
	return secretBlob{Session: session, Value: it.value, ContentType: "text/plain"}, nil
}

func (o *itemObj) Delete() (dbus.ObjectPath, *dbus.Error) {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
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
