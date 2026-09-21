package theme

import (
	"image/color"
	"strings"
	"testing"
)

// Wicket never paints its own surface, so its text sits on whatever the
// terminal's background is. Each text role has to read on the common ones,
// and on the selected row.
func TestWicketPalettes_TextReadsOnCommonBackgrounds(t *testing.T) {
	textRoles := []string{"primary", "secondary", "muted", "accent", "brand", "success", "warning", "danger"}
	for _, tc := range []struct {
		name string
		pal  map[string]string
		bgs  []string
	}{
		{"dark", wicketDark(), []string{"#000000", "#1E1E2E", "#282C34", "#2E3440"}},
		{"light", wicketLight(), []string{"#FFFFFF", "#FAFAFA", "#EEE8D5"}},
	} {
		bgs := append(tc.bgs, tc.pal["surface"], tc.pal["selection"])
		for _, role := range textRoles {
			for _, bg := range bgs {
				if c := contrast(tc.pal[role], bg); c < minTextContrast {
					t.Errorf("%s %s %s on %s: contrast %.2f < %.1f", tc.name, role, tc.pal[role], bg, c, minTextContrast)
				}
			}
		}
	}
}

func TestWicketPalettes_HaveEveryRole(t *testing.T) {
	for name, pal := range map[string]map[string]string{"dark": wicketDark(), "light": wicketLight()} {
		for _, role := range allRoles() {
			if _, ok := parseHex(pal[role]); !ok {
				t.Errorf("%s palette role %s = %q", name, role, pal[role])
			}
		}
	}
}

func TestResolveMode_Precedence(t *testing.T) {
	for _, tc := range []struct {
		env, file string
		want      Mode
	}{
		{"", "", ModeAuto},
		{"", "omarchy", ModeOmarchy},
		{"wicket-light", "omarchy", ModeWicketLight},
		{"  ", "terminal", ModeTerminal}, // blank is unset, not a value
		{" Wicket-Dark ", "", ModeWicketDark},
		{"", "wicket", ModeWicket},
	} {
		got, warn := ResolveMode(tc.env, tc.file)
		if got != tc.want || warn != "" {
			t.Errorf("ResolveMode(%q, %q) = %q, %q; want %q", tc.env, tc.file, got, warn, tc.want)
		}
	}
}

// An unknown value falls to auto, not to the next source: the user asked for
// something specific, and quietly honouring a different setting would hide
// the typo.
func TestResolveMode_InvalidIsAutoWithAWarning(t *testing.T) {
	for _, tc := range []struct{ env, file, source string }{
		{"solarized", "wicket-light", "WICKET_THEME"},
		{"", "dracula", "[ui] theme"},
	} {
		got, warn := ResolveMode(tc.env, tc.file)
		if got != ModeAuto {
			t.Errorf("ResolveMode(%q, %q) = %q, want auto", tc.env, tc.file, got)
		}
		if !strings.Contains(warn, tc.source) || !strings.Contains(warn, "auto") {
			t.Errorf("warning %q should name %s and say auto", warn, tc.source)
		}
	}
}

func TestChoose(t *testing.T) {
	withOmarchy := t.TempDir()
	writeTheme(t, withOmarchy, `mode = "dark"
background = "#101010"
foreground = "#F0F0F0"
accent = "#ABCDEF"
`)
	without := t.TempDir()
	for _, tc := range []struct {
		name   string
		mode   Mode
		home   string
		kind   Kind
		detect bool
		warn   bool
	}{
		{"auto with Omarchy", ModeAuto, withOmarchy, KindOmarchy, true, false},
		{"auto without Omarchy", ModeAuto, without, KindTerminal, true, false},
		{"wicket ignores Omarchy", ModeWicket, withOmarchy, KindTerminal, true, false},
		{"wicket-dark", ModeWicketDark, withOmarchy, KindWicket, false, false},
		{"wicket-light", ModeWicketLight, without, KindWicket, false, false},
		{"terminal", ModeTerminal, withOmarchy, KindTerminal, false, false},
		{"omarchy", ModeOmarchy, withOmarchy, KindOmarchy, true, false},
		{"omarchy missing", ModeOmarchy, without, KindTerminal, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Choose(tc.mode, tc.home)
			if s.Look.Kind != tc.kind || s.Detect != tc.detect || (s.Warning != "") != tc.warn {
				t.Fatalf("Choose = kind %d detect %v warning %q; want kind %d detect %v warning %v",
					s.Look.Kind, s.Detect, s.Warning, tc.kind, tc.detect, tc.warn)
			}
		})
	}
	if s := Choose(ModeWicketLight, without); s.Look.Palette.Hex["primary"] != wicketLight()["primary"] {
		t.Fatalf("wicket-light primary %s", s.Look.Palette.Hex["primary"])
	}
	if s := Choose(ModeWicketDark, without); s.Look.Palette.Hex["primary"] != wicketDark()["primary"] {
		t.Fatalf("wicket-dark primary %s", s.Look.Palette.Hex["primary"])
	}
}

func TestAdapt_PicksTheVariantForTheBackground(t *testing.T) {
	s := Choose(ModeWicket, t.TempDir())
	light := s.Adapt(color.RGBA{0xFA, 0xFA, 0xFA, 0xFF})
	if light.Kind != KindWicket || light.Dark || light.Palette.Hex["primary"] != wicketLight()["primary"] {
		t.Fatalf("light background: %+v", light)
	}
	dark := s.Adapt(color.RGBA{0x1E, 0x1E, 0x2E, 0xFF})
	if dark.Kind != KindWicket || !dark.Dark || dark.Palette.Hex["primary"] != wicketDark()["primary"] {
		t.Fatalf("dark background: %+v", dark)
	}
}

func TestAdapt_ForcedModesIgnoreTheReply(t *testing.T) {
	s := Choose(ModeWicketDark, t.TempDir())
	if got := s.Adapt(color.White); got.Palette.Hex["primary"] != wicketDark()["primary"] {
		t.Fatalf("wicket-dark switched on a white background: %s", got.Palette.Hex["primary"])
	}
}

// An Omarchy file does not prove the terminal uses it. Once the real
// background is known, body text must read on it, but the theme's accents are
// left as the theme chose them.
func TestAdapt_OmarchyTextIsReadableOnTheRealBackground(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `mode = "dark"
background = "#101010"
foreground = "#F0F0F0"
muted = "#A0A0A0"
accent = "#ABCDEF"
`)
	s := Choose(ModeAuto, home)
	got := s.Adapt(color.RGBA{0xFF, 0xFF, 0xFF, 0xFF})
	if got.Kind != KindOmarchy {
		t.Fatalf("kind %d, want Omarchy", got.Kind)
	}
	for _, role := range []string{"primary", "secondary", "muted"} {
		if c := contrast(got.Palette.Hex[role], "#FFFFFF"); c < minTextContrast {
			t.Errorf("%s %s on white: contrast %.2f", role, got.Palette.Hex[role], c)
		}
	}
	if got.Palette.Hex["accent"] != "#ABCDEF" {
		t.Errorf("accent rewritten to %s", got.Palette.Hex["accent"])
	}
}

func TestLoad_BrandFollowsAccent(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `mode = "dark"
background = "#101010"
foreground = "#F0F0F0"
accent = "#ABCDEF"
`)
	p, _ := Load(home)
	if p.Hex["brand"] != "#ABCDEF" {
		t.Fatalf("brand %s, want the accent", p.Hex["brand"])
	}
}
