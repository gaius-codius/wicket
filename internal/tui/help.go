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
	var b strings.Builder
	b.WriteString(m.styles.header.Render("KEYS"))
	b.WriteByte('\n')
	b.WriteString(m.styles.muted.Render(helpBody(m.helpFor)))
	b.WriteByte('\n')
	b.WriteString(m.styles.footer.Render("[?] close  [esc] close  [q] close"))
	return b.String()
}

func helpBody(v view) string {
	switch v {
	case viewLoadErr:
		return strings.Join([]string{
			"q  quit",
			"?  help",
		}, "\n")
	case viewForm:
		return strings.Join([]string{
			"tab / shift+tab  move fields",
			"ctrl+s           save",
			"esc / q          cancel to list",
			"?                help",
		}, "\n")
	case viewModal:
		return strings.Join([]string{
			"enter   connect once",
			"ctrl+s  save and connect",
			"esc     cancel",
			"?       help",
		}, "\n")
	case viewDelete:
		return strings.Join([]string{
			"y      confirm delete",
			"n/esc  cancel",
			"?      help",
		}, "\n")
	case viewRetry:
		return strings.Join([]string{
			"enter  retry",
			"n      new password",
			"esc    dismiss",
			"?      help",
		}, "\n")
	default:
		return strings.Join([]string{
			"j/k, arrows  move",
			"enter        connect",
			"n            new",
			"e            edit selected",
			"D            delete selected",
			"?            help",
			"q            quit",
			"esc          no-op",
		}, "\n")
	}
}
