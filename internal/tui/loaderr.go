package tui

import tea "charm.land/bubbletea/v2"

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
	_ = lo
	path := m.loadPath
	if path == "" {
		path = "(unknown path)"
	}
	return m.styles.danger.Render("✗ Cannot load config") + "\n" +
		m.styles.muted.Render(path) + "\n\n" +
		m.styles.primary.Render(m.loadErr)
}
