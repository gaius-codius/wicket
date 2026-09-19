package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/sys/unix"
)

const (
	stateTable = "last_used"
	lockSuffix = ".lock"
)

// Clock provides timestamps for last_used.
type Clock interface {
	Now() time.Time
}

type sysClock struct{}

func (sysClock) Now() time.Time { return time.Now() }

// StateStore is the sibling last_used file (REQ-018, DATA-008, SEC-001).
type StateStore struct {
	path  string
	clock Clock
}

// OpenState returns a store for the given path. The file is not created until the first write.
func OpenState(path string, clock Clock) (*StateStore, error) {
	if clock == nil {
		clock = sysClock{}
	}
	canon, err := Canonical(path, "")
	if err != nil {
		return nil, err
	}
	return &StateStore{path: canon, clock: clock}, nil
}

func (s *StateStore) Path() string { return s.path }

// LastUsed returns the timestamp for name. Missing, unparseable, or garbage → absent.
func (s *StateStore) LastUsed(name string) (time.Time, bool) {
	m, err := s.readMerged(false)
	if err != nil {
		return time.Time{}, false
	}
	t, ok := m[name]
	return t, ok
}

// Record sets last_used for name to now.
func (s *StateStore) Record(name string) error {
	return s.mutate(func(m map[string]time.Time) {
		m[name] = s.clock.Now().Truncate(time.Second)
	})
}

// Rename moves a last_used key. No-op if the old key is absent.
func (s *StateStore) Rename(old, new string) error {
	if old == new {
		return nil
	}
	return s.mutate(func(m map[string]time.Time) {
		t, ok := m[old]
		if !ok {
			return
		}
		delete(m, old)
		m[new] = t
	})
}

// Forget removes a last_used key. No-op if absent.
func (s *StateStore) Forget(name string) error {
	return s.mutate(func(m map[string]time.Time) {
		delete(m, name)
	})
}

func (s *StateStore) mutate(fn func(map[string]time.Time)) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	m, err := s.readMerged(true)
	if err != nil {
		return err
	}
	fn(m)
	return s.writeLocked(m)
}

// readMerged loads [last_used]. unparseable: if failOnGarbage, return error (writes must not clobber);
// otherwise treat as empty (reads display "never").
func (s *StateStore) readMerged(failOnGarbage bool) (map[string]time.Time, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]time.Time{}, nil
		}
		if failOnGarbage {
			return nil, err
		}
		return map[string]time.Time{}, nil
	}
	m, err := parseState(data)
	if err != nil {
		if failOnGarbage {
			return nil, fmt.Errorf("state file is unparseable; refusing to overwrite: %w", err)
		}
		return map[string]time.Time{}, nil
	}
	return m, nil
}

func parseState(data []byte) (map[string]time.Time, error) {
	raw := map[string]any{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, err
	}
	table, ok := raw[stateTable]
	if !ok {
		return map[string]time.Time{}, nil
	}
	tm, ok := table.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("[%s] must be a table", stateTable)
	}
	out := map[string]time.Time{}
	for k, v := range tm {
		t, ok := parseTimestamp(v)
		if !ok {
			continue
		}
		out[k] = t
	}
	return out, nil
}

func parseTimestamp(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		parsed, err := time.Parse(time.RFC3339, t)
		if err != nil {
			return time.Time{}, false
		}
		return parsed, true
	default:
		return time.Time{}, false
	}
}

func (s *StateStore) writeLocked(m map[string]time.Time) error {
	encoded := map[string]any{stateTable: timestampsTOML(m)}
	var buf []byte
	var err error
	buf, err = tomlEncode(encoded)
	if err != nil {
		return err
	}
	return atomicWrite(s.path, buf)
}

func timestampsTOML(m map[string]time.Time) map[string]any {
	out := make(map[string]any, len(m))
	for k, t := range m {
		out[k] = t.Format(time.RFC3339)
	}
	return out
}

func tomlEncode(v any) ([]byte, error) {
	var buf []byte
	w := &byteWriter{}
	if err := toml.NewEncoder(w).Encode(v); err != nil {
		return nil, err
	}
	buf = w.b
	return buf, nil
}

type byteWriter struct{ b []byte }

func (w *byteWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

func (s *StateStore) lock() (func(), error) {
	lockPath := s.path + lockSuffix
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("state lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("state lock: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
