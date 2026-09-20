package tui

import (
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
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
	// helpTop is the first help line shown, so a long key list stays
	// reachable in a short terminal.
	helpTop  int
	loadErr  string
	loadPath string
	quit     bool
	// now drives relative last-used times; nil means time.Now.
	now func() time.Time

	// filter narrows the list by name or host. filtering is true while the
	// filter input has focus.
	filter    textinput.Model
	filtering bool
	// filterErr holds the status message the filter replaced with its own
	// error, so clearing the error restores it.
	filterErr    string
	filterErrSet bool

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
	m.filter = m.newInput("", false)
	m.filter.Placeholder = "name or host"
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
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg)
	}
	return m, nil
}

// handlePaste sends a terminal paste to whichever text input has focus.
func (m Model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.connecting {
		return m, nil
	}
	switch {
	case m.view == viewForm && m.form.textFocused() && !m.form.confirmDiscard:
		m.form.editText(msg)
	case m.view == viewModal && m.modal.focused:
		return m.handleModalKey(msg, "")
	case m.view == viewList && m.filtering:
		return m.handleFilterKey(msg, "")
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
	if key == "ctrl+c" {
		return m.interrupt()
	}
	lo := newLayout(m.width, m.height)
	if lo.Tiny && m.view == viewList {
		if key == "q" {
			return m.quitNow()
		}
		return m, nil
	}
	if m.view == viewList && m.filtering {
		return m.handleFilterKey(msg, key)
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
	lo := m.panelLayout()
	if lo.Tiny && m.view != viewLoadErr {
		return m.styles.muted.Render("resize terminal")
	}
	context, status, foot, c := m.fitChrome(&lo)

	bodyLines := m.bodyLines(lo)
	assemble := func(body []string) string {
		parts := make([]string, 0, len(body)+len(status)+len(foot)+4)
		if c.header {
			parts = append(parts, m.header(lo.Inner, context))
		}
		if c.divider {
			parts = append(parts, m.divider(lo.Inner))
		}
		if c.topGap {
			parts = append(parts, "")
		}
		parts = append(parts, body...)
		if c.gap {
			parts = append(parts, "")
		}
		if c.status {
			parts = append(parts, status...)
		}
		if c.foot {
			parts = append(parts, foot...)
		}
		return m.styles.frame.Width(lo.Panel).Render(strings.Join(parts, "\n"))
	}
	panel := assemble(bodyLines)
	// A body that measures taller than it counted (a wrapped line, say) would
	// still push the border off screen. Give lines back until it fits, rather
	// than cutting the frame.
	for lipgloss.Height(panel) > lo.Height && len(bodyLines) > 1 {
		bodyLines = bodyLines[:len(bodyLines)-1]
		panel = assemble(bodyLines)
	}
	return lipgloss.Place(lo.Width, lo.Height, lipgloss.Center, lipgloss.Center, panel)
}

// panelLayout is the layout the current view is drawn in.
func (m Model) panelLayout() layout {
	lo := newLayout(m.width, m.height)
	if lo.Wide && m.view != viewList && m.view != viewRetry {
		// Only the list uses the wide two-pane layout; forms and dialogs
		// read better at the normal width.
		lo.Panel = min(lo.Panel, panelNormal)
		lo.Inner = max(lo.Panel-4, 1)
	}
	return lo
}

// fitChrome decides which pieces of the panel survive at the current height
// and sets lo.Budget to the lines left for the body.
//
// Chrome is dropped least-useful-first so the body keeps at least one line and
// the panel still fits the window. Without this the frame ran off the bottom
// of any terminal under eight rows, taking the footer and the bottom border
// with it.
func (m Model) fitChrome(lo *layout) (context string, status, foot []string, c chromeParts) {
	context, hs := m.chrome()
	foot = m.hints(lo.Inner, hs...)
	status = m.statusLines(lo.Inner)

	c = chromeParts{header: true, divider: true, topGap: true, gap: true,
		status: len(status) > 0, foot: len(foot) > 0}
	avail := max(lo.Height-2, 1) // the frame's top and bottom border
	// The footer goes before the status line: the keys are in the help view
	// and the README, while a warning that is never drawn is simply lost.
	for _, drop := range []*bool{&c.gap, &c.topGap, &c.divider, &c.foot, &c.status, &c.header} {
		if c.cost(len(status), len(foot))+1 <= avail {
			break
		}
		*drop = false
	}
	lo.Budget = max(avail-c.cost(len(status), len(foot)), 1)
	return context, status, foot, c
}

// listBudget is how many rows the list body gets at the current size. Page
// movement uses it so pgup and pgdn move by what is on screen, rather than by
// half the window, which counted the chrome as list rows.
func (m Model) listBudget() int {
	lo := newLayout(m.width, m.height)
	m.fitChrome(&lo)
	if m.filterActive() {
		lo.Budget = max(lo.Budget-2, 1)
	}
	return lo.Budget
}

// chromeParts records which pieces of the panel survive at the current height.
type chromeParts struct {
	header, divider, topGap, gap, status, foot bool
}

func (c chromeParts) cost(statusLines, footLines int) int {
	n := 0
	for _, on := range []bool{c.header, c.divider, c.topGap, c.gap} {
		if on {
			n++
		}
	}
	if c.status {
		n += statusLines
	}
	if c.foot {
		n += footLines
	}
	return n
}

// bodyLines renders the current view into at most lo.Budget lines.
func (m Model) bodyLines(lo layout) []string {
	if m.view == viewRetry {
		// The overlay is the point of this view, so it takes its lines first
		// and the list underneath gets what is left.
		rl := strings.Split(m.viewRetry(lo), "\n")
		if len(rl) > lo.Budget-1 {
			rl = rl[:max(lo.Budget-1, 0)]
		}
		listLo := lo
		listLo.Budget = max(lo.Budget-len(rl)-1, 1)
		out := strings.Split(m.viewList(listLo), "\n")
		out = append(out, "")
		out = append(out, rl...)
		return clipLines(out, lo.Budget)
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
	default:
		body = m.viewList(lo)
	}
	return clipLines(strings.Split(body, "\n"), lo.Budget)
}

// clipLines trims lines to at most n, marking the cut with an ellipsis so a
// truncated view does not look like the whole of it.
func clipLines(lines []string, n int) []string {
	if n < 1 {
		n = 1
	}
	if len(lines) <= n {
		return lines
	}
	out := append([]string{}, lines[:n]...)
	out[n-1] = "…"
	return out
}

// chrome returns the header context and footer keys for the current view.
func (m Model) chrome() (string, []hint) {
	switch m.view {
	case viewHelp:
		return "keys", []hint{{"↑/↓", "scroll"}, {"esc", "close"}}
	case viewLoadErr:
		return "config error", []hint{{"q", "quit"}, {"?", "help"}}
	case viewForm:
		ctx := "new connection"
		if m.form.oldName != "" {
			ctx = "edit " + m.form.oldName
		}
		if m.form.confirmDiscard {
			return ctx, []hint{{"y", "discard"}, {"n", "keep editing"}}
		}
		hs := []hint{{"ctrl+s", "save"}, {"↑/↓", "move"}, {"esc", "cancel"}}
		if m.form.textFocused() {
			// ? is text while a field has focus, so do not offer it here.
			return ctx, hs
		}
		return ctx, append(hs, hint{"?", "help"})
	case viewDelete:
		return "delete connection", []hint{{"y", "confirm"}, {"n", "cancel"}, {"?", "help"}}
	case viewModal:
		hs := []hint{{"enter", "connect once"}, {"ctrl+s", "save and connect"}, {"esc", "cancel"}}
		if m.modal.focused {
			// ? belongs in the password, so help needs tab first.
			return "password", append(hs, hint{"tab", "more keys"})
		}
		return "password", append(hs, hint{"?", "help"}, hint{"tab", "edit password"})
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

// selected returns the profile under the cursor, if it is visible through
// the filter.
func (m Model) selected() (config.Profile, bool) {
	ps := m.profiles()
	if m.cursor < 0 || m.cursor >= len(ps) || !slices.Contains(m.visible(), m.cursor) {
		return config.Profile{}, false
	}
	return ps[m.cursor], true
}

// interrupt quits from any view on ctrl+c. Nothing is saved: the form and
// password dialog only write on ctrl+s, so typed passwords are dropped.
func (m Model) interrupt() (tea.Model, tea.Cmd) {
	m.form = formState{}
	m.modal = modalState{}
	if m.retry.held != nil {
		m.retry.held.Clear()
	}
	m.retry = retryState{}
	m.clearUseOnce()
	return m.quitNow()
}

func (m Model) quitNow() (tea.Model, tea.Cmd) {
	m.quit = true
	return m, tea.Quit
}

func (m Model) openHelp() (tea.Model, tea.Cmd) {
	m.prev = m.view
	m.helpFor = m.view
	m.helpTop = 0
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

func (m *Model) clearUseOnce() {
	if m.useOnce != nil {
		m.useOnce.Clear()
		m.useOnce = nil
	}
	m.useOnceName = ""
}
