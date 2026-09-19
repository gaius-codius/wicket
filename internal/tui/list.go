package tui

import (
	"fmt"
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
		return m, nil
	case "j", "down":
		if m.cursor < len(ps)-1 {
			m.cursor++
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
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
	if lo.Wide {
		return m.viewListWide(lo, ps)
	}
	selLines := 1
	if !lo.Compact {
		selLines += len(m.details(ps[m.cursor], false))
	}
	start, end := listWindow(len(ps), m.cursor, lo.Budget, selLines)
	nameW, hostW := columnWidths(ps, lo.Inner)
	var lines []string
	for i := start; i < end; i++ {
		p := ps[i]
		switch {
		case i == m.cursor:
			lines = append(lines, m.row(p, true, nameW, hostW, lo.Inner))
			if !lo.Compact {
				for _, d := range m.details(p, false) {
					lines = append(lines, "    "+m.kv(detailLabelWidth, d.key, truncate(d.label, lo.Inner-6-detailLabelWidth)))
				}
			}
		case lo.Compact:
			// Compact shows only names on unselected rows (UX-001).
			lines = append(lines, "  "+m.styles.muted.Render(truncate(p.Name, lo.Inner-2)))
		default:
			lines = append(lines, m.row(p, false, nameW, hostW, lo.Inner))
		}
	}
	return strings.Join(lines, "\n")
}

// viewListWide shows profiles on the left and the selected profile's details
// on the right, so moving the cursor does not reflow the list.
func (m Model) viewListWide(lo layout, ps []config.Profile) string {
	nameW, hostW := columnWidths(ps, lo.Inner/2)
	leftW := 2 + nameW + 2 + hostW
	rightW := lo.Inner - leftW - 3
	start, end := listWindow(len(ps), m.cursor, lo.Budget, 1)
	var left []string
	for i := start; i < end; i++ {
		left = append(left, m.row(ps[i], i == m.cursor, nameW, hostW, leftW))
	}
	p := ps[m.cursor]
	right := []string{m.styles.primary.Bold(true).Render(truncate(p.Name, rightW)), ""}
	for _, d := range m.details(p, true) {
		right = append(right, m.kv(detailLabelWidth, d.key, truncate(d.label, rightW-2-detailLabelWidth)))
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
	text := st.onSelection(st.accent).Render("▌ ") +
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

func (m Model) listHints() []hint {
	if len(m.profiles()) == 0 {
		return []hint{{"n", "new"}, {"?", "help"}, {"q", "quit"}}
	}
	return []hint{{"enter", "connect"}, {"n", "new"}, {"e", "edit"}, {"D", "delete"}, {"?", "help"}, {"q", "quit"}}
}

func (m Model) listContext() string {
	switch n := len(m.profiles()); n {
	case 0:
		return "no connections"
	case 1:
		return "1 connection"
	default:
		return fmt.Sprintf("%d connections", n)
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
