//go:build !linux && !darwin

package secret

// Default returns the D-Bus Secret Service store. Other non-Linux platforms
// have no dedicated backend yet; this keeps them compiling the way they did
// before Default existed.
func Default() Store { return NewDBus() }
