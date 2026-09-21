package secret

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LookupResult is a stored secret. Multiple is a warning: more than one item matched.
type LookupResult struct {
	Password Password
	Multiple bool
}

// Presence says whether a password is stored for an identity, as far as the
// keyring will tell without being unlocked.
type Presence int

const (
	NotSaved Presence = iota
	Saved
)

func (p Presence) String() string {
	if p == Saved {
		return "saved"
	}
	return "not saved"
}

// Store is the primitive secret API. Rename/identity transactions live in tui/actions.go.
type Store interface {
	Lookup(Identity) (LookupResult, error)
	Upsert(Identity, Password) error
	Delete(Identity) error
	// Presence checks for a stored password from metadata alone. It never
	// reads the secret and never prompts, so it may run on every selection
	// change; ctx bounds how long a slow keyring can hold it up. A keyring
	// that cannot answer yields an error wrapping ErrUnavailable.
	Presence(context.Context, Identity) (Presence, error)
}

type memItem struct {
	id      Identity
	secret  Password
	modTime time.Time
}

// Memory is an in-process store for tests.
type Memory struct {
	mu    sync.Mutex
	items []memItem
	clock func() time.Time
}

func NewMemory() *Memory {
	return &Memory{clock: time.Now}
}

func (m *Memory) Lookup(id Identity) (LookupResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var hits []memItem
	for _, it := range m.items {
		if it.id == id {
			hits = append(hits, it)
		}
	}
	if len(hits) == 0 {
		return LookupResult{}, ErrNotFound
	}
	best := hits[0]
	for _, it := range hits[1:] {
		if it.modTime.After(best.modTime) {
			best = it
		}
	}
	return LookupResult{Password: best.secret, Multiple: len(hits) > 1}, nil
}

func (m *Memory) Presence(ctx context.Context, id Identity) (Presence, error) {
	if err := ctx.Err(); err != nil {
		return NotSaved, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.id == id {
			return Saved, nil
		}
	}
	return NotSaved, nil
}

func (m *Memory) Upsert(id Identity, pw Password) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	for i, it := range m.items {
		if it.id == id {
			m.items[i].secret = pw
			m.items[i].modTime = now
			return nil
		}
	}
	m.items = append(m.items, memItem{id: id, secret: pw, modTime: now})
	return nil
}

func (m *Memory) Delete(id Identity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.items[:0]
	found := false
	for _, it := range m.items {
		if it.id == id {
			found = true
			continue
		}
		out = append(out, it)
	}
	m.items = out
	if !found {
		return ErrNotFound
	}
	return nil
}
