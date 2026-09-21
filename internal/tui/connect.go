package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
)

type retryState struct {
	profile config.Profile
	held    *secret.Password
	useOnce bool
	status  string
	class   rdp.Class
	// outcome is how the client exited, when it started at all.
	outcome rdp.Outcome
	// note is the client's own last word on why, cleaned for the screen.
	note string
}

func (m Model) beginConnect() (tea.Model, tea.Cmd) {
	p, ok := m.selected()
	if !ok {
		return m, nil
	}
	var typed *secret.Password
	if m.useOnce != nil && m.useOnceName == p.Name {
		typed = m.useOnce
	}
	return m.connectProfile(p, typed)
}

func (m Model) connectProfile(p config.Profile, typed *secret.Password) (tea.Model, tea.Cmd) {
	if err := m.app.ProbeClient(p); err != nil {
		m.setStatus(err.Error(), statusError)
		m.view = viewList
		return m, nil
	}
	res := m.app.ResolveCredential(p, typed)
	if res.NeedModal {
		return m.openModal(p, res.Err, false)
	}
	extra := ""
	if res.Multiple {
		extra = "multiple matching secrets; using the most recently modified"
	}
	return m.runConnect(p, res.Cred, typed != nil, extra)
}

// runConnect starts the client and puts the session view up while it runs.
// Wicket keeps the terminal throughout: see session.go. Starting returns as
// soon as the client is running; recording the last-used time waits on a
// lock another Wicket may hold, so it happens off the update loop, where it
// cannot keep the session from being drawn or stopped.
func (m Model) runConnect(p config.Profile, cred rdp.Credential, keepUseOnce bool, extra string) (tea.Model, tea.Cmd) {
	s, err := m.app.Start(p, cred)
	if err != nil {
		return m.applyConnect(p, cred, keepUseOnce, extra, startFailed(err))
	}
	m.sessionSeq++
	held, _ := cred.(secret.Password)
	m.session = &sessionState{
		id: m.sessionSeq, s: s, profile: p, held: &held,
		keepUseOnce: keepUseOnce, extra: extra, opened: m.clock(),
	}
	m.view = viewSession
	m.setStatus("", statusInfo)
	id := m.session.id
	return m, tea.Batch(waitSession(m.app, id, s), sessionTick(id), recordUse(m.app, id, p.Name))
}

func (m Model) applyConnect(p config.Profile, cred rdp.Credential, keepUseOnce bool, extra string, cr ConnectResult) (tea.Model, tea.Cmd) {
	// The client may have started, and recorded the last-used time.
	m.refreshUsed()
	// warn holds messages that are not about how the session ended, so they
	// stay on the status line even when the retry overlay is shown.
	warn := ""
	join := func(s string) {
		if s == "" {
			return
		}
		if warn != "" {
			warn += "; "
		}
		warn += s
	}
	join(cr.Warning)
	join(extra)
	status := cr.Status
	if warn != "" {
		if status != "" {
			status += "; "
		}
		status += warn
	}
	switch cr.Class {
	case rdp.ClassStartError, rdp.ClassShortSession:
		if keepUseOnce {
			if pw, ok := cred.(secret.Password); ok {
				cp := pw
				m.useOnce = &cp
				m.useOnceName = p.Name
			}
		} else {
			m.clearUseOnce()
		}
		held, _ := cred.(secret.Password)
		hp := held
		m.retry = retryState{profile: p, held: &hp, useOnce: keepUseOnce, status: cr.Status, class: cr.Class, outcome: cr.Outcome,
			note: clientNote(cr.Output, cred)}
		m.view = viewRetry
		// The overlay carries the session's own message; only the other
		// warnings stay on the status line.
		kind := statusInfo
		if warn != "" {
			kind = statusError
		}
		m.setStatus(warn, kind)
	default:
		m.retry = retryState{}
		m.view = viewList
		m.clearUseOnce()
		kind := statusInfo
		if cr.IsError || warn != "" {
			kind = statusError
		}
		m.setStatus(status, kind)
	}
	return m, nil
}

func (m Model) handleRetryKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "?":
		return m.openHelp()
	case "esc":
		if m.retry.held != nil {
			m.retry.held.Clear()
		}
		m.retry = retryState{}
		m.clearUseOnce()
		m.view = viewList
		return m, nil
	case "n":
		p := m.retry.profile
		if m.retry.held != nil {
			m.retry.held.Clear()
		}
		m.retry = retryState{}
		m.clearUseOnce()
		return m.openModal(p, nil, true)
	case "enter":
		p := m.retry.profile
		held := m.retry.held
		useOnce := m.retry.useOnce
		m.retry = retryState{}
		// Leave the overlay before starting the client. Connecting is
		// asynchronous in production, so staying here would draw the retry
		// box with its message already cleared.
		m.view = viewList
		if useOnce && held != nil && !held.Empty() {
			cp := *held
			m.useOnce = &cp
			m.useOnceName = p.Name
			return m.connectProfile(p, &cp)
		}
		return m.connectProfile(p, nil)
	default:
		return m, nil
	}
}

// retryHint does not claim the password was wrong: a session also ends
// early when the host is unreachable or the window is closed.
const retryHint = "If the password may be wrong, press n for a new password."

// viewRetry renders the overlay in at most room lines. The message is what
// happened and always stays; how the client exited comes next, since it is
// a fact the user cannot see anywhere else, then what the client itself last
// said; the hint repeats a footer key and goes first.
func (m Model) viewRetry(lo layout, room int) []string {
	room = max(room, 1)
	wrap := lipgloss.NewStyle().Width(max(lo.Inner-2, 1))
	msg := strings.Split(wrap.Render(m.retry.status), "\n")
	for i := range msg {
		msg[i] = strings.TrimRight(msg[i], " ")
	}
	if len(msg) > room {
		// Cut the message itself rather than let clipLines swap its last
		// line for a bare "…": at one line that took the ▲ marker and every
		// word of the status with it.
		rest := strings.Join(msg[room-1:], " ")
		msg = append(msg[:room-1], truncate(rest, max(lo.Inner-2, 1)))
	}
	lines := make([]string, 0, room)
	for i, ln := range msg {
		prefix := "  "
		if i == 0 {
			prefix = m.styles.warning.Render("▲ ")
		}
		lines = append(lines, prefix+m.styles.primary.Bold(true).Render(ln))
	}
	more := func(text string) {
		block := strings.Split(m.styles.muted.Render(lipgloss.NewStyle().Width(lo.Inner).Render(text)), "\n")
		if room-len(lines) >= len(block) {
			lines = append(lines, block...)
		}
	}
	if detail := retryDetail(m.retry); detail != "" {
		more(detail)
	}
	if m.retry.note != "" {
		// One line only: a client's log line can be long, and the hint
		// below it matters more than its tail.
		more(truncate("client: "+m.retry.note, lo.Inner))
	}
	more(retryHint)
	return lines
}

// retryDetail says how long the session lasted and how the client exited,
// when the client ran at all. A start error has no outcome to report.
func retryDetail(r retryState) string {
	if r.class != rdp.ClassShortSession {
		return ""
	}
	o := r.outcome
	d := o.Duration.Round(100 * time.Millisecond)
	if o.Signaled {
		return fmt.Sprintf("The client was stopped by signal %d after %s.", o.ExitCode-128, d)
	}
	return fmt.Sprintf("The client exited with status %d after %s.", o.ExitCode, d)
}
