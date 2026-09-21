package config

import "fmt"

const keyUI = "ui"

// UITheme returns [ui] theme as written, or "" when it is not set.
//
// The value is not checked against the known themes here, and Open never
// rejects it: a bad colour setting must not stop `wicket connect` or hide the
// connection list. The TUI validates it when it starts and says so on the
// status line. The [ui] table stays in the document's extras, so a profile
// save writes it back untouched, unknown keys included.
func (c *Config) UITheme() (string, error) {
	if c.doc == nil {
		return "", nil
	}
	raw, ok := c.doc.extras[keyUI]
	if !ok {
		return "", nil
	}
	ui, ok := raw.(map[string]any)
	if !ok {
		return "", fmt.Errorf("[%s] must be a table", keyUI)
	}
	v, ok := ui["theme"]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("[%s] theme must be a string", keyUI)
	}
	return s, nil
}
