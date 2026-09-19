package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m Model) handleDeleteKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "?":
		return m.openHelp()
	case "n", "esc":
		m.delName = ""
		m.view = viewList
		return m, nil
	case "y", "Y":
		return m.confirmDelete()
	default:
		return m, nil
	}
}

func (m Model) confirmDelete() (tea.Model, tea.Cmd) {
	name := m.delName
	idx := m.cursor
	warns, err := m.app.DeleteProfile(name)
	m.delName = ""
	m.view = viewList
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	ps := m.profiles()
	if len(ps) == 0 {
		m.cursor = 0
	} else if idx >= len(ps) {
		m.cursor = len(ps) - 1
	} else {
		m.cursor = idx
	}
	if len(warns) > 0 {
		m.setStatus(strings.Join(warns, "; "), false)
	} else {
		m.setStatus("", false)
	}
	return m, nil
}

func (m Model) viewDelete(lo layout) string {
	_ = lo
	p, ok := m.app.Cfg.Profile(m.delName)
	name, host := m.delName, ""
	if ok {
		name, host = p.Name, p.Host
	}
	return m.styles.primary.Render("Delete ") + m.styles.primary.Bold(true).Render(name) +
		m.styles.muted.Render(" ("+host+")") + m.styles.primary.Render("?") + "\n" +
		m.styles.muted.Render("Removes the profile, its last-used time, and its stored password.")
}
