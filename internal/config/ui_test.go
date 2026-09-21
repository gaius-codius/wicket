package config

import "testing"

func TestUITheme(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, want string
		wantErr          bool
	}{
		{"unset", "[general]\n", "", false},
		{"no theme key", "[ui]\nother = 1\n", "", false},
		{"set", "[ui]\ntheme = \"wicket-light\"\n", "wicket-light", false},
		// Not a known theme, but that is for the TUI to judge.
		{"unknown value", "[ui]\ntheme = \"solarized\"\n", "solarized", false},
		{"not a string", "[ui]\ntheme = 3\n", "", true},
		{"ui not a table", "ui = \"dark\"\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A bad [ui] must never fail Open: `wicket connect` reads the
			// same file and has no use for a theme.
			c, err := Open(writeTOML(t, tc.body))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			got, err := c.UITheme()
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("UITheme() = %q, %v; want %q, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestPreserveUITableOnSave(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `
[ui]
theme = "wicket-light"
future_knob = 7

[[profiles]]
name = "work"
host = "h"
user = "u"
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Profile("work")
	p.Host = "h2"
	if err := c.Upsert(p, "work"); err != nil {
		t.Fatal(err)
	}
	ui, ok := decodeRaw(t, path)["ui"].(map[string]any)
	if !ok {
		t.Fatalf("[ui] lost on save")
	}
	assertType(t, ui["theme"], "wicket-light")
	assertType(t, ui["future_knob"], int64(7))
	if got, err := c.UITheme(); got != "wicket-light" || err != nil {
		t.Fatalf("UITheme after save = %q, %v", got, err)
	}
}
