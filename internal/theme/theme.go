package theme

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Palette holds Wicket chrome colors. Values are concrete RGBA (not Lipgloss).
type Palette struct {
	Surface   color.Color
	Border    color.Color
	Primary   color.Color
	Secondary color.Color
	Muted     color.Color
	Accent    color.Color
	Brand     color.Color
	Success   color.Color
	Danger    color.Color
	Warning   color.Color
	Selection color.Color
	Hex       map[string]string
}

// Report notes which roles used fallback.
type Report struct {
	FallbackRoles []string
	MissingFile   bool
	InvalidTOML   bool
}

// fileColors is the part of an Omarchy colors.toml that Wicket reads. A key
// that is missing or not a string is left empty, and its role falls back.
type fileColors struct {
	Background      string
	Foreground      string
	DarkForeground  string
	LightForeground string
	Muted           string
	Accent          string
	Green           string
	Red             string
	Yellow          string
	Selection       string
}

// wicketDark and wicketLight are Verdigris, Wicket's own theme. They are
// what Wicket paints with when no Omarchy theme applies, and they fill any
// role an Omarchy file leaves out or gets wrong.
//
// The text roles (primary, muted and the status colours) clear
// minTextContrast against the common terminal backgrounds of their mode, not
// only against their own surface: Wicket never paints the surface, so the
// terminal's background is what the text actually sits on.
func wicketDark() map[string]string {
	return map[string]string{
		"surface":   "#161C1B",
		"border":    "#3C4A48", // decorative only
		"primary":   "#E4E7E1",
		"secondary": "#9AA5A0",
		"muted":     "#9AA5A0",
		"accent":    "#5EC4AE",
		"brand":     "#D9956A",
		"success":   "#8CC47A",
		"danger":    "#EE7B6E",
		"warning":   "#E3B45A",
		"selection": "#1F3833",
	}
}

func wicketLight() map[string]string {
	return map[string]string{
		"surface":   "#F7F8F6",
		"border":    "#BCC7C3", // decorative only
		"primary":   "#1E2624",
		"secondary": "#56625E",
		"muted":     "#56625E",
		"accent":    "#0F7564",
		"brand":     "#A0532A",
		"success":   "#336B27",
		"danger":    "#B3362B",
		"warning":   "#8A5D00",
		"selection": "#D6ECE6",
	}
}

// WicketDark and WicketLight return Verdigris as a Palette.
func WicketDark() Palette  { return paletteFromHex(wicketDark()) }
func WicketLight() Palette { return paletteFromHex(wicketLight()) }

// Load is total: a broken theme never fails the TUI.
func Load(home string) (Palette, Report) {
	fb := wicketDark()
	path := filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "colors.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return paletteFromHex(fb), Report{MissingFile: true, FallbackRoles: allRoles()}
	}
	// Read mode from an untyped decode first. Decoding straight into
	// fileColors leaves Mode set or unset depending on where the decoder hit
	// the first type error, which made light/dark selection vary run to run
	// for the same file.
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return paletteFromHex(fb), Report{InvalidTOML: true, FallbackRoles: allRoles()}
	}
	if modeIsLight(raw) {
		fb = wicketLight()
	}
	// Each role is read on its own, so a key of the wrong type costs that
	// role, not the file. Decoding into a struct stopped at the first type
	// error and threw the whole theme away, where a bad string fell back
	// per role.
	fc := fileColors{}
	for key, dst := range map[string]*string{
		"background": &fc.Background, "foreground": &fc.Foreground,
		"dark_foreground": &fc.DarkForeground, "light_foreground": &fc.LightForeground,
		"muted": &fc.Muted, "accent": &fc.Accent, "green": &fc.Green, "red": &fc.Red,
		"yellow": &fc.Yellow, "selection": &fc.Selection,
	} {
		*dst, _ = raw[key].(string)
	}
	hex := map[string]string{}
	var fell []string
	put := func(role, raw string) {
		if h, ok := parseHex(raw); ok {
			hex[role] = h
			return
		}
		hex[role] = fb[role]
		fell = append(fell, role)
	}
	put("surface", fc.Background)
	// Omarchy themes use muted as a surface/border tone (see
	// hyprland_inactive_border), and darker_background is almost the same
	// as background, so a border drawn in it cannot be seen.
	put("border", fc.Muted)
	put("primary", fc.Foreground)
	// A theme whose own foreground fails the contrast floor would otherwise
	// make every line of body text unreadable, and readableText leans on
	// primary as its last resort.
	if fixed := readableOn(hex["surface"], hex["primary"]); fixed != hex["primary"] {
		hex["primary"] = fixed
		fell = append(fell, "primary")
	}
	// muted is too dim to read as text in most themes. Use the dimmest
	// theme token that is still readable on the background, else foreground.
	text := readableText(hex["surface"], hex["primary"], fc.Muted, fc.DarkForeground, fc.LightForeground)
	hex["muted"] = text
	hex["secondary"] = text
	put("accent", fc.Accent)
	// An Omarchy theme has no token meant for a logo, and borrowing one of
	// the ANSI colours would clash with some themes. Accent is the theme's
	// own highlight, so the header mark matches the rest of the chrome.
	hex["brand"] = hex["accent"]
	put("success", fc.Green)
	put("danger", fc.Red)
	put("warning", fc.Yellow)
	put("selection", fc.Selection)
	return paletteFromHex(hex), Report{FallbackRoles: fell}
}

// minTextContrast is the WCAG AA contrast ratio for normal text.
const minTextContrast = 4.5

// readableText returns the first candidate that parses and reaches
// minTextContrast against surface, or primary when none does.
func readableText(surface, primary string, candidates ...string) string {
	for _, raw := range candidates {
		if h, ok := parseHex(raw); ok && contrast(h, surface) >= minTextContrast {
			return h
		}
	}
	return primary
}

// readableOn returns want when it reads on surface, and otherwise black or
// white, whichever reads better. Plain black or white always clears the floor
// against something, so this cannot fail.
func readableOn(surface, want string) string {
	if want != "" && contrast(want, surface) >= minTextContrast {
		return want
	}
	if contrast("#000000", surface) >= contrast("#FFFFFF", surface) {
		return "#000000"
	}
	return "#FFFFFF"
}

// liftTo returns want when it reaches target against surface, and otherwise
// want mixed towards black or white, whichever reads better on surface, just
// far enough to reach it. Unlike readableOn it keeps the colour's hue where it
// can, so a lifted accent still reads as the accent rather than as more body
// text. When target cannot be reached, it returns black or white.
func liftTo(surface, want string, target float64) string {
	if want == "" {
		return readableOn(surface, want)
	}
	if contrast(want, surface) >= target {
		return want
	}
	ext := readableOn(surface, "")
	lo, hi := 0.0, 1.0
	for range 24 {
		mid := (lo + hi) / 2
		if contrast(mix(want, ext, mid), surface) >= target {
			hi = mid
		} else {
			lo = mid
		}
	}
	return mix(want, ext, hi)
}

// mix is a blended t of the way from a to b, in sRGB.
func mix(a, b string, t float64) string {
	if t >= 1 {
		return b
	}
	ra, ga, ba, _ := mustRGBA(a).RGBA()
	rb, gb, bb, _ := mustRGBA(b).RGBA()
	ch := func(x, y uint32) uint8 {
		fx, fy := float64(x>>8), float64(y>>8)
		return uint8(math.Round(fx + (fy-fx)*t))
	}
	return fmt.Sprintf("#%02X%02X%02X", ch(ra, rb), ch(ga, gb), ch(ba, bb))
}

func modeIsLight(raw map[string]any) bool {
	s, _ := raw["mode"].(string)
	return strings.EqualFold(strings.TrimSpace(s), "light")
}

// contrast is the WCAG 2 contrast ratio between two #RRGGBB colors.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	r, g, b, _ := mustRGBA(hex).RGBA()
	lin := func(v uint32) float64 {
		c := float64(v>>8) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func allRoles() []string {
	return []string{"surface", "border", "primary", "secondary", "muted", "accent", "brand", "success", "danger", "warning", "selection"}
}

func parseHex(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 7 || s[0] != '#' {
		return "", false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return "", false
		}
	}
	return strings.ToUpper(s[:1] + s[1:]), true
}

func paletteFromHex(hex map[string]string) Palette {
	p := Palette{Hex: hex}
	p.Surface = mustRGBA(hex["surface"])
	p.Border = mustRGBA(hex["border"])
	p.Primary = mustRGBA(hex["primary"])
	p.Secondary = mustRGBA(hex["secondary"])
	p.Muted = mustRGBA(hex["muted"])
	p.Accent = mustRGBA(hex["accent"])
	p.Brand = mustRGBA(hex["brand"])
	p.Success = mustRGBA(hex["success"])
	p.Danger = mustRGBA(hex["danger"])
	p.Warning = mustRGBA(hex["warning"])
	p.Selection = mustRGBA(hex["selection"])
	return p
}

func mustRGBA(hex string) color.Color {
	if hex == "" {
		return color.RGBA{R: 0, G: 0, B: 0, A: 255}
	}
	var r, g, b uint8
	_, _ = parseByte(hex[1:3], &r)
	_, _ = parseByte(hex[3:5], &g)
	_, _ = parseByte(hex[5:7], &b)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

func parseByte(s string, dst *uint8) (int, error) {
	var n uint8
	for i := 0; i < len(s); i++ {
		n <<= 4
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			n |= c - '0'
		case c >= 'a' && c <= 'f':
			n |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			n |= c - 'A' + 10
		}
	}
	*dst = n
	return 1, nil
}
