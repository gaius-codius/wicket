package tui

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
)

// While a session runs Wicket stays on screen and keeps the terminal in raw
// mode (UX-007). It used to hand the terminal to the client with tea.Exec, so
// that Ctrl+C would be a SIGINT the kernel delivered to the client; the
// client's own window is where the session happens, though, and the terminal
// it left behind showed nothing but FreeRDP's log. Now Ctrl+C arrives here as
// a key and Wicket signals the client's process group itself, escalating if
// the client does not stop. The client's output goes to a bounded buffer, not
// the screen, and a signal delivered to Wicket is turned into a message (see
// Run) so it is handled here too rather than killing Wicket and leaving the
// client running.

// stopGrace is how long a stop waits at each signal before sending the next.
const stopGrace = 5 * time.Second

// sessionState is the session the view is showing.
type sessionState struct {
	id      int
	s       *Session
	profile config.Profile
	// held is the password the client was given, kept for the retry offer
	// and use-once. It is a pointer, as retryState.held is, so the value
	// is not copied into every model Bubble Tea passes around.
	held        *secret.Password
	keepUseOnce bool
	extra       string
	opened      time.Time
	// stopping is the last signal a stop sent the client, or 0 before the
	// user has asked for one.
	stopping syscall.Signal
	// recorded is set once the last-used time has been written, or has
	// failed to be; recordWarn says why it failed.
	recorded   bool
	recordWarn string
}

// sessionEndedMsg reports that session id's client has exited.
type sessionEndedMsg struct {
	id int
	cr ConnectResult
}

// sessionRecordedMsg reports that session id's last-used time has been
// recorded, or why it could not be.
type sessionRecordedMsg struct {
	id   int
	warn string
}

// sessionTickMsg redraws the elapsed time of session id.
type sessionTickMsg struct{ id int }

// sessionEscalateMsg fires when session id has had stopGrace to act on
// signal sig and may need the next one.
type sessionEscalateMsg struct {
	id  int
	sig syscall.Signal
}

// signalMsg is a signal delivered to Wicket's own process.
type signalMsg struct{ sig os.Signal }

func waitSession(app *App, id int, s *Session) tea.Cmd {
	return func() tea.Msg { return sessionEndedMsg{id: id, cr: app.Wait(s)} }
}

func recordUse(app *App, id int, name string) tea.Cmd {
	return func() tea.Msg {
		msg := sessionRecordedMsg{id: id}
		if err := app.RecordUse(name); err != nil {
			msg.warn = "last_used: " + err.Error()
		}
		return msg
	}
}

func sessionTick(id int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return sessionTickMsg{id: id} })
}

func (m Model) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m Model) current(id int) bool {
	return m.session != nil && m.session.id == id
}

func (m Model) handleSessionTick(msg sessionTickMsg) (tea.Model, tea.Cmd) {
	// A tick for a session that has ended stops here, so the ticks never
	// outlive the session.
	if !m.current(msg.id) {
		return m, nil
	}
	return m, sessionTick(msg.id)
}

// handleSessionRecorded takes the last-used time the session's start wrote.
// A write that outlasted the session, held up by another Wicket's lock, still
// refreshes the list, and its failure goes on the status line on its own.
func (m Model) handleSessionRecorded(msg sessionRecordedMsg) (tea.Model, tea.Cmd) {
	m.refreshUsed()
	if m.current(msg.id) {
		ss := *m.session
		ss.recorded, ss.recordWarn = true, msg.warn
		m.session = &ss
		return m, nil
	}
	if msg.warn != "" {
		status := msg.warn
		if m.status != "" {
			status = m.status + "; " + status
		}
		m.setStatus(status, statusError)
	}
	return m, nil
}

func (m Model) handleSessionEnded(msg sessionEndedMsg) (tea.Model, tea.Cmd) {
	if !m.current(msg.id) {
		return m, nil
	}
	ss := *m.session
	m.session = nil
	m.view = viewList
	cr := msg.cr
	cr.Warning = ss.recordWarn
	if ss.stopping != 0 && cr.Class != rdp.ClassStartError {
		// The user ended this session. However short it was, it is not a
		// failure to offer a retry for.
		cr.Class = rdp.ClassEnded
		cr.Status = "session stopped"
	}
	cred := *ss.held
	ss.held.Clear()
	return m.applyConnect(ss.profile, cred, ss.keepUseOnce, ss.extra, cr)
}

// stopSession sends the client the next signal of a stop: SIGINT the first
// time, as Ctrl+C would in a terminal the client owned, then SIGTERM, then
// SIGKILL. The view stays until the client has exited.
func (m Model) stopSession() (tea.Model, tea.Cmd) {
	ss := *m.session
	sig := ss.s.rdp.Escalate()
	if sig == 0 {
		// Already gone; its end is on the way.
		return m, nil
	}
	ss.stopping = sig
	m.session = &ss
	m.setStatus(stoppingStatus(sig), statusInfo)
	if sig == syscall.SIGKILL {
		return m, nil
	}
	id := ss.id
	return m, tea.Tick(stopGrace, func(time.Time) tea.Msg { return sessionEscalateMsg{id: id, sig: sig} })
}

func stoppingStatus(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "Stopping session…"
	case syscall.SIGTERM:
		return "Stopping session… asked the client to terminate"
	default:
		return "Stopping session… killed the client"
	}
}

// handleEscalate moves a stop on when the client has ignored the last
// signal for stopGrace. A stop the user has already pushed further by hand
// has its own timer.
func (m Model) handleEscalate(msg sessionEscalateMsg) (tea.Model, tea.Cmd) {
	if !m.current(msg.id) || m.session.stopping != msg.sig {
		return m, nil
	}
	return m.stopSession()
}

// handleSignal deals with a signal sent to Wicket itself. SIGINT is Ctrl+C
// by another route: it stops a running session and otherwise quits. SIGTERM
// and SIGHUP quit, and Run stops any session on the way out.
func (m Model) handleSignal(msg signalMsg) (tea.Model, tea.Cmd) {
	if msg.sig == os.Interrupt && m.session != nil {
		return m.stopSession()
	}
	return m.interrupt()
}

// handleSessionKey takes the keys while a session runs: Ctrl+C stops it and
// nothing else does anything, since the session is in another window and a
// stray key here must not start a second one or edit a profile.
func (m Model) handleSessionKey(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		return m.stopSession()
	}
	return m, nil
}

func (m Model) sessionHints() []keyHint {
	if m.session != nil && m.session.stopping != 0 {
		return []keyHint{{"ctrl+c", "force stop", intentDanger}}
	}
	return []keyHint{{"ctrl+c", "stop session", intentDanger}}
}

const sessionNote = "The session runs in its own window. Wicket comes back here when it closes."

// viewSession renders the running session in at most lo.Budget lines. The
// first line says a session is open and stays; the details come next, and
// the explanation, which never changes, is shed first.
func (m Model) viewSession(lo layout) []string {
	ss := m.session
	if ss == nil {
		return nil
	}
	width := max(lo.Inner, 1)
	head := m.styles.success.Render("● ") +
		m.styles.primary.Bold(true).Render(truncate("Connected to "+ss.profile.Name, max(width-2, 1)))
	lines := []string{head}
	room := max(lo.Budget, 1)
	if room >= 2 {
		lines = append(lines, m.styles.muted.Render(m.sessionDetail(width)))
	}
	note := strings.Split(lipgloss.NewStyle().Width(width).Render(sessionNote), "\n")
	if room-len(lines)-1 >= len(note) {
		lines = append(lines, "")
		for _, ln := range note {
			lines = append(lines, m.styles.muted.Render(strings.TrimRight(ln, " ")))
		}
	}
	return lines
}

// sessionDetail is "user@host · opened 14:02 · elapsed 00:12:31" in width
// cells. The opening time goes first when it does not fit, then the target
// is cut: the ticking elapsed time is what shows the session is alive.
func (m Model) sessionDetail(width int) string {
	ss := m.session
	target := ss.profile.Host
	if ss.profile.User != "" {
		target = ss.profile.User + "@" + target
	}
	opened := "opened " + ss.opened.Format("15:04")
	elapsed := "elapsed " + clockDuration(m.clock().Sub(ss.opened))
	full := target + " · " + opened + " · " + elapsed
	if lipgloss.Width(full) <= width {
		return full
	}
	tail := " · " + elapsed
	if room := width - lipgloss.Width(tail); room >= 4 {
		return truncate(target, room) + tail
	}
	return truncate(elapsed, width)
}

// clockDuration formats d as HH:MM:SS.
func clockDuration(d time.Duration) string {
	d = max(d, 0).Truncate(time.Second)
	h := int(d / time.Hour)
	mnt := int(d%time.Hour) / int(time.Minute)
	sec := int(d%time.Minute) / int(time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", h, mnt, sec)
}
