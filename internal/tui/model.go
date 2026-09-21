package tui

import (
	"os"
	"slices"
	"strings"
	"syscall"
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
	viewSession
)

type Model struct {
	app        *App
	styles     styles
	width      int
	height     int
	view       view
	prev       view
	cursor     int
	status     string
	statusKind statusKind
	// statusCarried is set when the status was there before the dialog on
	// screen opened: an error from something else, which the dialog's own
	// question, field and keys outrank.
	statusCarried bool
	helpFor       view
	// helpTop is the first help line shown, so a long key list stays
	// reachable in a short terminal.
	helpTop  int
	loadErr  string
	loadPath string
	quit     bool
	// stoppedBy is the SIGTERM or SIGHUP that ended Wicket, if one did.
	stoppedBy syscall.Signal
	// now drives relative last-used times; nil means time.Now.
	now func() time.Time

	// filter narrows the list by name or host. filtering is true while the
	// filter input has focus.
	filter    textinput.Model
	filtering bool
	// filterErr holds the status message the filter replaced with its own
	// error, so clearing the error restores it.
	filterErr     string
	filterErrKind statusKind
	filterErrSet  bool

	// sortRecent lists the most recently used profiles first. It lasts
	// for the session only.
	sortRecent bool
	// used is a snapshot of every last-used time. Rows and the sort read it
	// on every frame, so it is taken once and refreshed only after something
	// that can change it -- a connect, save or delete -- rather than
	// rereading state.toml for every row. usedStamp is the file it was read
	// from, so a poll can tell when another Wicket has written it.
	used      map[string]time.Time
	usedStamp config.StateStamp
	// usedPollIdle is set when the poll for other writers stopped because the
	// list was hidden; showing the list again restarts it. Only a poll that
	// has run can stop, so a model nobody called Init on never polls.
	usedPollIdle bool
	// presence caches whether each identity has a password in the keyring.
	// It is filled asynchronously; the list only ever reads it.
	presence    map[secret.Identity]presenceEntry
	presenceSeq int

	form        formState
	delName     string
	modal       modalState
	retry       retryState
	useOnce     *secret.Password
	useOnceName string
	// session is the client running now, drawn by the session view. Only
	// Ctrl+C does anything while it is set.
	session    *sessionState
	sessionSeq int
	// keyring is the keyring operation running off the loop, if any; while
	// it is set only esc and ctrl+c do anything. See keyring.go.
	keyring    *keyringOp
	keyringSeq int

	// theme is the start-up theme decision and look what is drawn now; a
	// background colour reply from the terminal can replace look.
	theme theme.Setup
	look  theme.Look
	// queryBackground is set when Init asks the terminal for its
	// background, and gates the reply.
	queryBackground bool
}

type Options struct {
	Home       string
	ConfigPath string
	StatePath  string
	Store      secret.Store
	Launcher   *rdp.Launcher
	Width      int
	Height     int
	// Getenv reads WICKET_THEME; nil means os.Getenv.
	Getenv func(string) string
	// StdoutIsTerminal reports whether the output is a terminal that can be
	// asked for its background colour. nil means it cannot, so nothing is
	// asked unless the caller knows better.
	StdoutIsTerminal func() bool
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
	m := Model{
		width:  opt.Width,
		height: opt.Height,
		app: &App{
			Secrets:  opt.Store,
			Launcher: opt.Launcher,
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
		if warn := m.chooseTheme(opt, home, nil); warn != "" {
			m.setStatus(warn, statusWarning)
		}
		return m
	}
	cfg, err := config.OpenOrCreate(cfgPath)
	if err != nil {
		m.view = viewLoadErr
		m.loadPath = cfgPath
		m.loadErr = err.Error()
		if warn := m.chooseTheme(opt, home, nil); warn != "" {
			m.setStatus(warn, statusWarning)
		}
		return m
	}
	st, _ := config.OpenState(stPath, nil)
	m.app.Cfg = cfg
	m.app.State = st
	m.refreshUsed()
	m.loadPath = cfg.Path()
	if warns := cfg.Warnings(); len(warns) > 0 {
		m.setStatus(strings.Join(warns, "; "), statusInfo)
	}
	if warn := m.chooseTheme(opt, home, cfg); warn != "" {
		// A theme setting that cannot be honoured is a warning: Wicket
		// still runs, in colours the user did not ask for. It outranks
		// the config notes it is joined to.
		m.setStatus(strings.TrimPrefix(m.status+"; "+warn, "; "), statusWarning)
	}
	return m
}

func (m Model) Init() tea.Cmd {
	// The first poll runs at once, so it is the tick it schedules, not this
	// command, that waits.
	return tea.Batch(m.initTheme(), func() tea.Msg { return usedPollMsg{} })
}

// Update handles msg, then starts a keyring check for the selected profile if
// the message changed the selection to one the list knows nothing about.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := m.update(msg)
	next, ok := nm.(Model)
	if !ok {
		return nm, cmd
	}
	next.scopeStatus(m)
	next, poll := next.resumeUsedPoll(m.listShown())
	next, check := next.ensurePresence()
	return next, tea.Batch(cmd, poll, check)
}

// scopeStatus drops a stale note when a form or dialog opens over the list.
// "✓ Deleted work." under "▲ Delete newbox?" reads as an answer to the
// question on screen. Only notes and successes go: a warning or an error
// is something still to act on, and one the user may not have read yet. A
// status the opening itself set is left alone.
func (m *Model) scopeStatus(before Model) {
	opened := m.view != before.view && isDialog(m.view) && !isDialog(before.view) && before.view != viewHelp
	if !opened || m.status != before.status || m.statusKind != before.statusKind {
		return
	}
	if m.statusKind == statusInfo || m.statusKind == statusSuccess {
		m.setStatus("", statusInfo)
		return
	}
	m.statusCarried = true
}

// isDialog reports whether v is drawn over the list and answers to it.
func isDialog(v view) bool {
	return v == viewForm || v == viewDelete || v == viewModal
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.BackgroundColorMsg:
		return m.handleBackground(msg)
	case sessionEndedMsg:
		return m.handleSessionEnded(msg)
	case sessionRecordedMsg:
		return m.handleSessionRecorded(msg)
	case sessionTickMsg:
		return m.handleSessionTick(msg)
	case sessionEscalateMsg:
		return m.handleEscalate(msg)
	case signalMsg:
		return m.handleSignal(msg)
	case presenceMsg:
		return m.handlePresence(msg)
	case keyringReplyMsg:
		return m.handleKeyringReply(msg)
	case keyringSlowMsg:
		return m.handleKeyringSlow(msg)
	case usedPollMsg:
		return m.handleUsedPoll()
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg)
	}
	return m, nil
}

// handlePaste sends a terminal paste to whichever text input has focus.
func (m Model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.session != nil || m.keyring != nil {
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
	key := msg.String()
	if m.session != nil {
		return m.handleSessionKey(key)
	}
	if m.keyring != nil {
		return m.handleKeyringKey(key)
	}
	if key == "ctrl+c" {
		if m.formAtStake() {
			// Unsaved typing is asked about, as esc asks; a second
			// ctrl+c, while asking, quits.
			m.view = viewForm
			m.form.confirmDiscard, m.form.quitOnDiscard = true, true
			return m, nil
		}
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
	if m.view == viewList {
		// Only now is the list's window known, and with it whether the
		// header should say that more rows exist.
		context = m.listContextFit(lo, lo.Inner-lipgloss.Width(brandMark)-2)
	}

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
// Chrome is dropped least-useful-first, and only while the body is short of
// what it needs at that rank (see bodyNeeds): the spacing and the divider go
// while the body lacks any of its content, the footer -- first cut to one
// line, then entirely -- while it lacks what it cannot do without, and the
// status line and header only to keep a single line of body. Without this the
// frame ran off the bottom of any terminal under eight rows; with only the
// last rule, a short terminal kept its spacing and a four-line footer and
// showed one profile.
func (m Model) fitChrome(lo *layout) (context string, status, foot []string, c chromeParts) {
	context, hs := m.chrome()
	foot = m.hints(lo.Inner, hs...)
	status = m.statusLines(lo.Inner)
	// An error left from before a dialog opened is kept, since it may not
	// have been read, but it is not the dialog's: it gets one line, and goes
	// before the dialog's keys do. Wrapped whole, three lines of "Deleted
	// testnet, but…" once left "Delete work?" without its host or its y/n.
	carried := m.statusCarried && isDialog(m.view)
	if carried && len(status) > 1 {
		mark, st := m.statusMark(m.statusKind)
		status = []string{st.Render(mark) + " " + st.Render(truncate(m.status, max(lo.Inner-2, 1)))}
	}

	c = chromeParts{header: true, divider: true, topGap: true, gap: true,
		status: len(status) > 0, foot: len(foot) > 0}
	avail := max(lo.Height-2, 1) // the frame's top and bottom border
	room := func() int { return avail - c.cost(len(status), len(foot)) }
	want, keep, core := m.bodyNeeds(*lo)
	short := m.shortHints(lo.Inner, hs)
	// The footer goes before the status line: the keys are in the help view
	// and the README, while a warning that is never drawn is simply lost.
	steps := []struct {
		need int
		drop func()
	}{
		{want, func() { c.gap = false }},
		{want, func() { c.topGap = false }},
		{want, func() { c.divider = false }},
		{keep, func() {
			if carried {
				c.status = false
			}
		}},
		{keep, func() {
			if c.foot && len(short) < len(foot) {
				foot = short
			} else {
				c.foot = false
			}
		}},
		{keep, func() { c.foot = false }},
		{core, func() { c.status = false }},
		{core, func() { c.header = false }},
	}
	for _, st := range steps {
		if room() >= max(st.need, 1) {
			break
		}
		st.drop()
	}
	lo.Budget = max(room(), 1)
	return context, status, foot, c
}

// bodyNeeds is what the current view's body asks of the chrome, in three
// ranks: want is everything worth a line of spacing or the divider, keep is
// what is worth the footer, and core is what is worth the status line and
// the header. Each is at least one line and want >= keep >= core.
func (m Model) bodyNeeds(lo layout) (want, keep, core int) {
	switch m.view {
	case viewList:
		if len(m.profiles()) == 0 {
			break
		}
		lo.Budget = 1 << 16
		pl := m.planList(lo)
		// The filter line and the selected match are the core: typing into
		// a filter nobody can see, or at matches nobody can see, is blind.
		filter := btoi(m.filterActive())
		rows := max(min(len(pl.vis), listKeepRows), 1)
		return filter + rows + len(pl.details), filter + rows, filter + 1
	case viewForm:
		// The focused row (and a pending "discard?"), then a scroll cue
		// either side and the help line.
		c := 1 + btoi(m.form.confirmDiscard)
		return c + 3, c, c
	case viewDelete:
		// The question, then the host that says which profile it means;
		// the explanation below them is worth the spacing, not the keys.
		note := len(strings.Split(lipgloss.NewStyle().Width(lo.Inner).Render(deleteNote), "\n"))
		if p, ok := m.app.Cfg.Profile(m.delName); ok && p.Host != "" {
			return 2 + note, 2, 1
		}
		return 1 + note, 1, 1
	case viewSession:
		// Who is connected and for how long; the note only explains, and
		// is drawn under a gap of its own.
		if m.session != nil {
			note := len(strings.Split(lipgloss.NewStyle().Width(max(lo.Inner, 1)).Render(sessionNote), "\n"))
			return 3 + note, 1, 1
		}
	case viewRetry:
		// What happened, how the client exited, what to do about a known
		// fullscreen failure and what the client said outrank the spacing
		// and the divider, and are worth the long footer; the password hint
		// and the list row under the overlay only want them.
		msg, detail, fullscreen, note, hint := m.retryBlocks(lo)
		keep := len(msg) + len(detail) + len(fullscreen) + len(note)
		return keep + len(hint) + 2, keep, len(msg)
	}
	return 1, 1, 1
}

// listBudget is how many rows the list shows at the current size. Page
// movement uses it so pgup and pgdn move by what is on screen, rather than by
// half the window, which counted the chrome as list rows.
func (m Model) listBudget() int {
	lo := newLayout(m.width, m.height)
	m.fitChrome(&lo)
	pl := m.planList(lo)
	return max(pl.end-pl.start, 1)
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
		// and the list underneath gets what is left: at least a row and the
		// gap above the overlay, or nothing at all.
		rl := m.viewRetry(lo, lo.Budget)
		if lo.Budget-len(rl) < 2 {
			// No room for a row and the gap above the overlay: the overlay
			// alone. Holding two lines back for the list, as this once did,
			// cost the exit status and the client's error.
			return clipLines(rl, lo.Budget)
		}
		listLo := lo
		listLo.Budget = lo.Budget - len(rl) - 1
		out := clipLines(strings.Split(m.viewList(listLo), "\n"), listLo.Budget)
		out = append(out, "")
		out = append(out, rl...)
		return clipLines(out, lo.Budget)
	}
	if m.view == viewSession {
		return clipLines(m.viewSession(lo), lo.Budget)
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

// fit returns as much of lines as room allows, and nothing at all when the
// only thing that would survive is the ellipsis clipLines leaves behind: a
// lone "…" spends a line of a short panel saying nothing.
func fit(lines []string, room int) []string {
	if room <= 0 || (room == 1 && len(lines) > 1) {
		return nil
	}
	return clipLines(lines, room)
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
// Each key's intent is set here, by what it does in this view.
func (m Model) chrome() (string, []keyHint) {
	if m.keyring != nil {
		// Waiting on the keyring, the only keys are the ones that stop it.
		w := m
		w.keyring = nil
		ctx, _ := w.chrome()
		return ctx, m.keyringHints()
	}
	switch m.view {
	case viewHelp:
		return "keys", []keyHint{{"↑/↓", "scroll", intentNormal}, {"esc", "close", intentNormal}}
	case viewLoadErr:
		return "config error", []keyHint{{"q", "quit", intentNormal}, {"?", "help", intentNormal}}
	case viewForm:
		ctx := "new connection"
		if m.form.oldName != "" {
			ctx = "edit " + m.form.oldName
		}
		if m.form.confirmDiscard {
			// Discarding an edit loses typing, not a saved profile, so y
			// is not drawn as a danger.
			if m.form.quitOnDiscard {
				return ctx, []keyHint{{"y", "discard and quit", intentNormal}, {"n", "keep editing", intentNormal}}
			}
			return ctx, []keyHint{{"y", "discard", intentNormal}, {"n", "keep editing", intentNormal}}
		}
		hs := []keyHint{{"ctrl+s", "save", intentPrimary}, {"↑/↓", "move", intentNormal}, {"esc", "cancel", intentNormal}}
		if m.form.textFocused() {
			// ? is text while a field has focus, so do not offer it here.
			return ctx, hs
		}
		return ctx, append(hs, keyHint{"?", "help", intentNormal})
	case viewDelete:
		return "delete", []keyHint{{"y", "delete", intentDanger}, {"n", "cancel", intentNormal}, {"?", "help", intentNormal}}
	case viewModal:
		hs := []keyHint{{"enter", "connect once", intentPrimary}}
		if m.modal.canSave() {
			hs = append(hs, keyHint{"ctrl+s", "save and connect", intentNormal})
		}
		hs = append(hs, keyHint{"esc", "cancel", intentNormal})
		if m.modal.focused {
			// ? belongs in the password, so help needs tab first.
			return "password", append(hs, keyHint{"tab", "more keys", intentNormal})
		}
		return "password", append(hs, keyHint{"?", "help", intentNormal}, keyHint{"tab", "edit password", intentNormal})
	case viewSession:
		return "session open", m.sessionHints()
	case viewRetry:
		return m.listContext(), []keyHint{{"enter", "retry", intentPrimary}, {"n", "new password", intentNormal},
			{"esc", "dismiss", intentNormal}, {"?", "help", intentNormal}}
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
	if m.session != nil && m.session.held != nil {
		// Run stops the client on the way out; its password goes now.
		m.session.held.Clear()
	}
	return m.quitNow()
}

// formAtStake reports whether ctrl+c would throw away form changes nobody
// has been asked about: the form, or its help, is up with unsaved edits and
// no "discard?" pending.
func (m Model) formAtStake() bool {
	onForm := m.view == viewForm || (m.view == viewHelp && m.prev == viewForm)
	return onForm && m.form.dirty() && !m.form.confirmDiscard
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

func (m *Model) setStatus(msg string, kind statusKind) {
	m.status = msg
	m.statusKind = kind
	m.statusCarried = false
}

// statusNameWidth caps a profile name quoted on the status line, so a long
// name does not wrap the line on its own.
const statusNameWidth = 32

// outcome is the status after a save or delete of name. Success is claimed
// only when nothing went wrong; otherwise the line says what did happen and
// then what did not, with the error marker, since each warning is part of
// the request that failed.
func outcome(done, name string, warns []string) (string, statusKind) {
	msg := done + " " + truncate(name, statusNameWidth)
	if len(warns) == 0 {
		return msg + ".", statusSuccess
	}
	return msg + ", but " + strings.Join(warns, "; "), statusError
}

func (m *Model) selectName(name string) {
	for i, p := range m.profiles() {
		if p.Name == name {
			m.cursor = i
			return
		}
	}
}

// refreshUsed retakes the last-used snapshot.
func (m *Model) refreshUsed() {
	m.used, m.usedStamp = nil, config.StateStamp{}
	if m.app != nil && m.app.State != nil {
		// Stamp first: a write between the two is then seen by the next poll.
		m.usedStamp = m.app.State.Stamp()
		m.used = m.app.State.Snapshot()
	}
}

// refreshUsedIfChanged retakes the snapshot only when state.toml has changed
// since the last one, so polling costs a stat rather than a parse.
func (m *Model) refreshUsedIfChanged() {
	if m.app != nil && m.app.State != nil && m.app.State.Stamp() != m.usedStamp {
		m.refreshUsed()
	}
}

// usedPollEvery is how often the visible list checks state.toml for a connect
// made by another Wicket, such as `wicket connect work` in another shell.
const usedPollEvery = 5 * time.Second

// usedPollMsg asks the model to check state.toml for other writers.
type usedPollMsg struct{}

func usedPoll() tea.Cmd {
	return tea.Tick(usedPollEvery, func(time.Time) tea.Msg { return usedPollMsg{} })
}

// listShown reports whether last-used times are on screen: the list, or the
// retry overlay drawn over it.
func (m Model) listShown() bool {
	return m.view == viewList || m.view == viewRetry
}

// handleUsedPoll refreshes a stale snapshot and keeps polling while the list
// is shown. Once it is not, the poll stops rather than waking every few
// seconds for nothing; resumeUsedPoll starts it again.
func (m Model) handleUsedPoll() (tea.Model, tea.Cmd) {
	if !m.listShown() {
		m.usedPollIdle = true
		return m, nil
	}
	m.refreshUsedIfChanged()
	return m, usedPoll()
}

// resumeUsedPoll catches the list up when it comes back into view, from help,
// a form or a session, and restarts the poll if it had stopped. wasShown is
// whether the list was shown before this update.
func (m Model) resumeUsedPoll(wasShown bool) (Model, tea.Cmd) {
	if wasShown || !m.listShown() {
		return m, nil
	}
	m.refreshUsedIfChanged()
	if !m.usedPollIdle {
		// Still running, or never started: either way not ours to start.
		return m, nil
	}
	m.usedPollIdle = false
	return m, usedPoll()
}

// selectNameOr selects name, or the first profile when name is gone.
func (m *Model) selectNameOr(name string) {
	m.cursor = 0
	m.selectName(name)
}

func (m *Model) clearUseOnce() {
	if m.useOnce != nil {
		m.useOnce.Clear()
		m.useOnce = nil
	}
	m.useOnceName = ""
}
