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
	case "s":
		if empty {
			return m, nil
		}
		// The cursor names a profile, not a position, so the selection
		// stays on the same profile wherever the sort moves it.
		m.sortRecent = !m.sortRecent
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
	cols := columnWidths(ps, lo.Inner, m.timeWidth(ps))
	// A detail repeats nothing the row already shows: the host and last-used
	// time come back only when the row had no room for them.
	var details []detail
	if hasSel {
		details = m.details(sel, cols.host == 0, cols.time == 0)
	}
	// The selected card's details are trimmed to what is left of the budget,
	// so the footer and frame stay on screen in a short terminal. The
	// password line comes last, so it is the first to go.
	detailBudget := 0
	if hasSel && !lo.Compact {
		detailBudget = min(len(details), max(lo.Budget-1, 0))
	}
	start, end := listWindow(len(vis), max(slices.Index(vis, m.cursor), 0), lo.Budget, detailBudget+1)
	q := m.query()
	var lines []string
	for _, i := range vis[start:end] {
		p := ps[i]
		switch {
		case i == m.cursor:
			lines = append(lines, m.row(p, true, cols, lo.Inner))
			for _, d := range details[:detailBudget] {
				lines = append(lines, "    "+m.detailLine(d, lo.Inner-4))
			}
		case lo.Compact:
			// Compact shows only names on unselected rows (UX-001).
			lines = append(lines, "  "+m.highlight(p.Name, lo.Inner-2, q, m.styles.muted, m.styles.accent.Underline(true)))
		default:
			lines = append(lines, m.row(p, false, cols, lo.Inner))
		}
	}
	return head + strings.Join(lines, "\n")
}

// viewListWide shows profiles on the left and the selected profile's details
// on the right, so moving the cursor does not reflow the list.
func (m Model) viewListWide(lo layout, ps []config.Profile, vis []int, sel config.Profile, hasSel bool) string {
	cols := columnWidths(ps, lo.Inner/2, m.timeWidth(ps))
	leftW := cols.width()
	rightW := lo.Inner - leftW - 3
	start, end := listWindow(len(vis), max(slices.Index(vis, m.cursor), 0), lo.Budget, 1)
	var left []string
	for _, i := range vis[start:end] {
		left = append(left, m.row(ps[i], i == m.cursor, cols, leftW))
	}
	var right []string
	if hasSel {
		right = []string{m.styles.primary.Bold(true).Render(truncate(sel.Name, rightW)), ""}
		for _, d := range m.details(sel, true, cols.time == 0) {
			right = append(right, m.detailLine(d, rightW))
		}
		if cmd := m.shellLine(sel, rightW); cmd != "" {
			right = append(right, "", cmd)
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

// shellLabel introduces the command that connects to a profile without the
// TUI, so the wide pane teaches the CLI in passing.
const shellLabel = "from a shell"

// shellLine renders "from a shell  wicket connect <name>" in width cells, or
// nothing when a command cut to a few letters would say nothing.
func (m Model) shellLine(p config.Profile, width int) string {
	room := width - lipgloss.Width(shellLabel) - 2
	if room < len("wicket connect")+2 {
		return ""
	}
	return m.styles.muted.Render(shellLabel) + "  " +
		m.styles.primary.Render(truncate("wicket connect "+shellQuote(p.Name), room))
}

// shellQuote quotes s for a POSIX shell when it holds anything a shell would
// read specially, so the command shown can be pasted as it is. Names never
// start with "-" (the config refuses them), so no "--" is needed.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+:,./_-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// row renders one profile as "▌ name  host      last used", the time
// right-aligned at width. The selected row carries the accent bar and the
// theme's selection background across the full width. A filter match is
// underlined in the accent colour, on the selection background where there
// is one, so the row keeps its width and its highlight.
func (m Model) row(p config.Profile, selected bool, c columns, width int) string {
	st := m.styles
	on := func(s lipgloss.Style) lipgloss.Style {
		if selected {
			return st.onSelection(s)
		}
		return s
	}
	fill := func(n int) string {
		if n <= 0 {
			return ""
		}
		if !selected {
			return strings.Repeat(" ", n)
		}
		return on(st.primary).Render(strings.Repeat(" ", n))
	}
	q := m.query()
	hl := on(st.accent.Underline(true))
	nameSt := st.primary
	lead := "  "
	if selected {
		nameSt = nameSt.Bold(true)
		lead = st.selectionMark().Render("▌ ")
	}
	text := lead
	name := m.highlight(p.Name, c.name, q, on(nameSt), hl.Bold(selected))
	text += name
	if c.host > 0 {
		text += fill(c.name-lipgloss.Width(name)+2) + m.highlight(p.Host, c.host, q, on(st.muted), hl)
	}
	if c.time > 0 {
		when := truncate(m.lastUsed(p.Name), c.time)
		text += fill(width-lipgloss.Width(text)-lipgloss.Width(when)) + on(st.muted).Render(when)
	}
	return text + fill(width-lipgloss.Width(text))
}

// highlight truncates s to width and styles it with base, drawing the part
// that matches q in hl. Styling never adds or removes a cell, so a highlight
// cannot push a row past its width. The match is found in the whole of s
// and then clipped to what survived truncation.
func (m Model) highlight(s string, width int, q string, base, hl lipgloss.Style) string {
	out := truncate(s, width)
	if out == "" {
		return ""
	}
	start, end := matchSpan(s, q)
	r := []rune(out)
	kept := len(r)
	if out != s {
		kept-- // the ellipsis is not part of s
	}
	end = min(end, kept)
	if start < 0 || start >= end {
		return base.Render(out)
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString(base.Render(string(r[:start])))
	}
	b.WriteString(hl.Render(string(r[start:end])))
	if end < len(r) {
		b.WriteString(base.Render(string(r[end:])))
	}
	return b.String()
}

// columns is the width of each part of a row. A zero host or time means the
// row has no room for it.
type columns struct{ name, host, time int }

// width is the row's width with every column at its size.
func (c columns) width() int {
	w := 2 + c.name
	if c.host > 0 {
		w += 2 + c.host
	}
	if c.time > 0 {
		w += 2 + c.time
	}
	return w
}

// columnWidths sizes the name, host and last-used columns to their longest
// values within width. When space runs short the host goes first: the name
// is what the user picks a row by, and the time is what the sort is by. The
// time only goes when keeping it would squeeze the name below eight cells.
func columnWidths(ps []config.Profile, width, timeW int) columns {
	nameMax, hostMax := 1, 0
	for _, p := range ps {
		nameMax = max(nameMax, lipgloss.Width(p.Name))
		hostMax = max(hostMax, lipgloss.Width(p.Host))
	}
	nameMax = min(nameMax, 24)
	room := max(width-2, 1)
	if timeW > 0 && room-timeW-2 >= min(nameMax, 8) {
		room -= timeW + 2
	} else {
		timeW = 0
	}
	c := columns{name: min(nameMax, room), time: timeW}
	// A host cut to a letter or two says nothing, so it goes entirely.
	if hr := room - c.name - 2; hostMax > 0 && hr >= min(hostMax, 4) {
		c.host = min(hostMax, hr)
	}
	return c
}

// timeWidth is the width of the longest last-used label, so the column does
// not shift as the cursor or filter moves.
func (m Model) timeWidth(ps []config.Profile) int {
	w := 0
	for _, p := range ps {
		w = max(w, lipgloss.Width(m.lastUsed(p.Name)))
	}
	return w
}

// detailLabelWidth fits the longest detail label, "last used".
const detailLabelWidth = 9

// detail is one line of the selected profile's details. style, when set,
// colours the value; otherwise it is drawn in the text colour.
type detail struct {
	key, value string
	style      *lipgloss.Style
}

// detailLine renders d as an aligned label/value row in width cells.
func (m Model) detailLine(d detail, width int) string {
	st := m.styles.primary
	if d.style != nil {
		st = *d.style
	}
	value := truncate(d.value, width-2-detailLabelWidth)
	return m.styles.muted.Render(padRight(d.key, detailLabelWidth)) + "  " + st.Render(value)
}

// details lists the selected profile's fields. The host and last-used time
// are left out when the row already shows them. The password line is last,
// so a short card drops it first.
func (m Model) details(p config.Profile, withHost, withLast bool) []detail {
	var ds []detail
	if withHost {
		ds = append(ds, detail{key: "host", value: p.Host})
	}
	ds = append(ds, detail{key: "user", value: p.User})
	if p.Domain != "" {
		ds = append(ds, detail{key: "domain", value: p.Domain})
	}
	if withLast {
		ds = append(ds, detail{key: "last used", value: m.lastUsed(p.Name)})
	}
	ds = append(ds, detail{key: "display", value: displayLine(p)})
	if p.Client != "" && p.Client != config.DefaultClient {
		ds = append(ds, detail{key: "client", value: p.Client})
	}
	text, st := m.presenceLine(p)
	ds = append(ds, detail{key: "password", value: text, style: &st})
	return ds
}

// lastUsed formats name's last-used time from the snapshot. It never reads
// state.toml: it runs for every row on every frame.
func (m Model) lastUsed(name string) string {
	t, ok := m.used[name]
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
		{"D", "delete", intentNormal}, {"/", "filter", intentNormal}, {"s", "sort", intentNormal},
		{"?", "help", intentNormal}, {"q", "quit", intentNormal}}
}

func (m Model) listContext() string {
	n := len(m.profiles())
	noun := "connections"
	if n == 1 {
		noun = "connection"
	}
	var ctx string
	switch {
	case n == 0:
		return "no connections"
	case m.filterActive():
		ctx = fmt.Sprintf("%d of %d %s", len(m.visible()), n, noun)
	default:
		ctx = fmt.Sprintf("%d %s", n, noun)
	}
	if m.sortRecent {
		ctx += " · recent first"
	}
	return ctx
}

// sortByRecent orders idx, which is in file order, most recently used
// first. Profiles never used go last. The sort is stable, so ties -- and
// every never-used profile -- keep their file order.
func (m Model) sortByRecent(ps []config.Profile, idx []int) {
	slices.SortStableFunc(idx, func(a, b int) int {
		ta, oka := m.used[ps[a].Name]
		tb, okb := m.used[ps[b].Name]
		switch {
		case oka && !okb:
			return -1
		case !oka && okb:
			return 1
		case !oka:
			return 0
		}
		return tb.Compare(ta)
	})
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
