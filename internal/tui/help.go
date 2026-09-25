package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m Model) handleHelpKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "?", "q":
		m.view = m.prev
		m.helpTop = 0
		return m, nil
	case "j", "down":
		m.helpTop++
	case "k", "up":
		m.helpTop--
	case "g", "home":
		m.helpTop = 0
	case "G", "shift+g", "end", "pgdown":
		m.helpTop = len(helpKeys(m.helpFor))
	case "pgup":
		m.helpTop = 0
	}
	return m, nil
}

// helpLines renders the key list one entry per line, truncated to width so a
// narrow panel does not wrap an entry onto a second row.
func (m Model) helpLines(width int) []string {
	keys := helpKeys(m.helpFor)
	w := 0
	for _, k := range keys {
		w = max(w, len(k.key))
	}
	w = min(w, max(width-4, 1))
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = m.styles.key.Render(padRight(truncate(k.key, w), w)) + "  " +
			m.styles.muted.Render(truncate(k.label, max(width-w-2, 1)))
	}
	return lines
}

// viewHelp shows the key list, scrolled so every entry is reachable even when
// the panel is shorter than the list.
func (m Model) viewHelp(lo layout) string {
	lines := m.helpLines(lo.Inner)
	budget := max(lo.Budget, 1)
	if len(lines) <= budget {
		return strings.Join(lines, "\n")
	}
	top := min(max(m.helpTop, 0), len(lines)-budget)
	return strings.Join(lines[top:top+budget], "\n")
}

func helpKeys(v view) []hint {
	switch v {
	case viewLoadErr:
		return []hint{{"q", "quit"}, {"?", "help"}}
	case viewForm:
		return []hint{
			{"↑/↓, tab", "move fields"},
			{"←/→, home/end", "move in text"},
			{"ctrl+w / ctrl+u", "delete word / line"},
			{"space, enter", "switch on/off"},
			{"←/→, h/l", "change scale or client"},
			{"ctrl+s", "save"},
			{"esc", "cancel; asks first if changed"},
			{"?", "help (off a text field)"},
			{"ctrl+c", "quit; asks first if changed"},
		}
	case viewModal:
		return []hint{
			{"enter", "connect once"},
			{"ctrl+s", "save and connect"},
			{"tab", "leave the password field"},
			{"esc", "cancel"},
			{"?", "help (off the password field)"},
		}
	case viewDelete:
		return []hint{
			{"y", "confirm delete"},
			{"n / esc", "cancel"},
			{"?", "help"},
		}
	case viewSession:
		return []hint{
			{"ctrl+c", "stop the session"},
			{"ctrl+c again", "force it to stop"},
		}
	case viewRetry:
		return []hint{
			{"enter", "retry"},
			{"n", "new password"},
			{"esc", "dismiss"},
			{"?", "help"},
		}
	default:
		return []hint{
			{"j/k, arrows", "move"},
			{"g/G, home/end", "first / last"},
			{"pgup/pgdn", "page"},
			{"/", "filter by name or host"},
			{"s", "sort: recent first / file order"},
			{"enter", "connect (ctrl+c stops the session)"},
			{"n", "new"},
			{"e", "edit selected"},
			{"y", "copy selected to a new profile"},
			{"D", "delete selected"},
			{"?", "help"},
			{"q, ctrl+c", "quit"},
		}
	}
}
