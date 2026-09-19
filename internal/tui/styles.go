package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/theme"
)

type styles struct {
	frame   lipgloss.Style
	title   lipgloss.Style
	muted   lipgloss.Style
	primary lipgloss.Style
	accent  lipgloss.Style
	danger  lipgloss.Style
	warning lipgloss.Style
	key     lipgloss.Style
	divider lipgloss.Style
	// selection is the background of the selected list row. Each segment of
	// the row is rendered with it, since an inner style's reset would end an
	// outer background.
	selection color.Color
}

// The frame does not paint a background: Wicket draws on the terminal's own
// background, which Omarchy terminals already set from the theme.
func newStyles(p theme.Palette) styles {
	fg := func(c color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(c)
	}
	return styles{
		frame: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Border).
			Padding(0, 1),
		title:     fg(p.Accent).Bold(true),
		muted:     fg(p.Muted),
		primary:   fg(p.Primary),
		accent:    fg(p.Accent),
		danger:    fg(p.Danger),
		warning:   fg(p.Warning),
		key:       fg(p.Primary).Bold(true),
		divider:   fg(p.Border),
		selection: p.Selection,
	}
}

// onSelection returns s with the selection background.
func (st styles) onSelection(s lipgloss.Style) lipgloss.Style {
	return s.Background(st.selection)
}
