package fakesecret

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Sandbox(m)) }

// The fake's bus must never start a service for a name the fake does not
// own. The stock session config would start whatever activation file the
// machine has for org.freedesktop.secrets -- the real keyring daemon, on the
// developer's real keyring -- once a test had lost the fake daemon (issue
// #162). Here an activation file is planted where the stock config looks;
// the bus must answer ServiceUnknown and leave it unstarted.
func TestStartActivatesNoService(t *testing.T) {
	data := t.TempDir()
	marker := filepath.Join(data, "started")
	script := filepath.Join(data, "keyring.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	services := filepath.Join(data, "dbus-1", "services")
	if err := os.MkdirAll(services, 0o700); err != nil {
		t.Fatal(err)
	}
	file := "[D-BUS Service]\nName=" + busName + "\nExec=" + script + "\n"
	if err := os.WriteFile(filepath.Join(services, busName+".service"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", data)

	_, srv, cleanup := Start(t)
	defer cleanup()
	srv.Disconnect()

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// The bus drops the fake's name once it sees the connection close.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var has bool
		if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, busName).Store(&has); err != nil {
			t.Fatal(err)
		}
		if !has {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bus still has an owner for the Secret Service name")
		}
		time.Sleep(5 * time.Millisecond)
	}

	err = conn.Object(busName, servicePath).Call(serviceIface+".ReadAlias", 0, "default").Err
	var reply dbus.Error
	if !errors.As(err, &reply) || reply.Name != "org.freedesktop.DBus.Error.ServiceUnknown" {
		t.Fatalf("call to the lost name: err = %v, want ServiceUnknown", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the bus started the planted keyring service")
	}
}
