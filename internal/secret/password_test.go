package secret

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewPassword_RejectsCRLF(t *testing.T) {
	t.Parallel()
	if _, err := NewPassword("ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPassword("a\nb"); err != ErrCRLF {
		t.Fatalf("lf: %v", err)
	}
	if _, err := NewPassword("a\rb"); err != ErrCRLF {
		t.Fatalf("cr: %v", err)
	}
}

func TestPassword_Redacted(t *testing.T) {
	t.Parallel()
	pw, err := NewPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if pw.String() == "hunter2" || pw.GoString() == "hunter2" {
		t.Fatal("string leaked")
	}
	if s := fmt.Sprintf("%v %s %#v", pw, pw, pw); strings.Contains(s, "hunter2") {
		t.Fatalf("format leaked: %s", s)
	}
	if _, err := pw.MarshalText(); err == nil {
		t.Fatal("marshal")
	}
}

// A password is written to the client's stdin before the child is known to be
// reading, so it has to stay well inside the pipe buffer.
func TestNewPassword_RejectsOneTooLongToWriteInOneGo(t *testing.T) {
	if _, err := NewPassword(strings.Repeat("a", maxPasswordLen)); err != nil {
		t.Fatalf("a password of exactly the limit was rejected: %v", err)
	}
	if _, err := NewPassword(strings.Repeat("a", maxPasswordLen+1)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v, want ErrTooLong", err)
	}
	if maxPasswordLen >= 65536 {
		t.Fatalf("limit %d is not below the 64 KiB pipe buffer", maxPasswordLen)
	}
}

// OccursIn finds the password as a cleaning would leave it, not only as it
// was stored, and a cleaning that leaves nothing matches nothing.
func TestPassword_OccursInCleanedForms(t *testing.T) {
	strip := func(s string) string { return strings.ReplaceAll(s, "\x1b[31m", "") }
	p, err := NewPassword("abc\x1b[31mdef")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		s     string
		forms []func(string) string
		want  bool
	}{
		{"x abc\x1b[31mdef y", nil, true},
		{"x abcdef y", nil, false},
		{"x abcdef y", []func(string) string{strip}, true},
		{"x abc y", []func(string) string{strip}, false},
	} {
		if got := p.OccursIn(c.s, c.forms...); got != c.want {
			t.Errorf("OccursIn(%q, %d forms) = %v, want %v", c.s, len(c.forms), got, c.want)
		}
	}
	all, _ := NewPassword("\x1b[31m")
	if all.OccursIn("anything", strip) {
		t.Error("a password its cleaning erases matched everything")
	}
	if (Password{}).OccursIn("anything", strip) {
		t.Error("an empty password occurred")
	}
}
