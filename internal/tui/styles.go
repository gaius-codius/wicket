package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/theme"
)

type styles struct {
	frame     lipgloss.Style
	title     lipgloss.Style
	header    lipgloss.Style
	muted     lipgloss.Style
	primary   lipgloss.Style
	accent    lipgloss.Style
	danger    lipgloss.Style
	success   lipgloss.Style
	warning   lipgloss.Style
	footer    lipgloss.Style
	statusErr lipgloss.Style
	statusOK  lipgloss.Style
}

func newStyles(p theme.Palette) styles {
	fg := func(c color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(c)
	}
	return styles{
		frame: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Border).
			Background(p.Surface).
			Padding(0, 1),
		title:     fg(p.Accent).Bold(true),
		header:    fg(p.Secondary).Bold(true),
		muted:     fg(p.Muted),
		primary:   fg(p.Primary),
		accent:    fg(p.Accent),
		danger:    fg(p.Danger),
		success:   fg(p.Success),
		warning:   fg(p.Warning),
		footer:    fg(p.Muted),
		statusErr: fg(p.Danger),
		statusOK:  fg(p.Muted),
	}
}
