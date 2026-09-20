package config

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxPort = 65535
)

// FieldError is a validation failure for a single profile field.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string {
	if e == nil {
		return "field error"
	}
	if e.Field == "" {
		return e.Msg
	}
	return e.Field + ": " + e.Msg
}

// ValidateProfile checks REQ-005/009/014/015/021/023 rules for one profile.
// Uniqueness against siblings is NameTaken on the document, not here.
func ValidateProfile(p Profile) error {
	if err := validateName(p.Name); err != nil {
		return err
	}
	if err := validateHost(p.Host); err != nil {
		return err
	}
	if err := validateUser(p.User); err != nil {
		return err
	}
	if err := validateDomainUser(p.Domain, p.User); err != nil {
		return err
	}
	if err := validateClient(p.Client); err != nil {
		return err
	}
	if err := validateSize(p.Size); err != nil {
		return err
	}
	return validateScale(p.Scale)
}

// hasControl reports whether s holds a control character. These fields reach
// two places that cannot cope with one: the terminal, where an ESC would let a
// profile name rewrite the screen, and the FreeRDP command line, where a
// newline or tab produces an argument nobody typed.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

func validateName(name string) error {
	if name == "" {
		return &FieldError{Field: "name", Msg: "must not be empty"}
	}
	if hasControl(name) {
		return &FieldError{Field: "name", Msg: "must not contain control characters"}
	}
	if strings.TrimSpace(name) != name {
		return &FieldError{Field: "name", Msg: "must not have leading or trailing whitespace"}
	}
	if strings.HasPrefix(name, "-") {
		return &FieldError{Field: "name", Msg: "must not start with '-'"}
	}
	return nil
}

func validateHost(host string) error {
	h := strings.TrimSpace(host)
	if h == "" {
		return &FieldError{Field: "host", Msg: "must not be empty"}
	}
	if h != host {
		// REQ-015 is as typed after the caller trims; reject untrimmed on the stored value.
		return &FieldError{Field: "host", Msg: "must not have leading or trailing whitespace"}
	}
	if hasControl(h) {
		return &FieldError{Field: "host", Msg: "must not contain control characters"}
	}
	if strings.HasPrefix(h, "[") {
		end := strings.LastIndex(h, "]")
		if end <= 1 {
			return &FieldError{Field: "host", Msg: "IPv6 address must be written as [addr] or [addr]:port"}
		}
		inner := h[1:end]
		if inner == "" || strings.ContainsRune(inner, ']') {
			return &FieldError{Field: "host", Msg: "IPv6 address must be written as [addr] or [addr]:port"}
		}
		rest := h[end+1:]
		if rest == "" {
			return nil
		}
		if !strings.HasPrefix(rest, ":") {
			return &FieldError{Field: "host", Msg: "IPv6 with a port must be [addr]:port"}
		}
		if err := validatePort(rest[1:]); err != nil {
			return err
		}
		return nil
	}
	if strings.Count(h, ":") > 1 {
		return &FieldError{Field: "host", Msg: "IPv6 with a port must be bracketed ([::1]:3389)"}
	}
	if strings.Count(h, ":") == 1 {
		hostPart, port, _ := strings.Cut(h, ":")
		if hostPart == "" {
			return &FieldError{Field: "host", Msg: "host must not be empty"}
		}
		if err := validatePort(port); err != nil {
			return err
		}
	}
	return nil
}

func validatePort(port string) error {
	if port == "" {
		return &FieldError{Field: "host", Msg: "port must be a positive integer"}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > maxPort {
		return &FieldError{Field: "host", Msg: "port must be a number from 1 to 65535"}
	}
	return nil
}

func validateUser(user string) error {
	u := strings.TrimSpace(user)
	if u == "" {
		return &FieldError{Field: "user", Msg: "must not be empty"}
	}
	if u != user {
		return &FieldError{Field: "user", Msg: "must not have leading or trailing whitespace"}
	}
	if hasControl(u) {
		return &FieldError{Field: "user", Msg: "must not contain control characters"}
	}
	return nil
}

func validateDomainUser(domain, user string) error {
	d := strings.TrimSpace(domain)
	if d == "" {
		if domain != "" {
			return &FieldError{Field: "domain", Msg: "must not have leading or trailing whitespace"}
		}
		return nil
	}
	if d != domain {
		return &FieldError{Field: "domain", Msg: "must not have leading or trailing whitespace"}
	}
	if hasControl(d) {
		return &FieldError{Field: "domain", Msg: "must not contain control characters"}
	}
	if strings.ContainsAny(user, `\@`) {
		return &FieldError{Field: "user", Msg: `must not contain '\' or '@' when domain is set`}
	}
	return nil
}

func validateClient(client string) error {
	if client == "" {
		return &FieldError{Field: "client", Msg: "must not be empty"}
	}
	if hasControl(client) {
		return &FieldError{Field: "client", Msg: "must not contain control characters"}
	}
	if strings.ContainsRune(client, '/') || strings.ContainsFunc(client, unicode.IsSpace) {
		return &FieldError{Field: "client", Msg: "must be a basename (no '/' or whitespace)"}
	}
	return nil
}

func validateSize(size string) error {
	s := strings.TrimSpace(size)
	if s == "" {
		return nil
	}
	if hasControl(s) {
		return &FieldError{Field: "size", Msg: "must not contain control characters"}
	}
	if strings.HasSuffix(s, "%") {
		n := s[:len(s)-1]
		if !positiveInt(n) {
			return &FieldError{Field: "size", Msg: "use WIDTHxHEIGHT (e.g. 1920x1080), N% (e.g. 100%), or empty"}
		}
		return nil
	}
	lower := strings.ToLower(s)
	w, h, ok := strings.Cut(lower, "x")
	if !ok || !positiveInt(w) || !positiveInt(h) {
		return &FieldError{Field: "size", Msg: "use WIDTHxHEIGHT (e.g. 1920x1080), N% (e.g. 100%), or empty"}
	}
	return nil
}

func validateScale(scale int) error {
	switch scale {
	case 100, 140, 180:
		return nil
	default:
		return &FieldError{Field: "scale", Msg: "must be 100, 140, or 180"}
	}
}

func positiveInt(s string) bool {
	if s == "" {
		return false
	}
	// An explicit sign is not a size: Atoi accepts "+100", which would let
	// "+100%" through as 100.
	if s[0] == '+' || s[0] == '-' {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1
}

func fmtIndex(i int) string {
	return fmt.Sprintf("profiles[%d]", i)
}
