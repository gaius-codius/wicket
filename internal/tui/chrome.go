package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// hint is a key and what it does: a help entry or a detail row.
type hint struct{ key, label string }

// intent says how a footer key is drawn. It is set where the key is offered,
// never guessed from the key text: y deletes in one dialog and merely
// discards an edit in another.
type intent int

const (
	intentNormal intent = iota
	// intentPrimary marks the key that does what the view is for.
	intentPrimary
	// intentDanger marks a key that destroys something that cannot be
	// brought back.
	intentDanger
)

// keyHint is one footer entry.
type keyHint struct {
	key, label string
	intent     intent
}

// brandMark is the header's name. The mark carries the brand colour, which
// no other element uses, so the header is recognisable in any theme.
const brandMark = "◧ wicket"

// header renders the brand on the left and the context on the right.
func (m Model) header(width int, context string) string {
	title := m.styles.brand.Render(truncate(brandMark, width))
	titleW := lipgloss.Width(title)
	if context == "" {
		return title
	}
	right := m.headerContext(context, width-titleW-2)
	gap := width - titleW - lipgloss.Width(right)
	if right == "" || gap < 1 {
		// No room for both. The context is the expendable half.
		return title
	}
	return title + strings.Repeat(" ", gap) + right
}

// headerContext draws the context in room cells.
func (m Model) headerContext(context string, room int) string {
	if room <= 0 {
		return ""
	}
	return m.contextStyle().Render(truncate(context, room))
}

func (m Model) divider(width int) string {
	return m.styles.divider.Render(strings.Repeat("─", width))
}

// contextStyle colours the header context: a pending delete says so in the
// danger colour, since it is the one view whose answer cannot be undone.
func (m Model) contextStyle() lipgloss.Style {
	if m.view == viewDelete {
		return m.styles.danger
	}
	return m.styles.muted
}

// hints renders footer entries as "key label · key label", wrapping between
// entries so no entry is split across lines.
func (m Model) hints(width int, hs ...keyHint) []string {
	width = max(width, 1)
	sep := m.styles.divider.Render(" · ")
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
		keyStyle, labelStyle := m.hintStyles(h.intent)
		entry := keyStyle.Render(key)
		if label != "" {
			entry += " " + labelStyle.Render(label)
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

// hintStyles returns the key and label styles for a footer entry. The
// primary and danger labels are in the text colour rather than muted, so the
// key the view is for reads first.
func (m Model) hintStyles(in intent) (key, label lipgloss.Style) {
	switch in {
	case intentPrimary:
		return m.styles.accent.Bold(true), m.styles.primary
	case intentDanger:
		return m.styles.danger.Bold(true), m.styles.primary
	default:
		return m.styles.key, m.styles.muted
	}
}

// statusKind is what the status line reports, and picks its marker.
type statusKind int

const (
	// statusInfo is a routine note.
	statusInfo statusKind = iota
	// statusSuccess reports something that fully happened. It is never
	// used when any part of the work failed.
	statusSuccess
	// statusWarning is a setting or condition worth fixing that did not
	// stop anything.
	statusWarning
	// statusError is something that did not happen.
	statusError
)

// statusMark returns the marker and style for a status of kind k.
func (m Model) statusMark(k statusKind) (string, lipgloss.Style) {
	switch k {
	case statusSuccess:
		return "✓", m.styles.success
	case statusWarning:
		return "▲", m.styles.warning
	case statusError:
		return "✗", m.styles.danger
	default:
		return "•", m.styles.muted
	}
}

// statusLines renders the status message with its marker, wrapped to width.
func (m Model) statusLines(width int) []string {
	if m.status == "" {
		return nil
	}
	mark, st := m.statusMark(m.statusKind)
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
