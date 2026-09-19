package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m Model) handleHelpKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "?", "q":
		m.view = m.prev
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) viewHelp(lo layout) string {
	_ = lo
	keys := helpKeys(m.helpFor)
	w := 0
	for _, k := range keys {
		w = max(w, len(k.key))
	}
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = m.styles.key.Render(padRight(k.key, w)) + "  " + m.styles.muted.Render(k.label)
	}
	return strings.Join(lines, "\n")
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
			{"space, enter", "toggle checkbox"},
			{"←/→", "change scale"},
			{"ctrl+s", "save"},
			{"esc", "cancel to list"},
			{"?", "help (off a text field)"},
			{"ctrl+c", "quit without saving"},
		}
	case viewModal:
		return []hint{
			{"enter", "connect once"},
			{"ctrl+s", "save and connect"},
			{"esc", "cancel"},
			{"?", "help"},
		}
	case viewDelete:
		return []hint{
			{"y", "confirm delete"},
			{"n / esc", "cancel"},
			{"?", "help"},
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
			{"enter", "connect"},
			{"n", "new"},
			{"e", "edit selected"},
			{"D", "delete selected"},
			{"?", "help"},
			{"q, ctrl+c", "quit"},
		}
	}
}
