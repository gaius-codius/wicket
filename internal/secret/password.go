package secret

import (
	"encoding"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrCRLF        = errors.New("password must not contain CR or LF")
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

// NewPassword rejects CR/LF (REQ-007).
func NewPassword(s string) (Password, error) {
	if strings.ContainsAny(s, "\r\n") {
		return Password{}, ErrCRLF
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
