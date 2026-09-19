package secret

import (
	"sync"
	"time"
)

// LookupResult is a stored secret. Multiple is a warning: more than one item matched.
type LookupResult struct {
	Password Password
	Multiple bool
}

// Store is the primitive secret API. Rename/identity transactions live in tui/actions.go.
type Store interface {
	Lookup(Identity) (LookupResult, error)
	Upsert(Identity, Password) error
	Delete(Identity) error
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
