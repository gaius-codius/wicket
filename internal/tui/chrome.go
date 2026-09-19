package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// hint is one footer entry: a key and what it does.
type hint struct{ key, label string }

// chromeLines is the fixed vertical cost of the panel around a view body:
// top and bottom border, header, divider, the blank line under the divider,
// and the blank line above the status/footer block.
const chromeLines = 6

// header renders "WICKET" on the left and a muted context string on the right.
func (m Model) header(width int, context string) string {
	title := m.styles.title.Render("WICKET")
	if context == "" {
		return title
	}
	right := m.styles.muted.Render(truncate(context, width-lipgloss.Width(title)-2))
	gap := width - lipgloss.Width(title) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return title + strings.Repeat(" ", gap) + right
}

func (m Model) divider(width int) string {
	return m.styles.divider.Render(strings.Repeat("─", width))
}

// hints renders footer entries as "key label · key label", wrapping between
// entries so no entry is split across lines.
func (m Model) hints(width int, hs ...hint) []string {
	sep := m.styles.muted.Render(" · ")
	sepW := lipgloss.Width(sep)
	var lines []string
	var cur string
	curW := 0
	for _, h := range hs {
		entry := m.styles.key.Render(h.key) + " " + m.styles.muted.Render(h.label)
		w := lipgloss.Width(entry)
		switch {
		case curW == 0:
			cur, curW = entry, w
		case curW+sepW+w <= width:
			cur += sep + entry
			curW += sepW + w
		default:
			lines = append(lines, cur)
			cur, curW = entry, w
		}
	}
	if curW > 0 {
		lines = append(lines, cur)
	}
	return lines
}

// statusLines renders the status message with an error or info marker,
// wrapped to width.
func (m Model) statusLines(width int) []string {
	if m.status == "" {
		return nil
	}
	mark, st := "•", m.styles.muted
	if m.statusErr {
		mark, st = "✗", m.styles.danger
	}
	wrapped := lipgloss.NewStyle().Width(width - 2).Render(m.status)
	var out []string
	for i, ln := range strings.Split(wrapped, "\n") {
		prefix := "  "
		if i == 0 {
			prefix = st.Render(mark) + " "
		}
		out = append(out, prefix+st.Render(strings.TrimRight(ln, " ")))
	}
	return out
}

// kv renders an aligned label/value row for detail panes.
func (m Model) kv(labelWidth int, label, value string) string {
	return m.styles.muted.Render(padRight(label, labelWidth)) + "  " + m.styles.primary.Render(value)
}

// truncate shortens plain text to width cells, ending in "…" when cut.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// padRight pads plain or styled text with spaces to width cells.
func padRight(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}
