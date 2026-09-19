package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

const starterTOML = "[general]\n"

// Config is an open wicket config file with a typed view and a raw document.
type Config struct {
	path     string
	doc      *document
	profiles []Profile
}

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
	if err := ValidateProfile(p); err != nil {
		return err
	}
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
	table := map[string]any{}
	if idx >= 0 {
		table = c.doc.profiles[idx]
	}
	table = applyProfile(table, p)
	if idx >= 0 {
		c.doc.profiles[idx] = table
	} else {
		c.doc.profiles = append(c.doc.profiles, table)
	}
	if err := c.Save(); err != nil {
		return err
	}
	return c.reload()
}

// Remove deletes the named profile and saves.
func (c *Config) Remove(name string) error {
	idx := c.index(name)
	if idx < 0 {
		return fmt.Errorf("profile %q not found", name)
	}
	c.doc.profiles = append(c.doc.profiles[:idx], c.doc.profiles[idx+1:]...)
	if err := c.Save(); err != nil {
		return err
	}
	return c.reload()
}

// Save writes the document atomically at 0600.
func (c *Config) Save() error {
	data, err := c.doc.encode()
	if err != nil {
		return err
	}
	return atomicWrite(c.path, data)
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
