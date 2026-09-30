package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath turns "~" / "~/…" into an absolute path using the current
// user's home. Other paths are cleaned but otherwise left as given; a
// relative path stays relative so validation can refuse it.
func ExpandPath(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return filepath.Clean(home), nil
		}
		return filepath.Clean(filepath.Join(home, p[2:])), nil
	}
	return filepath.Clean(p), nil
}

// ShareNameFromPath is the FreeRDP share name used when the share has none
// of its own: the path's base name, with characters FreeRDP would not take
// in a name turned into underscores.
func ShareNameFromPath(path string) string {
	base := filepath.Base(strings.TrimRight(path, string(filepath.Separator)))
	if base == "." || base == ".." || base == string(filepath.Separator) || base == "" {
		return "share"
	}
	return sanitizeShareName(base)
}

func sanitizeShareName(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		ok := r == '_' || r == '-' ||
			r >= '0' && r <= '9' ||
			r >= 'A' && r <= 'Z' ||
			r >= 'a' && r <= 'z'
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "share"
	}
	return out
}
