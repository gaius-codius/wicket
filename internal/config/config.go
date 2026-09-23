package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const starterTOML = "[general]\n"

// Config is an open wicket config file with a typed view and a raw document.
type Config struct {
	path     string
	doc      *document
	profiles []Profile
	// rewrote is set when the last save could not patch the file and wrote
	// it out in full, losing its comments and formatting.
	rewrote bool
}

// Rewrote reports whether the last save rewrote the file in full rather than
// editing it in place, so comments and formatting in it were lost.
func (c *Config) Rewrote() bool { return c.rewrote }

// Path is the canonical config path.
func (c *Config) Path() string { return c.path }

// Profiles returns profiles in file order.
func (c *Config) Profiles() []Profile {
	out := make([]Profile, len(c.profiles))
	copy(out, c.profiles)
	return out
}

// Profile returns a named profile.
func (c *Config) Profile(name string) (Profile, bool) {
	for _, p := range c.profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// Warnings are non-fatal load notes (stripped password-family keys).
func (c *Config) Warnings() []string {
	if c.doc == nil {
		return nil
	}
	out := make([]string, len(c.doc.warnings))
	copy(out, c.doc.warnings)
	return out
}

// NameTaken reports whether name is already used by another profile.
// except is the current name on edit (empty when adding).
func (c *Config) NameTaken(name, except string) bool {
	for _, p := range c.profiles {
		if p.Name == name && p.Name != except {
			return true
		}
	}
	return false
}

// Open reads an existing config file. It never creates or rewrites the file.
func Open(path string) (*Config, error) {
	canon, err := Canonical(path, "")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(canon)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, loadError(canon, err)
	}
	return parseConfig(canon, data)
}

// OpenOrCreate is TUI bootstrap: create a starter only when the file is missing.
func OpenOrCreate(path string) (*Config, error) {
	c, err := Open(path)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	canon, err := Canonical(path, "")
	if err != nil {
		return nil, err
	}
	if err := atomicWrite(canon, []byte(starterTOML)); err != nil {
		return nil, err
	}
	return Open(canon)
}

func parseConfig(path string, data []byte) (*Config, error) {
	doc, err := parseDocument(data)
	if err != nil {
		return nil, loadError(path, err)
	}
	profiles, err := doc.typedProfiles()
	if err != nil {
		return nil, loadError(path, err)
	}
	return &Config{path: path, doc: doc, profiles: profiles}, nil
}

// Upsert validates p, inserts or replaces the profile named except (empty = add), and saves.
func (c *Config) Upsert(p Profile, except string) error {
	if err := ValidateProfileInUse(p); err != nil {
		return err
	}
	return c.mutate(func() error {
		if c.NameTaken(p.Name, except) {
			return &FieldError{Field: "name", Msg: "already used"}
		}
		idx := -1
		if except != "" {
			idx = c.index(except)
			if idx < 0 {
				return fmt.Errorf("profile %q not found", except)
			}
		}
		if idx >= 0 {
			c.doc.setProfile(idx, applyProfile(c.doc.profiles[idx], p))
		} else {
			c.doc.addProfile(applyProfile(nil, p))
		}
		return nil
	})
}

// Remove deletes the named profile and saves.
func (c *Config) Remove(name string) error {
	return c.mutate(func() error {
		idx := c.index(name)
		if idx < 0 {
			return fmt.Errorf("profile %q not found", name)
		}
		c.doc.removeProfile(idx)
		return nil
	})
}

// mutate applies edit to the file's current contents and writes the result,
// all while holding the config lock.
//
// The re-read matters: a Config can be minutes old by the time the user saves
// a form, and writing the document captured at Open would erase whatever
// another editor changed in between. Taking the same lock as StateStore also
// keeps two Wicket processes from interleaving their writes.
func (c *Config) mutate(edit func() error) error {
	unlock, err := c.lock()
	if err != nil {
		return err
	}
	defer unlock()

	switch err := c.reload(); {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		// The file was removed while this Config was open. Recreating it is
		// what OpenOrCreate would have done, and is friendlier than refusing
		// to save the profile the user just filled in.
		fresh, perr := parseConfig(c.path, []byte(starterTOML))
		if perr != nil {
			return perr
		}
		c.doc, c.profiles = fresh.doc, fresh.profiles
	default:
		return fmt.Errorf("re-read config before saving: %w", err)
	}
	if err := edit(); err != nil {
		return err
	}
	if err := c.save(); err != nil {
		return err
	}
	return c.reload()
}

// save writes the document atomically at 0600. Callers must hold the lock.
func (c *Config) save() error {
	data, patched, err := c.doc.render()
	if err != nil {
		return err
	}
	if err := atomicWrite(c.path, data); err != nil {
		return err
	}
	c.rewrote = !patched
	return nil
}

func (c *Config) lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	f, err := os.OpenFile(c.path+lockSuffix, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("config lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("config lock: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}

func (c *Config) reload() error {
	fresh, err := Open(c.path)
	if err != nil {
		return err
	}
	c.doc = fresh.doc
	c.profiles = fresh.profiles
	return nil
}

func (c *Config) index(name string) int {
	for i, p := range c.profiles {
		if p.Name == name {
			return i
		}
	}
	return -1
}
