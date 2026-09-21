package theme

import (
	"fmt"
	"image/color"
	"strings"
)

// Mode is the theme setting, from WICKET_THEME or [ui] theme in the config.
type Mode string

const (
	// ModeAuto uses the Omarchy theme when there is a readable one, and
	// otherwise Wicket's own theme in the terminal's light or dark.
	ModeAuto Mode = "auto"
	// ModeWicket ignores Omarchy and picks light or dark from the terminal.
	ModeWicket      Mode = "wicket"
	ModeWicketDark  Mode = "wicket-dark"
	ModeWicketLight Mode = "wicket-light"
	ModeOmarchy     Mode = "omarchy"
	// ModeTerminal paints with the terminal's own ANSI colours.
	ModeTerminal Mode = "terminal"
)

// Modes lists every accepted setting, for messages and docs.
func Modes() []Mode {
	return []Mode{ModeAuto, ModeWicket, ModeWicketDark, ModeWicketLight, ModeOmarchy, ModeTerminal}
}

// ParseMode accepts a setting regardless of case and surrounding space.
func ParseMode(s string) (Mode, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, m := range Modes() {
		if string(m) == s {
			return m, true
		}
	}
	return "", false
}

// ResolveMode applies the precedence WICKET_THEME, then [ui] theme, then auto.
// A set but unknown value is treated as auto and explained in warning, rather
// than refusing to start: a typo in a colour setting is not worth losing the
// connection list over.
func ResolveMode(env, file string) (mode Mode, warning string) {
	raw, from := file, "[ui] theme"
	if strings.TrimSpace(env) != "" {
		raw, from = env, "WICKET_THEME"
	}
	if strings.TrimSpace(raw) == "" {
		return ModeAuto, ""
	}
	if m, ok := ParseMode(raw); ok {
		return m, ""
	}
	return ModeAuto, fmt.Sprintf("unknown theme %q in %s; using auto", strings.TrimSpace(raw), from)
}

// Kind is what a Look paints with.
type Kind int

const (
	// KindTerminal uses the terminal's ANSI colours; Palette is unset.
	KindTerminal Kind = iota
	KindWicket
	KindOmarchy
)

// Look is the theme the TUI draws with right now.
type Look struct {
	Kind Kind
	// Dark reports which Wicket variant is in use. It means nothing for
	// the other kinds.
	Dark    bool
	Palette Palette
}

// Setup is the theme decision made at start, before the terminal has said
// what its background is.
type Setup struct {
	Mode Mode
	// Look is what to draw with until the terminal replies, or for good
	// when it never does.
	Look Look
	// Detect asks the caller to query the terminal's background colour
	// and hand the reply to Adapt. The caller decides whether it can: only
	// a real terminal answers.
	Detect bool
	// Warning explains a setting that could not be honoured.
	Warning string
}

// Choose makes the start-up decision for mode. home locates the Omarchy
// theme file. It never fails: whatever goes wrong, the terminal's own colours
// are still a usable theme.
func Choose(mode Mode, home string) Setup {
	switch mode {
	case ModeWicketDark:
		return Setup{Mode: mode, Look: Look{Kind: KindWicket, Dark: true, Palette: WicketDark()}}
	case ModeWicketLight:
		return Setup{Mode: mode, Look: Look{Kind: KindWicket, Palette: WicketLight()}}
	case ModeTerminal:
		return Setup{Mode: mode, Look: Look{Kind: KindTerminal}}
	case ModeWicket:
		return Setup{Mode: mode, Look: Look{Kind: KindTerminal}, Detect: true}
	case ModeOmarchy:
		p, rep := Load(home)
		if rep.MissingFile || rep.InvalidTOML {
			return Setup{Mode: mode, Look: Look{Kind: KindTerminal},
				Warning: "no readable Omarchy theme; using terminal colours"}
		}
		return omarchySetup(mode, p)
	default:
		p, rep := Load(home)
		if rep.MissingFile || rep.InvalidTOML {
			return Setup{Mode: ModeAuto, Look: Look{Kind: KindTerminal}, Detect: true}
		}
		return omarchySetup(ModeAuto, p)
	}
}

// omarchySetup draws Omarchy straight away but still asks for the background:
// the theme file does not prove the terminal is using it, and text is only
// known to be readable against the background it actually sits on.
func omarchySetup(mode Mode, p Palette) Setup {
	return Setup{Mode: mode, Look: Look{Kind: KindOmarchy, Palette: p}, Detect: true}
}

// Adapt returns the Look for a terminal whose background is bg. A Setup that
// did not ask for detection keeps its Look.
func (s Setup) Adapt(bg color.Color) Look {
	if !s.Detect || bg == nil {
		return s.Look
	}
	surface := hexOf(bg)
	look := s.Look
	if look.Kind != KindOmarchy {
		look.Dark = IsDark(bg)
		look.Kind = KindWicket
		look.Palette = WicketLight()
		if look.Dark {
			look.Palette = WicketDark()
		}
	}
	look.Palette = readableTextOn(look.Palette, surface)
	return look
}

// IsDark reports whether dark text would read worse on bg than light text.
func IsDark(bg color.Color) bool {
	h := hexOf(bg)
	return contrast("#FFFFFF", h) >= contrast("#000000", h)
}

// readableTextOn lifts the roles that carry words to the contrast floor
// against surface and leaves the rest alone. The status roles are text, and
// the ones that matter most: an unreadable danger line is a failure the user
// never sees. The accent and brand are text too -- the footer's keys, the
// header mark, the focus and selection bars -- so they are lifted when, and
// only when, the real background makes them unreadable: an Omarchy theme's
// accents are its identity, and a dark theme on a light terminal left its
// accent at 2.5:1. The border is a surface, not text, and is never touched.
//
// Roles are lifted by as little as reaches the floor, keeping their hue, and
// body text that has to move goes further than the rest, to the AAA ratio:
// snapping everything to black left body and muted text the same colour.
//
// The selection background is a surface too, and is kept while the selected
// row's text reads on it. Once that text has been lifted for a background
// the theme was not made for, it may not: a dark theme's selection on a light
// terminal left the selected name at 1.7:1. The selection is then made
// again, as a tint of the real background towards the accent, and the row's
// text lifted to read on it as well.
func readableTextOn(p Palette, surface string) Palette {
	hex := make(map[string]string, len(p.Hex))
	for k, v := range p.Hex {
		hex[k] = v
	}
	for _, role := range []string{"secondary", "muted", "success", "warning", "danger", "accent", "brand"} {
		hex[role] = liftTo(surface, hex[role], minTextContrast)
	}
	if contrast(hex["primary"], surface) < minTextContrast {
		hex["primary"] = liftTo(surface, hex["primary"], bodyLiftContrast)
	}
	if !readsOn(hex, hex["selection"]) {
		hex["selection"] = mix(surface, hex["accent"], selectionTint)
		for _, role := range selectedRowRoles {
			hex[role] = liftTo(hex["selection"], hex[role], minTextContrast)
		}
	}
	return paletteFromHex(hex)
}

// selectedRowRoles are the colours drawn on the selection background: the
// name, the host and last-used time, and the accent bar and filter match.
var selectedRowRoles = []string{"primary", "secondary", "muted", "accent"}

// selectionTint is how far a made selection moves from the background
// towards the accent: about what Verdigris's own light selection is.
const selectionTint = 0.15

// readsOn reports whether every colour of the selected row reaches the
// contrast floor on bg.
func readsOn(hex map[string]string, bg string) bool {
	if bg == "" {
		return false
	}
	for _, role := range selectedRowRoles {
		if contrast(hex[role], bg) < minTextContrast {
			return false
		}
	}
	return true
}

// bodyLiftContrast is the WCAG AAA ratio, which body text is lifted to when
// it has to be lifted at all, so it stays apart from muted text at the AA
// floor.
const bodyLiftContrast = 7

func hexOf(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02X%02X%02X", r>>8, g>>8, b>>8)
}
