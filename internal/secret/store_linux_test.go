//go:build linux

package secret

import "testing"

func TestDefault_IsDBus(t *testing.T) {
	if _, ok := Default().(*DBus); !ok {
		t.Fatalf("Default() = %T, want *DBus", Default())
	}
}
