//go:build darwin && !cgo

package secret

import "context"

// Keychain is a stub when cgo is disabled: Security.framework is unavailable.
type Keychain struct{}

// NewKeychain returns a store that reports ErrUnavailable without cgo.
func NewKeychain() *Keychain { return &Keychain{} }

// Default returns the stub Keychain so darwin/!cgo builds stay compileable.
func Default() Store { return NewKeychain() }

func (k *Keychain) Lookup(context.Context, Identity) (LookupResult, error) {
	return LookupResult{}, ErrUnavailable
}

func (k *Keychain) Upsert(context.Context, Identity, Password) error {
	return ErrUnavailable
}

func (k *Keychain) Delete(context.Context, Identity) error {
	return ErrUnavailable
}

func (k *Keychain) Presence(context.Context, Identity) (Presence, error) {
	return NotSaved, ErrUnavailable
}
