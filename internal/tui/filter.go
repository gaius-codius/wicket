package tui

import (
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// visible returns the indexes of profiles that match the filter, in the
// order the list shows them: file order, or most recent first while that sort
// is on. With no filter, every profile is visible.
func (m Model) visible() []int {
	ps := m.profiles()
	q := m.query()
	out := make([]int, 0, len(ps))
	for i, p := range ps {
		if q == "" || matchStart(p.Name, q) >= 0 || matchStart(p.Host, q) >= 0 {
			out = append(out, i)
		}
	}
	if m.sortRecent {
		m.sortByRecent(ps, out)
	}
	return out
}

// query is the filter text the list matches against.
func (m Model) query() string {
	return strings.TrimSpace(m.filter.Value())
}

// matchSpan finds q in s ignoring case and returns the match as rune offsets,
// or -1, -1. It compares rune by rune rather than searching strings.ToLower
// of each: lowering can change a string's length ("İ" becomes two runes), so
// byte offsets found in the lowered copy do not point at the same text in the
// original, and the highlight drawn from them would land on the wrong letters.
func matchSpan(s, q string) (start, end int) {
	qr := []rune(q)
	if len(qr) == 0 {
		return -1, -1
	}
	sr := []rune(s)
	for i := 0; i+len(qr) <= len(sr); i++ {
		if foldEqual(sr[i:i+len(qr)], qr) {
			return i, i + len(qr)
		}
	}
	return -1, -1
}

func matchStart(s, q string) int {
	start, _ := matchSpan(s, q)
	return start
}

// foldEqual reports whether a and b, of equal length, match under Unicode
// simple case folding.
func foldEqual(a, b []rune) bool {
	for i := range a {
		if !runeFoldEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func runeFoldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	for r := unicode.SimpleFold(a); r != a; r = unicode.SimpleFold(r) {
		if r == b {
			return true
		}
	}
	return false
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
	m.clearFilterErr()
}

// setFilterErr and clearFilterErr keep a rejected filter paste from leaving a
// stale error behind, and from wiping a warning the filter did not raise.
func (m *Model) setFilterErr(msg string) {
	if !m.filterErrSet {
		// A second rejection must not save the first one's error as the
		// status to come back to.
		m.filterErr, m.filterErrKind = m.status, m.statusKind
	}
	m.filterErrSet = true
	m.setStatus(msg, statusError)
}

func (m *Model) clearFilterErr() {
	if !m.filterErrSet {
		return
	}
	m.filterErrSet = false
	// The kind comes back with the text: a restored error or warning that
	// lost its marker would read as a routine note.
	m.setStatus(m.filterErr, m.filterErrKind)
	m.filterErr, m.filterErrKind = "", statusInfo
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
		m.clearFilterErr()
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
		m.setFilterErr(err.Error())
		return m, nil
	}
	m.filter = ti
	m.clearFilterErr()
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
	// A page is what is on screen, less one row of overlap, so the reader
	// keeps their place. It used to be half the window, which counted the
	// header, footer and borders as list rows.
	page := max(m.listBudget()-1, 1)
	switch key {
	case "j", "down":
		pos++
	case "k", "up":
		pos--
	case "g", "home":
		pos = 0
	case "G", "shift+g", "end":
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
