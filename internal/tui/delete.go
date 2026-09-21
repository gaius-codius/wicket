package tui

import (
	"slices"
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
	m.delName = ""
	m.view = viewList
	m.clearFilter()
	// The selection moves to the profile shown after the deleted one, or
	// before it at the end of the list, found by name in the order the list
	// is drawn in: the cursor is a file index, and the next file index is
	// somewhere else entirely once the list is sorted.
	next := m.neighbour(name)
	id, warns, err := m.app.removeProfile(name)
	m.forgetPresence(id)
	m.refreshUsed()
	if err != nil {
		m.setStatus(err.Error(), statusError)
		return m, nil
	}
	m.selectNameOr(next)
	// The password goes off the update loop: the keyring may be slow, or
	// waiting on an unlock prompt. See keyring.go.
	return m.finishDelete(name, id, warns)
}

// neighbour is the name drawn after name in the list, or before it when name
// is last, or "" when there is no other profile.
func (m Model) neighbour(name string) string {
	ps := m.profiles()
	vis := m.visible()
	pos := slices.IndexFunc(vis, func(i int) bool { return ps[i].Name == name })
	switch {
	case pos < 0 || len(vis) < 2:
		return ""
	case pos+1 < len(vis):
		return ps[vis[pos+1]].Name
	default:
		return ps[vis[pos-1]].Name
	}
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
