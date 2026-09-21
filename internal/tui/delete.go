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
		m.setStatus(err.Error(), statusError)
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
	m.setStatus(outcome("Deleted", name, warns))
	return m, nil
}

// deleteNote is what a delete takes with it, so the user answers knowing.
const deleteNote = "Removes the profile, its last-used time and its saved password. This cannot be undone."

// viewDelete asks the question first and sheds the rest from the bottom: the
// question is what the user is answering, the host confirms which profile it
// is, and the note only explains. Clipping the view from the bottom instead
// left a short terminal asking for a "y" with nothing but an ellipsis to go
// on.
func (m Model) viewDelete(lo layout) string {
	p, ok := m.app.Cfg.Profile(m.delName)
	name, host := m.delName, ""
	if ok {
		name, host = p.Name, p.Host
	}
	// The name and host come from the config and can be longer than the
	// panel, so both are truncated and the note is wrapped.
	const mark, verb = "▲ ", "Delete "
	nameW := max(lo.Inner-lipgloss.Width(mark+verb)-1, 1)
	lines := []string{m.styles.danger.Render(mark) +
		m.styles.primary.Bold(true).Render(truncate(verb+truncate(name, nameW)+"?", max(lo.Inner-2, 1)))}
	if host != "" && lo.Budget > len(lines) {
		lines = append(lines, m.styles.muted.Render(truncate(host, lo.Inner)))
	}
	wrap := lipgloss.NewStyle().Width(lo.Inner)
	note := append([]string{""}, strings.Split(m.styles.muted.Render(wrap.Render(deleteNote)), "\n")...)
	// The note goes whole or not at all, and its gap goes before it does.
	switch room := lo.Budget - len(lines); {
	case room >= len(note):
		lines = append(lines, note...)
	case room == len(note)-1:
		lines = append(lines, note[1:]...)
	}
	return strings.Join(lines, "\n")
}
