package tui

import (
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
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
		body = m.viewList(lo) + "\n" + m.viewRetry(lo)
	default:
		body = m.viewList(lo)
	}
	title := m.styles.title.Render("WICKET")
	inner := title + "\n" + body
	if m.status != "" {
		st := m.styles.statusOK
		if m.statusErr {
			st = m.styles.statusErr
		}
		inner += "\n" + st.Render(m.status)
	}
	w := lo.Width - 2
	if w < 1 {
		w = 1
	}
	return m.styles.frame.Width(w).Render(inner)
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
