package tui

import (
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/theme"
)

// envTheme overrides [ui] theme in the config, as WICKET_CONFIG overrides the
// config path.
const envTheme = "WICKET_THEME"

// chooseTheme resolves the theme setting and makes the start-up decision. cfg
// is nil when the config could not be loaded; the environment still applies.
// It returns a warning to show on the status line, or "".
func (m *Model) chooseTheme(opt Options, home string, cfg *config.Config) string {
	getenv := opt.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var warns []string
	file := ""
	if cfg != nil {
		v, err := cfg.UITheme()
		if err != nil {
			warns = append(warns, err.Error()+"; using auto")
		}
		file = v
	}
	mode, warn := theme.ResolveMode(getenv(envTheme), file)
	if warn != "" {
		warns = append(warns, warn)
	}
	m.theme = theme.Choose(mode, home)
	if m.theme.Warning != "" {
		warns = append(warns, m.theme.Warning)
	}
	// Only a terminal answers the query. Anything else would leave the
	// request unanswered at best and, at worst, write an escape sequence
	// into a file or pipe.
	m.queryBackground = m.theme.Detect && opt.StdoutIsTerminal != nil && opt.StdoutIsTerminal()
	m.applyLook(m.theme.Look)
	return strings.Join(warns, "; ")
}

// initTheme is the start-up command for the theme: a request for the
// terminal's background colour when the setting needs one. The reply arrives
// as a tea.BackgroundColorMsg; until then, or for good if it never comes, the
// TUI draws in the terminal's own colours, which read on any background.
func (m Model) initTheme() tea.Cmd {
	if !m.queryBackground {
		return nil
	}
	return tea.RequestBackgroundColor
}

// handleBackground adopts the theme that suits the terminal's background.
func (m Model) handleBackground(msg tea.BackgroundColorMsg) (tea.Model, tea.Cmd) {
	if !m.queryBackground {
		return m, nil
	}
	m.applyLook(m.theme.Adapt(msg.Color))
	return m, nil
}

// applyLook rebuilds the styles for look and restyles the text inputs that
// already exist, which copied the old styles when they were made.
func (m *Model) applyLook(look theme.Look) {
	m.look = look
	m.styles = stylesFor(look)
	m.filter.SetStyles(m.styles.input)
	m.modal.ti.SetStyles(m.styles.input)
	for id := range fieldCount {
		if m.form.textValue(id) != nil {
			m.form.inputs[id].SetStyles(m.styles.input)
		}
	}
}
