package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MapsDistinctHexes(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `
mode = "dark"
background = "#010101"
darker_background = "#020202"
foreground = "#030303"
muted = "#040404"
accent = "#050505"
green = "#060606"
red = "#070707"
yellow = "#080808"
selection = "#090909"
`)
	p, rep := Load(home)
	if rep.MissingFile || rep.InvalidTOML {
		t.Fatalf("%+v", rep)
	}
	if p.Hex["surface"] != "#010101" || p.Hex["border"] != "#020202" || p.Hex["primary"] != "#030303" {
		t.Fatalf("%v", p.Hex)
	}
	if p.Hex["muted"] != "#040404" || p.Hex["secondary"] != "#040404" {
		t.Fatalf("muted/secondary %v", p.Hex)
	}
	if p.Hex["accent"] != "#050505" || p.Hex["success"] != "#060606" || p.Hex["danger"] != "#070707" {
		t.Fatalf("%v", p.Hex)
	}
	if p.Hex["warning"] != "#080808" || p.Hex["selection"] != "#090909" {
		t.Fatalf("%v", p.Hex)
	}
	if len(rep.FallbackRoles) != 0 {
		t.Fatalf("fallback %v", rep.FallbackRoles)
	}
}

func TestLoad_MissingAndInvalid(t *testing.T) {
	p, rep := Load(t.TempDir())
	if !rep.MissingFile || p.Hex["surface"] == "" {
		t.Fatalf("%+v %+v", p, rep)
	}
	home := t.TempDir()
	writeTheme(t, home, "this is { not toml")
	_, rep = Load(home)
	if !rep.InvalidTOML {
		t.Fatalf("%+v", rep)
	}
}

func TestLoad_PerRoleFallback(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `
mode = "dark"
background = "#111111"
foreground = "nope"
accent = "#ABCDEF"
`)
	p, rep := Load(home)
	if p.Hex["surface"] != "#111111" {
		t.Fatalf("surface %s", p.Hex["surface"])
	}
	if p.Hex["accent"] != "#ABCDEF" {
		t.Fatalf("accent %s", p.Hex["accent"])
	}
	if p.Hex["primary"] == "#111111" || p.Hex["primary"] == "" {
		t.Fatal("primary should fall back")
	}
	found := false
	for _, r := range rep.FallbackRoles {
		if r == "primary" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fallback roles %v", rep.FallbackRoles)
	}
}

func writeTheme(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
