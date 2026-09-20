package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	m.clearFilter()
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
	m.setStatus(strings.Join(warns, "; "), len(warns) > 0)
	return m, nil
}

func (m Model) viewDelete(lo layout) string {
	p, ok := m.app.Cfg.Profile(m.delName)
	name, host := m.delName, ""
	if ok {
		name, host = p.Name, p.Host
	}
	wrap := lipgloss.NewStyle().Width(lo.Inner)
	// The name and host come from the config and can be longer than the
	// panel, so the question is truncated and the note is wrapped.
	head := truncate("Delete "+name+" ("+host+")?", lo.Inner)
	lines := []string{m.styles.primary.Render(head)}
	// The question is what the user is answering, so the note gives up its
	// lines first. Clipping the view from the bottom instead left a short
	// terminal asking for a "y" with nothing but an ellipsis to go on.
	note := strings.Split(m.styles.muted.Render(wrap.Render(
		"Removes the profile, its last-used time, and its stored password.")), "\n")
	if room := lo.Budget - len(lines); room > 0 {
		lines = append(lines, clipLines(note, room)...)
	}
	return strings.Join(lines, "\n")
}
