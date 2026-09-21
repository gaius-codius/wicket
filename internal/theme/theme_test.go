package theme

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoad_MapsDistinctHexes(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `
mode = "dark"
background = "#101010"
darker_background = "#020202"
foreground = "#F0F0F0"
muted = "#A0A0A0"
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
	if p.Hex["surface"] != "#101010" || p.Hex["border"] != "#A0A0A0" || p.Hex["primary"] != "#F0F0F0" {
		t.Fatalf("%v", p.Hex)
	}
	if p.Hex["muted"] != "#A0A0A0" || p.Hex["secondary"] != "#A0A0A0" {
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

// Omarchy themes often use muted as a surface tone. Secondary text must use
// the dimmest theme token that is readable on the background.
func TestLoad_SecondaryTextIsReadable(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"dim muted skips to light_foreground (beirut-noir)", `
mode = "dark"
background = "#0C080C"
foreground = "#F0E5D3"
muted = "#322938"
dark_foreground = "#6D6478"
light_foreground = "#CBBFAB"
`, "#CBBFAB"},
		{"readable dark_foreground wins over light_foreground (osaka-jade)", `
mode = "dark"
background = "#111c18"
foreground = "#C1C497"
muted = "#53685B"
dark_foreground = "#81B8A8"
light_foreground = "#D6D5BC"
`, "#81B8A8"},
		{"readable muted is kept (vantablack)", `
mode = "dark"
background = "#000000"
foreground = "#ffffff"
muted = "#7a7a7a"
dark_foreground = "#505050"
light_foreground = "#ececec"
`, "#7A7A7A"},
		{"light theme (catppuccin-latte)", `
mode = "light"
background = "#eff1f5"
foreground = "#4c4f69"
muted = "#acb0be"
dark_foreground = "#9ca0b0"
light_foreground = "#5c5f77"
`, "#5C5F77"},
		{"nothing readable falls back to foreground", `
mode = "dark"
background = "#101010"
foreground = "#EEEEEE"
muted = "#202020"
`, "#EEEEEE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeTheme(t, home, tc.body)
			p, _ := Load(home)
			if p.Hex["muted"] != tc.want || p.Hex["secondary"] != tc.want {
				t.Fatalf("muted=%s secondary=%s want %s", p.Hex["muted"], p.Hex["secondary"], tc.want)
			}
			if c := contrast(p.Hex["muted"], p.Hex["surface"]); c < minTextContrast {
				t.Fatalf("contrast %.2f < %.1f", c, minTextContrast)
			}
		})
	}
}

func TestFallbackSecondaryIsReadable(t *testing.T) {
	for name, fb := range map[string]map[string]string{"dark": wicketDark(), "light": wicketLight()} {
		if c := contrast(fb["muted"], fb["surface"]); c < minTextContrast {
			t.Errorf("%s fallback muted contrast %.2f", name, c)
		}
	}
}

func TestContrast(t *testing.T) {
	if c := contrast("#000000", "#FFFFFF"); c < 20.9 || c > 21.1 {
		t.Fatalf("black/white = %.2f, want 21", c)
	}
	if c := contrast("#777777", "#777777"); c != 1 {
		t.Fatalf("same color = %.2f, want 1", c)
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

// A theme can set a foreground that is nearly invisible on its own background.
// The floor has to hold there too, or every line of body text is unreadable.
func TestLoad_ForegroundBelowTheFloorIsReplaced(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `mode = "dark"
background = "#101010"
foreground = "#121212"
muted = "#141414"
accent = "#6FA3D8"
green = "#7FB069"
red = "#D45D5D"
yellow = "#D4A017"
selection = "#2C3144"
`)
	pal, rep := Load(home)
	for _, role := range []string{"primary", "secondary", "muted"} {
		if got := contrast(pal.Hex[role], pal.Hex["surface"]); got < minTextContrast {
			t.Errorf("%s contrast %.2f on surface, want >= %.1f", role, got, minTextContrast)
		}
	}
	if !slices.Contains(rep.FallbackRoles, "primary") {
		t.Errorf("FallbackRoles = %v, want primary reported", rep.FallbackRoles)
	}
}

// A type error used to leave Mode set or unset depending on where the decoder
// stopped, so the same file could load light or dark from run to run.
func TestLoad_ModeIsStableWhenATypeErrorStopsTheDecode(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `mode = "light"
background = 12345
foreground = "#2A2A32"
`)
	first, _ := Load(home)
	for i := 0; i < 32; i++ {
		got, _ := Load(home)
		if got.Hex["surface"] != first.Hex["surface"] {
			t.Fatalf("run %d surface %s, first run %s", i, got.Hex["surface"], first.Hex["surface"])
		}
	}
	if first.Hex["surface"] != wicketLight()["surface"] {
		t.Fatalf("surface = %s, want the light fallback %s", first.Hex["surface"], wicketLight()["surface"])
	}
}
