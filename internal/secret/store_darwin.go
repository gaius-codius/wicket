//go:build darwin && cgo

package secret

// Default returns the platform secret store (macOS Keychain).
func Default() Store { return NewKeychain() }
