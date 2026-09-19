package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// visible returns the indexes of profiles that match the filter, in file
// order. With no filter, every profile is visible.
func (m Model) visible() []int {
	ps := m.profiles()
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	out := make([]int, 0, len(ps))
	for i, p := range ps {
		if q == "" || strings.Contains(strings.ToLower(p.Name), q) || strings.Contains(strings.ToLower(p.Host), q) {
			out = append(out, i)
		}
	}
	return out
}

func (m Model) filterActive() bool {
	return m.filtering || m.filter.Value() != ""
}

// startFilter focuses the filter input, keeping any existing query.
func (m Model) startFilter() (tea.Model, tea.Cmd) {
	m.filtering = true
	m.filter.Focus()
	m.filter.CursorEnd()
	return m, nil
}

func (m *Model) clearFilter() {
	m.filtering = false
	m.filter.Blur()
	m.filter.SetValue("")
}

// handleFilterKey edits the filter while it has focus. Arrows move through
// the matches; enter keeps the filter and returns to the list keys; esc
// clears it.
func (m Model) handleFilterKey(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.clearFilter()
		return m, nil
	case "enter":
		m.filtering = false
		m.filter.Blur()
		if m.filter.Value() == "" {
			m.clearFilter()
		}
		return m, nil
	case "up", "down", "pgup", "pgdown":
		m.moveCursor(key)
		return m, nil
	}
	ti, err := updateInput(m.filter, msg, false)
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	m.filter = ti
	if vis := m.visible(); len(vis) > 0 && !slices.Contains(vis, m.cursor) {
		m.cursor = vis[0]
	}
	return m, nil
}

// moveCursor applies a list movement key to the visible profiles.
func (m *Model) moveCursor(key string) {
	vis := m.visible()
	if len(vis) == 0 {
		return
	}
	pos := slices.Index(vis, m.cursor)
	if pos < 0 {
		m.cursor = vis[0]
		return
	}
	page := max(m.height/2, 1)
	switch key {
	case "j", "down":
		pos++
	case "k", "up":
		pos--
	case "g", "home":
		pos = 0
	case "G", "end":
		pos = len(vis) - 1
	case "pgdown":
		pos += page
	case "pgup":
		pos -= page
	}
	m.cursor = vis[min(max(pos, 0), len(vis)-1)]
}

// viewFilter renders the filter line shown above the list.
func (m Model) viewFilter(lo layout) string {
	prefix := m.styles.accent.Render("/ ")
	if m.filtering {
		return prefix + inputView(m.filter, lo.Inner-2)
	}
	return prefix + m.styles.primary.Render(truncate(m.filter.Value(), lo.Inner-2))
}
