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
