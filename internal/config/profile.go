package config

import (
	"crypto/rand"
	"fmt"
	"reflect"
	"slices"
)

const (
	DefaultClient            = "sdl-freerdp3"
	DefaultScale             = 100
	DefaultDynamicResolution = true
	DefaultFullscreen        = false
	// FreeRDP 3 shares the clipboard unless told not to, so on is what a
	// profile written before the setting existed has always had.
	DefaultClipboard = true
)

// Share is one local folder offered to the remote session as a named drive.
type Share struct {
	// Path is absolute, or "~" / "~/…". It is expanded when connecting and
	// when checking that the folder exists.
	Path string
	// Name is the FreeRDP share name. Empty means derive one from Path's
	// base name when connecting.
	Name string
}

// Profile is a v1 user-editable connection (REQ-005).
type Profile struct {
	// ID is a stable opaque UUID for keyring identity (issue #24). It is
	// assigned when a profile is created or when a legacy config without id
	// is loaded, and does not change when the display name or account fields
	// are edited.
	ID                string
	Name              string
	Host              string
	User              string
	Domain            string
	Client            string
	Size              string
	Fullscreen        bool
	DynamicResolution bool
	Scale             int
	// Multimon spans the session across every monitor. Wicket does that only
	// full screen, so it implies Fullscreen: a window across several
	// monitors is not something the form offers.
	Multimon  bool
	Clipboard bool
	// ShareHome offers the whole local home folder, read-write, to the
	// remote machine as a drive.
	ShareHome bool
	// Shares are specific local folders offered as named drives. Empty means
	// none beyond ShareHome.
	Shares []Share
}

// DefaultProfile is the field set a new profile starts with in the TUI
// and on import: scale 100, dynamic resolution and clipboard on, and the
// compiled-in default client (overridden at import time with the first
// FreeRDP client found on PATH).
func DefaultProfile() Profile {
	return Profile{
		Client:            DefaultClient,
		DynamicResolution: DefaultDynamicResolution,
		Fullscreen:        DefaultFullscreen,
		Scale:             DefaultScale,
		Clipboard:         DefaultClipboard,
	}
}

// Clone is p with its own share list. A plain assignment still shares
// Shares, so an edit of one copy would change the other.
func (p Profile) Clone() Profile {
	p.Shares = slices.Clone(p.Shares)
	return p
}

// Equal reports whether p and o hold the same fields. Profile contains a
// slice, so callers cannot use ==.
func (p Profile) Equal(o Profile) bool {
	return reflect.DeepEqual(p, o)
}

// newProfileID returns a fresh profile id. Tests may replace it for
// deterministic TOML output.
var newProfileID = randomProfileID

func randomProfileID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A process that cannot read entropy cannot safely mint ids; panic
		// rather than risk colliding keyring identities.
		panic("config: random profile id: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// EnsureID sets p.ID to a new UUID when it is empty. Callers must use it
// before secret.IdentityFor so every keyring lookup has a stable id.
func EnsureID(p *Profile) {
	if p == nil || p.ID != "" {
		return
	}
	p.ID = newProfileID()
}

// FreshID returns a copy of p with a newly assigned ID, for duplicate and
// import paths that must not keep the source profile's keyring identity.
func FreshID(p Profile) Profile {
	p.ID = newProfileID()
	return p
}
