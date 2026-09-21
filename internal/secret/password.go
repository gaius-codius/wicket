package secret

import (
	"encoding"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxPasswordLen bounds a password so writing one to the client's stdin
// cannot block: the write happens before the child is known to be reading,
// and a value larger than the pipe buffer would deadlock the launch.
const maxPasswordLen = 4096

var (
	ErrCRLF        = errors.New("password must not contain CR or LF")
	ErrTooLong     = fmt.Errorf("password must be at most %d bytes", maxPasswordLen)
	ErrNotFound    = errors.New("secret not found")
	ErrUnavailable = errors.New("secret service unavailable")
)

const redacted = "********"

// Password is a secret value that cannot be printed or marshaled.
type Password struct {
	v string
}

var (
	_ fmt.Stringer           = Password{}
	_ fmt.GoStringer         = Password{}
	_ encoding.TextMarshaler = Password{}
)

// NewPassword rejects CR/LF (REQ-007) and anything too long to hand to the
// client in one write.
func NewPassword(s string) (Password, error) {
	if strings.ContainsAny(s, "\r\n") {
		return Password{}, ErrCRLF
	}
	if len(s) > maxPasswordLen {
		return Password{}, ErrTooLong
	}
	return Password{v: s}, nil
}

func (p Password) String() string   { return redacted }
func (p Password) GoString() string { return "secret.Password{}" }

func (p Password) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, redacted)
}

func (p Password) MarshalText() ([]byte, error) {
	return nil, errors.New("password must not be marshaled")
}

// WriteLine writes the secret plus a single newline, then the caller must close the pipe.
func (p Password) WriteLine(w io.Writer) error {
	_, err := io.WriteString(w, p.v+"\n")
	return err
}

// Clear zeroes the in-struct string by replacing it. Go strings are immutable;
// this drops the Password's reference (SEC-003 / §4: no zeroing of Go strings).
func (p *Password) Clear() {
	if p == nil {
		return
	}
	p.v = ""
}

// Empty reports whether any secret was stored on this value.
func (p Password) Empty() bool { return p.v == "" }

// OccursIn reports whether the password appears in s, so text from outside
// Wicket -- a client's log, say -- can be kept off the screen if it repeats
// the password. An empty password occurs nowhere.
func (p Password) OccursIn(s string) bool {
	return p.v != "" && strings.Contains(s, p.v)
}
