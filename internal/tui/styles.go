package tui

import (
	"image/color"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/theme"
)

type styles struct {
	frame   lipgloss.Style
	title   lipgloss.Style
	muted   lipgloss.Style
	primary lipgloss.Style
	accent  lipgloss.Style
	brand   lipgloss.Style
	success lipgloss.Style
	danger  lipgloss.Style
	warning lipgloss.Style
	key     lipgloss.Style
	divider lipgloss.Style
	// selection is the background of the selected list row, or nil in
	// terminal mode, where the row is reverse video instead. Each segment of
	// the row is styled on its own, since an inner style's reset would end
	// an outer background.
	selection color.Color
	input     textinput.Styles
}

// colours is one set of role colours, whether a theme's RGB or the
// terminal's ANSI indices. A nil colour means the terminal's default.
type colours struct {
	border, text, muted, accent, brand, success, warning, danger color.Color
}

// stylesFor builds the styles for look.
func stylesFor(look theme.Look) styles {
	if look.Kind == theme.KindTerminal {
		return terminalStyles()
	}
	return newStyles(look.Palette)
}

// The frame does not paint a background: Wicket draws on the terminal's own
// background, which Omarchy terminals already set from the theme.
func newStyles(p theme.Palette) styles {
	st := baseStyles(colours{
		border: p.Border, text: p.Primary, muted: p.Muted, accent: p.Accent,
		brand: p.Brand, success: p.Success, warning: p.Warning, danger: p.Danger,
	})
	st.selection = p.Selection
	return st
}

// terminalStyles paints with the terminal's ANSI palette. It is used when the
// background is unknown, so no RGB colour can be trusted to read on it: the
// user's terminal theme already made its ANSI colours readable there. Body
// text keeps the default foreground, and the selected row is reverse video
// rather than a background of our choosing.
func terminalStyles() styles {
	return baseStyles(colours{
		border:  lipgloss.BrightBlack,
		muted:   lipgloss.BrightBlack,
		accent:  lipgloss.Cyan,
		brand:   lipgloss.Magenta, // yellow is taken by warning
		success: lipgloss.Green,
		warning: lipgloss.Yellow,
		danger:  lipgloss.Red,
	})
}

func baseStyles(c colours) styles {
	fg := func(col color.Color) lipgloss.Style {
		s := lipgloss.NewStyle()
		if col != nil {
			s = s.Foreground(col)
		}
		return s
	}
	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if c.border != nil {
		frame = frame.BorderForeground(c.border)
	}
	return styles{
		frame:   frame,
		title:   fg(c.accent).Bold(true),
		muted:   fg(c.muted),
		primary: fg(c.text),
		accent:  fg(c.accent),
		brand:   fg(c.brand).Bold(true),
		success: fg(c.success),
		danger:  fg(c.danger),
		warning: fg(c.warning),
		key:     fg(c.text).Bold(true),
		divider: fg(c.border),
		input:   inputStyles(c),
	}
}

func inputStyles(c colours) textinput.Styles {
	text := lipgloss.NewStyle()
	if c.text != nil {
		text = text.Foreground(c.text)
	}
	state := textinput.StyleState{
		Text:        text,
		Placeholder: lipgloss.NewStyle().Foreground(c.muted),
	}
	return textinput.Styles{
		Focused: state,
		Blurred: state,
		Cursor:  textinput.CursorStyle{Color: c.accent},
	}
}

// onSelection returns s as it looks on the selected row. Without a known
// background, reverse video is the one highlight every terminal can show. The
// foreground goes with it: reversed, it would become the background of each
// segment, turning one row into a patchwork of blocks.
func (st styles) onSelection(s lipgloss.Style) lipgloss.Style {
	if st.selection == nil {
		return s.UnsetForeground().Reverse(true).Bold(true)
	}
	return s.Background(st.selection)
}

// selectionMark styles the ▌ bar that leads the selected row. In reverse
// video the half block would invert into a notch, so there the bar keeps its
// own colour and the reversal starts after it.
func (st styles) selectionMark() lipgloss.Style {
	if st.selection == nil {
		return st.accent
	}
	return st.onSelection(st.accent)
}
