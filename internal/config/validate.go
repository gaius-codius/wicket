package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxPort = 65535
	// maxDimension is the widest or tallest /size FreeRDP takes: it reads
	// each side into a UINT16 and refuses the connection over it.
	maxDimension = 65535
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
	if err := validateScale(p.Scale); err != nil {
		return err
	}
	return validateSharesShape(p)
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

// validateUser allows an empty user: an imported file often has none (an
// Azure Virtual Desktop download, a shared team file), and the profile must
// still load so the front end can ask for one. Saving and connecting need one; see
// ErrNoUser.
func validateUser(user string) error {
	if user == "" {
		return nil
	}
	u := strings.TrimSpace(user)
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
			return &FieldError{Field: "size", Msg: "use dimension (1920x1080), N% (e.g. 100%), or empty (FreeRDP chooses)"}
		}
		return nil
	}
	lower := strings.ToLower(s)
	w, h, ok := strings.Cut(lower, "x")
	if !ok || !positiveInt(w) || !positiveInt(h) {
		return &FieldError{Field: "size", Msg: "use dimension (1920x1080), N% (e.g. 100%), or empty (FreeRDP chooses)"}
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

// ValidateProfileInUse holds a profile to rules a loaded config is not held
// to: the ones that decide whether Wicket will act on it, rather than whether
// it can read it. Saving and connecting both ask, so a host Wicket refuses to
// save is also one it refuses to launch, with a reason in its own words
// rather than FreeRDP's. A config that already holds one still opens, or a
// single bad profile would lock the user out of every other.
func ValidateProfileInUse(p Profile) error {
	if err := ValidateProfile(p); err != nil {
		return err
	}
	if p.User == "" {
		return ErrNoUser
	}
	// No host name or address has a space in it, and FreeRDP would get
	// "/v:bad host" as one argument and fail on it at connect time.
	if strings.ContainsFunc(strings.TrimSpace(p.Host), unicode.IsSpace) {
		return &FieldError{Field: "host", Msg: "must not contain spaces"}
	}
	if err := validateSizeInRange(p.Size); err != nil {
		return err
	}
	return validateSharesExist(p)
}

// ErrNoUser is a profile with no user, as an import may leave one, asked to
// save or connect. FreeRDP would otherwise be launched with "/u:" and sign
// in as nobody, or prompt on a terminal Wicket owns.
var ErrNoUser = &FieldError{Field: "user", Msg: "must not be empty"}

// validateSizeInRange refuses a width or height FreeRDP would reject. It is
// held to save and connect, not load: a hand-edited "70000x1080" used to be
// accepted, and the config holding it must still open. ValidateProfile has
// already checked the shape, so only the numbers are left to judge.
func validateSizeInRange(size string) error {
	w, h, ok := strings.Cut(strings.ToLower(strings.TrimSpace(size)), "x")
	if !ok {
		return nil
	}
	for _, d := range []string{w, h} {
		if n, err := strconv.Atoi(d); err == nil && n > maxDimension {
			return &FieldError{Field: "size", Msg: "width and height must be at most 65535"}
		}
	}
	return nil
}

// validateImported is what AddProfiles holds a profile to: the same as a
// save, except a shared folder that is not on disk yet is kept. Import
// writes a config that would still load; connect will refuse until the
// folder is there.
func validateImported(p Profile) error {
	if err := ValidateProfile(p); err != nil {
		return err
	}
	if strings.ContainsFunc(strings.TrimSpace(p.Host), unicode.IsSpace) {
		return &FieldError{Field: "host", Msg: "must not contain spaces"}
	}
	return validateSizeInRange(p.Size)
}

// dropUnimportableShares keeps shares a loaded config would accept, and
// drops the rest, so one bad Remmina folder does not skip the connection.
// Each dropped share comes back with its reason, for the import summary.
// Names that are almost valid are cleaned the way ShareNameFromPath is.
func dropUnimportableShares(p Profile) (Profile, []DroppedShare) {
	if len(p.Shares) == 0 {
		return p, nil
	}
	var dropped []DroppedShare
	kept := make([]Share, 0, len(p.Shares))
	// seen maps each kept drive name to its folder. Remmina can list one
	// folder bare and again under its own base name; that second entry is
	// the share already kept, so it goes without a note rather than one
	// saying a folder the summary lists was dropped.
	seen := map[string]string{}
	for _, s := range p.Shares {
		s.Path = strings.TrimSpace(s.Path)
		s.Name = strings.TrimSpace(s.Name)
		if s.Name != "" && s.Name != sanitizeShareName(s.Name) {
			if cleaned := sanitizeShareName(s.Name); cleaned == "share" {
				s.Name = ""
			} else {
				s.Name = cleaned
			}
		}
		drive, msg := checkShareShape(s, p.ShareHome)
		key := strings.ToLower(drive)
		if prev, ok := seen[key]; msg == "" && ok {
			if expanded, _ := ExpandPath(s.Path); expanded == prev {
				continue
			}
			msg = fmt.Sprintf("duplicate share name %q", drive)
		}
		if msg != "" {
			dropped = append(dropped, DroppedShare{Path: s.Path, Reason: msg})
			continue
		}
		seen[key], _ = ExpandPath(s.Path)
		kept = append(kept, s)
	}
	p.Shares = kept
	return p, dropped
}

// validateSharesShape checks share paths and names without touching the
// filesystem, so a config still opens when a shared folder has gone.
func validateSharesShape(p Profile) error {
	seen := map[string]bool{}
	for i, s := range p.Shares {
		drive, msg := checkShareShape(s, p.ShareHome)
		if msg != "" {
			return &FieldError{Field: "shares", Msg: fmt.Sprintf("entry %d: %s", i+1, msg)}
		}
		key := strings.ToLower(drive)
		if seen[key] {
			return &FieldError{Field: "shares", Msg: fmt.Sprintf("duplicate share name %q", drive)}
		}
		seen[key] = true
	}
	return nil
}

// checkShareShape is why one share would not load on its own, or "" when it
// would, and the drive name it is shared as. Duplicate drive names are left
// to the caller, which knows the shares before it. It names no entry number,
// so an import can report a dropped share by its path.
func checkShareShape(s Share, shareHome bool) (drive, msg string) {
	path := strings.TrimSpace(s.Path)
	if path == "" {
		return "", "path must not be empty"
	}
	if hasControl(path) {
		return "", "path must not contain control characters"
	}
	if strings.ContainsRune(path, ',') {
		return "", "path must not contain ','"
	}
	expanded, err := ExpandPath(path)
	if err != nil {
		return "", err.Error()
	}
	if !filepath.IsAbs(expanded) {
		return "", "path must be absolute or ~/…"
	}
	name := strings.TrimSpace(s.Name)
	if name != "" {
		if hasControl(name) {
			return "", "name must not contain control characters"
		}
		if strings.ContainsAny(name, ",/") || strings.Contains(name, string(filepath.Separator)) {
			return "", "name must not contain ',' or '/'"
		}
		if name != sanitizeShareName(name) {
			return "", "name must be letters, digits, '_' or '-'"
		}
	}
	drive = name
	if drive == "" {
		drive = ShareNameFromPath(expanded)
	}
	if shareHome && strings.EqualFold(drive, "home") {
		return "", fmt.Sprintf("name %q clashes with share_home", drive)
	}
	return drive, ""
}

// validateSharesExist requires each share path to name an existing directory
// after ~ expansion, and one ShareRefusal allows, as it is when asked: a
// share an import kept may since have become a link to home. Save and
// connect ask; load does not.
func validateSharesExist(p Profile) error {
	if err := validateSharesShape(p); err != nil {
		return err
	}
	for i, s := range p.Shares {
		expanded, err := ExpandPath(strings.TrimSpace(s.Path))
		if err != nil {
			return &FieldError{Field: "shares", Msg: fmt.Sprintf("entry %d: %v", i+1, err)}
		}
		info, err := os.Stat(expanded)
		if err != nil {
			if os.IsNotExist(err) {
				return &FieldError{Field: "shares", Msg: fmt.Sprintf("entry %d: %s is not a directory", i+1, expanded)}
			}
			return &FieldError{Field: "shares", Msg: fmt.Sprintf("entry %d: %v", i+1, err)}
		}
		if !info.IsDir() {
			return &FieldError{Field: "shares", Msg: fmt.Sprintf("entry %d: %s is not a directory", i+1, expanded)}
		}
		if reason := ShareRefusal(s.Path); reason != "" {
			return &FieldError{Field: "shares", Msg: fmt.Sprintf("entry %d: %s %s", i+1, expanded, reason)}
		}
	}
	return nil
}
