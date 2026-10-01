//go:build linux

package secret

// Default returns the platform secret store (Secret Service via D-Bus).
func Default() Store { return NewDBus() }
