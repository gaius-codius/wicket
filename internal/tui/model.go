package tui

import (
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/theme"
)

type view int

const (
	viewList view = iota
	viewLoadErr
	viewHelp
	viewForm
	viewDelete
	viewModal
	viewRetry
)

type Model struct {
	app       *App
	styles    styles
	width     int
	height    int
	view      view
	prev      view
	cursor    int
	status    string
	statusErr bool
	helpFor   view
	loadErr   string
	loadPath  string
	quit      bool
	// now drives relative last-used times; nil means time.Now.
	now func() time.Time

	form        formState
	delName     string
	modal       modalState
	retry       retryState
	useOnce     *secret.Password
	useOnceName string
	connecting  bool
}

type Options struct {
	Home       string
	ConfigPath string
	StatePath  string
	Store      secret.Store
	Launcher   *rdp.Launcher
	Term       TerminalController
	Width      int
	Height     int
}

func New(opt Options) Model {
	if opt.Width == 0 {
		opt.Width = 80
	}
	if opt.Height == 0 {
		opt.Height = 24
	}
	home := opt.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	pal, _ := theme.Load(home)
	m := Model{
		styles: newStyles(pal),
		width:  opt.Width,
		height: opt.Height,
		app: &App{
			Secrets:  opt.Store,
			Launcher: opt.Launcher,
			Term:     opt.Term,
		},
	}
	if m.app.Secrets == nil {
		m.app.Secrets = secret.NewMemory()
	}
	paths, err := config.ResolveFromEnv()
	cfgPath, stPath := paths.Config, paths.State
	if opt.ConfigPath != "" {
		cfgPath = opt.ConfigPath
	}
	if opt.StatePath != "" {
		stPath = opt.StatePath
	}
	if err != nil && opt.ConfigPath == "" {
		m.view = viewLoadErr
		m.loadErr = err.Error()
		return m
	}
	cfg, err := config.OpenOrCreate(cfgPath)
	if err != nil {
		m.view = viewLoadErr
		m.loadPath = cfgPath
		m.loadErr = err.Error()
		return m
	}
	st, _ := config.OpenState(stPath, nil)
	m.app.Cfg = cfg
	m.app.State = st
	m.loadPath = cfg.Path()
	if warns := cfg.Warnings(); len(warns) > 0 {
		m.status = strings.Join(warns, "; ")
	}
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case connectDoneMsg:
		return m.handleConnectDone(msg)
	case tea.InterruptMsg:
		if m.connecting {
			return m, nil
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.quit {
		return m, tea.Quit
	}
	if m.connecting {
		return m, nil
	}
	key := msg.String()
	lo := newLayout(m.width, m.height)
	if lo.Tiny && m.view == viewList {
		if key == "q" {
			return m.quitNow()
		}
		return m, nil
	}
	switch m.view {
	case viewHelp:
		return m.handleHelpKey(key)
	case viewLoadErr:
		return m.handleLoadErrKey(key)
	case viewForm:
		return m.handleFormKey(msg, key)
	case viewDelete:
		return m.handleDeleteKey(key)
	case viewModal:
		return m.handleModalKey(msg, key)
	case viewRetry:
		return m.handleRetryKey(key)
	default:
		return m.handleListKey(key)
	}
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m Model) render() string {
	lo := newLayout(m.width, m.height)
	if lo.Tiny && m.view != viewLoadErr {
		return m.styles.muted.Render("resize terminal")
	}
	if lo.Wide && m.view != viewList && m.view != viewRetry {
		// Only the list uses the wide two-pane layout; forms and dialogs
		// read better at the normal width.
		lo.Panel = min(lo.Panel, panelNormal)
		lo.Inner = lo.Panel - 4
	}
	context, hs := m.chrome()
	foot := m.hints(lo.Inner, hs...)
	status := m.statusLines(lo.Inner)
	var retry string
	if m.view == viewRetry {
		retry = m.viewRetry(lo)
	}
	lo.Budget = lo.Height - chromeLines - len(foot) - len(status)
	if retry != "" {
		lo.Budget -= lipgloss.Height(retry) + 1
	}
	lo.Budget = max(lo.Budget, 1)

	var body string
	switch m.view {
	case viewHelp:
		body = m.viewHelp(lo)
	case viewLoadErr:
		body = m.viewLoadError(lo)
	case viewForm:
		body = m.viewForm(lo)
	case viewDelete:
		body = m.viewDelete(lo)
	case viewModal:
		body = m.viewModal(lo)
	case viewRetry:
		body = m.viewList(lo) + "\n\n" + retry
	default:
		body = m.viewList(lo)
	}
	parts := []string{m.header(lo.Inner, context), m.divider(lo.Inner), "", body, ""}
	parts = append(parts, status...)
	parts = append(parts, foot...)
	panel := m.styles.frame.Width(lo.Panel).Render(strings.Join(parts, "\n"))
	return lipgloss.Place(lo.Width, lo.Height, lipgloss.Center, lipgloss.Center, panel)
}

// chrome returns the header context and footer keys for the current view.
func (m Model) chrome() (string, []hint) {
	switch m.view {
	case viewHelp:
		return "keys", []hint{{"esc", "close"}}
	case viewLoadErr:
		return "config error", []hint{{"q", "quit"}, {"?", "help"}}
	case viewForm:
		ctx := "new connection"
		if m.form.oldName != "" {
			ctx = "edit " + m.form.oldName
		}
		return ctx, []hint{{"ctrl+s", "save"}, {"tab", "next field"}, {"esc", "cancel"}, {"?", "help"}}
	case viewDelete:
		return "delete connection", []hint{{"y", "confirm"}, {"n", "cancel"}, {"?", "help"}}
	case viewModal:
		return "password", []hint{{"enter", "connect once"}, {"ctrl+s", "save and connect"}, {"esc", "cancel"}, {"?", "help"}}
	case viewRetry:
		return m.listContext(), []hint{{"enter", "retry"}, {"n", "new password"}, {"esc", "dismiss"}, {"?", "help"}}
	default:
		return m.listContext(), m.listHints()
	}
}

func (m Model) profiles() []config.Profile {
	if m.app == nil || m.app.Cfg == nil {
		return nil
	}
	return m.app.Cfg.Profiles()
}

func (m Model) selected() (config.Profile, bool) {
	ps := m.profiles()
	if m.cursor < 0 || m.cursor >= len(ps) {
		return config.Profile{}, false
	}
	return ps[m.cursor], true
}

func (m Model) quitNow() (tea.Model, tea.Cmd) {
	m.quit = true
	return m, tea.Quit
}

func (m Model) openHelp() (tea.Model, tea.Cmd) {
	m.prev = m.view
	m.helpFor = m.view
	m.view = viewHelp
	return m, nil
}

func (m *Model) setStatus(msg string, isErr bool) {
	m.status = msg
	m.statusErr = isErr
}

func (m *Model) selectName(name string) {
	for i, p := range m.profiles() {
		if p.Name == name {
			m.cursor = i
			return
		}
	}
}

func (m *Model) clampCursor() {
	ps := m.profiles()
	if len(ps) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(ps) {
		m.cursor = len(ps) - 1
	}
}

func (m *Model) clearUseOnce() {
	if m.useOnce != nil {
		m.useOnce.Clear()
		m.useOnce = nil
	}
	m.useOnceName = ""
}
