package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// hint is one footer entry: a key and what it does.
type hint struct{ key, label string }

// header renders "WICKET" on the left and a muted context string on the right.
func (m Model) header(width int, context string) string {
	title := m.styles.title.Render(truncate("WICKET", width))
	titleW := lipgloss.Width(title)
	if context == "" {
		return title
	}
	right := m.styles.muted.Render(truncate(context, width-titleW-2))
	gap := width - titleW - lipgloss.Width(right)
	if gap < 1 {
		// No room for both. The context is the expendable half.
		return title
	}
	return title + strings.Repeat(" ", gap) + right
}

func (m Model) divider(width int) string {
	return m.styles.divider.Render(strings.Repeat("─", width))
}

// hints renders footer entries as "key label · key label", wrapping between
// entries so no entry is split across lines.
func (m Model) hints(width int, hs ...hint) []string {
	width = max(width, 1)
	sep := m.styles.muted.Render(" · ")
	sepW := lipgloss.Width(sep)
	var lines []string
	var cur string
	curW := 0
	for _, h := range hs {
		// An entry wider than the panel is shortened here. Left alone, the
		// frame folds it onto a second line that the height budget never
		// counted, and the bottom border drops off the screen.
		key, label := h.key, h.label
		if lipgloss.Width(key)+1+lipgloss.Width(label) > width {
			key = truncate(key, width)
			label = truncate(label, max(width-lipgloss.Width(key)-1, 0))
		}
		entry := m.styles.key.Render(key)
		if label != "" {
			entry += " " + m.styles.muted.Render(label)
		}
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
	wrapped := lipgloss.NewStyle().Width(max(width-2, 1)).Render(m.status)
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
