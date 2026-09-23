package config

const (
	DefaultClient            = "sdl-freerdp3"
	DefaultScale             = 100
	DefaultDynamicResolution = true
	DefaultFullscreen        = false
	// FreeRDP 3 shares the clipboard unless told not to, so on is what a
	// profile written before the setting existed has always had.
	DefaultClipboard = true
)

// Profile is a v1 user-editable connection (REQ-005).
type Profile struct {
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
}

func defaultProfile() Profile {
	return Profile{
		Client:            DefaultClient,
		DynamicResolution: DefaultDynamicResolution,
		Fullscreen:        DefaultFullscreen,
		Scale:             DefaultScale,
		Clipboard:         DefaultClipboard,
	}
}
