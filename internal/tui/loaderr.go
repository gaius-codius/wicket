package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m Model) handleLoadErrKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m.quitNow()
	case "?":
		return m.openHelp()
	default:
		return m, nil
	}
}

func (m Model) viewLoadError(lo layout) string {
	path := m.loadPath
	if path == "" {
		path = "(unknown path)"
	}
	wrap := lipgloss.NewStyle().Width(lo.Inner)
	return m.styles.danger.Render("✗ Cannot load config") + "\n" +
		m.styles.muted.Render(wrap.Render(path)) + "\n\n" +
		m.styles.primary.Render(wrap.Render(m.loadErr))
}
