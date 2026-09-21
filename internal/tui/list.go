package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
)

func (m Model) handleListKey(key string) (tea.Model, tea.Cmd) {
	ps := m.profiles()
	empty := len(ps) == 0
	switch key {
	case "q":
		return m.quitNow()
	case "?":
		return m.openHelp()
	case "esc":
		if m.filter.Value() != "" {
			m.clearFilter()
		}
		return m, nil
	case "/":
		if empty {
			return m, nil
		}
		return m.startFilter()
	case "j", "down", "k", "up", "g", "home", "G", "shift+g", "end", "pgup", "pgdown":
		m.moveCursor(key)
		return m, nil
	case "n":
		return m.openForm("", config.Profile{
			Client:            config.DefaultClient,
			Scale:             config.DefaultScale,
			DynamicResolution: config.DefaultDynamicResolution,
		})
	case "e":
		if empty {
			return m, nil
		}
		p, ok := m.selected()
		if !ok {
			return m, nil
		}
		return m.openForm(p.Name, p)
	case "D", "shift+d":
		if empty {
			return m, nil
		}
		p, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.delName = p.Name
		m.view = viewDelete
		return m, nil
	case "enter":
		if empty {
			return m, nil
		}
		return m.beginConnect()
	default:
		return m, nil
	}
}

func (m Model) viewList(lo layout) string {
	ps := m.profiles()
	if len(ps) == 0 {
		return m.viewEmpty(lo)
	}
	var head string
	if m.filterActive() {
		head = m.viewFilter(lo) + "\n\n"
		lo.Budget = max(lo.Budget-2, 1)
	}
	vis := m.visible()
	if len(vis) == 0 {
		return head + m.styles.muted.Render("No matches.")
	}
	sel, hasSel := m.selected()
	if lo.Wide {
		return head + m.viewListWide(lo, ps, vis, sel, hasSel)
	}
	// The selected card's details are trimmed to what is left of the budget,
	// so the footer and frame stay on screen in a short terminal.
	detailBudget := 0
	if hasSel && !lo.Compact {
		detailBudget = min(len(m.details(sel, false)), max(lo.Budget-1, 0))
	}
	start, end := listWindow(len(vis), max(slices.Index(vis, m.cursor), 0), lo.Budget, detailBudget+1)
	nameW, hostW := columnWidths(ps, lo.Inner)
	var lines []string
	for _, i := range vis[start:end] {
		p := ps[i]
		switch {
		case i == m.cursor:
			lines = append(lines, m.row(p, true, nameW, hostW, lo.Inner))
			for _, d := range m.details(p, false)[:detailBudget] {
				lines = append(lines, "    "+m.kv(detailLabelWidth, d.key, truncate(d.label, lo.Inner-6-detailLabelWidth)))
			}
		case lo.Compact:
			// Compact shows only names on unselected rows (UX-001).
			lines = append(lines, "  "+m.styles.muted.Render(truncate(p.Name, lo.Inner-2)))
		default:
			lines = append(lines, m.row(p, false, nameW, hostW, lo.Inner))
		}
	}
	return head + strings.Join(lines, "\n")
}

// viewListWide shows profiles on the left and the selected profile's details
// on the right, so moving the cursor does not reflow the list.
func (m Model) viewListWide(lo layout, ps []config.Profile, vis []int, sel config.Profile, hasSel bool) string {
	nameW, hostW := columnWidths(ps, lo.Inner/2)
	leftW := 2 + nameW + 2 + hostW
	rightW := lo.Inner - leftW - 3
	start, end := listWindow(len(vis), max(slices.Index(vis, m.cursor), 0), lo.Budget, 1)
	var left []string
	for _, i := range vis[start:end] {
		left = append(left, m.row(ps[i], i == m.cursor, nameW, hostW, leftW))
	}
	var right []string
	if hasSel {
		right = []string{m.styles.primary.Bold(true).Render(truncate(sel.Name, rightW)), ""}
		for _, d := range m.details(sel, true) {
			right = append(right, m.kv(detailLabelWidth, d.key, truncate(d.label, rightW-2-detailLabelWidth)))
		}
	}
	if len(right) > lo.Budget {
		right = right[:max(lo.Budget, 1)]
	}
	sep := m.styles.divider.Render("│")
	lines := make([]string, max(len(left), len(right)))
	for i := range lines {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines[i] = padRight(l, leftW) + " " + sep + " " + r
	}
	return strings.Join(lines, "\n")
}

// row renders one profile as "▌ name  host". The selected row carries the
// accent bar and the theme's selection background across the full width.
func (m Model) row(p config.Profile, selected bool, nameW, hostW, width int) string {
	name := padRight(truncate(p.Name, nameW), nameW)
	host := truncate(p.Host, hostW)
	if !selected {
		return padRight("  "+m.styles.primary.Render(name)+"  "+m.styles.muted.Render(host), width)
	}
	st := m.styles
	text := st.selectionMark().Render("▌ ") +
		st.onSelection(st.primary.Bold(true)).Render(name) +
		st.onSelection(st.primary).Render("  ") +
		st.onSelection(st.muted).Render(host)
	if gap := width - lipgloss.Width(text); gap > 0 {
		text += st.onSelection(st.primary).Render(strings.Repeat(" ", gap))
	}
	return text
}

// columnWidths sizes the name and host columns to their longest values,
// within width.
func columnWidths(ps []config.Profile, width int) (nameW, hostW int) {
	for _, p := range ps {
		nameW = max(nameW, lipgloss.Width(p.Name))
		hostW = max(hostW, lipgloss.Width(p.Host))
	}
	nameW = min(nameW, 24)
	avail := width - 4 - nameW
	if avail < 8 {
		nameW = max(width/2-2, 4)
		avail = width - 4 - nameW
	}
	return nameW, max(min(hostW, avail), 1)
}

// detailLabelWidth fits the longest detail label, "last used".
const detailLabelWidth = 9

// details lists the selected profile's fields as label/value pairs. The
// single-column layout already shows the host on the row.
func (m Model) details(p config.Profile, withHost bool) []hint {
	var ds []hint
	if withHost {
		ds = append(ds, hint{"host", p.Host})
	}
	ds = append(ds, hint{"user", p.User})
	if p.Domain != "" {
		ds = append(ds, hint{"domain", p.Domain})
	}
	ds = append(ds, hint{"last used", m.lastUsed(p.Name)})
	ds = append(ds, hint{"display", displayLine(p)})
	if p.Client != "" && p.Client != config.DefaultClient {
		ds = append(ds, hint{"client", p.Client})
	}
	return ds
}

func (m Model) lastUsed(name string) string {
	if m.app == nil || m.app.State == nil {
		return "never"
	}
	t, ok := m.app.State.LastUsed(name)
	if !ok {
		return "never"
	}
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	return humanTime(t, now)
}

// humanTime formats t relative to now: "just now", "12 min ago",
// "today 13:21", "yesterday 13:21", "Mon 13:21", "2 Sep", or "2 Sep 2025".
func humanTime(t, now time.Time) string {
	t = t.In(now.Location())
	if d := now.Sub(t); d >= 0 && d < time.Minute {
		return "just now"
	} else if d >= 0 && d < time.Hour {
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	}
	day := func(x time.Time) time.Time {
		y, mo, d := x.Date()
		return time.Date(y, mo, d, 0, 0, 0, 0, x.Location())
	}
	days := int(day(now).Sub(day(t)).Hours()/24 + 0.5)
	switch {
	case days == 0:
		return "today " + t.Format("15:04")
	case days == 1:
		return "yesterday " + t.Format("15:04")
	case days > 1 && days < 7:
		return t.Format("Mon 15:04")
	case t.Year() == now.Year():
		return t.Format("2 Jan")
	default:
		return t.Format("2 Jan 2006")
	}
}

// displayLine summarises the display settings, leaving out an unset size.
func displayLine(p config.Profile) string {
	var parts []string
	if p.Size != "" {
		parts = append(parts, p.Size)
	}
	if p.Fullscreen {
		parts = append(parts, "fullscreen")
	} else {
		parts = append(parts, "window")
	}
	if p.DynamicResolution {
		parts = append(parts, "dynamic resolution")
	}
	parts = append(parts, fmt.Sprintf("scale %d%%", p.Scale))
	return strings.Join(parts, " · ")
}

func (m Model) listHints() []keyHint {
	switch {
	case len(m.profiles()) == 0:
		return []keyHint{{"n", "new", intentPrimary}, {"?", "help", intentNormal}, {"q", "quit", intentNormal}}
	case m.filtering:
		return []keyHint{{"↑/↓", "move", intentNormal}, {"enter", "done", intentPrimary}, {"esc", "clear", intentNormal}}
	case m.filter.Value() != "":
		return []keyHint{{"enter", "connect", intentPrimary}, {"/", "edit filter", intentNormal},
			{"esc", "clear filter", intentNormal}, {"?", "help", intentNormal}, {"q", "quit", intentNormal}}
	}
	return []keyHint{{"enter", "connect", intentPrimary}, {"n", "new", intentNormal}, {"e", "edit", intentNormal},
		{"D", "delete", intentNormal}, {"/", "filter", intentNormal},
		{"?", "help", intentNormal}, {"q", "quit", intentNormal}}
}

func (m Model) listContext() string {
	n := len(m.profiles())
	noun := "connections"
	if n == 1 {
		noun = "connection"
	}
	switch {
	case n == 0:
		return "no connections"
	case m.filterActive():
		return fmt.Sprintf("%d of %d %s", len(m.visible()), n, noun)
	default:
		return fmt.Sprintf("%d %s", n, noun)
	}
}

// listWindow returns a half-open [start, end) of profiles that fit in budget
// lines, always including the selected card (selLines tall; others 1 line).
func listWindow(n, cursor, budget, selLines int) (start, end int) {
	if n <= 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= n {
		cursor = n - 1
	}
	if selLines < 1 {
		selLines = 1
	}
	if budget < 1 {
		budget = 1
	}
	start, end = cursor, cursor+1
	used := selLines
	for used < budget {
		progressed := false
		if start > 0 && used+1 <= budget {
			start--
			used++
			progressed = true
		}
		if end < n && used+1 <= budget {
			end++
			used++
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return start, end
}
