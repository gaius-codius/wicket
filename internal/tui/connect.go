package tui

import (
	"fmt"
	"path/filepath"
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
	// fullscreen is a hint for a known client failure under fullscreen,
	// worked out when the session ended; see fullscreenHint.
	fullscreen string
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
	if typed != nil {
		return m.runConnect(p, *typed, true, "")
	}
	// The keyring is asked off the loop: it may be waiting on an unlock
	// prompt, or not answering at all. See keyring.go.
	return m.resolveCredential(p)
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
	case rdp.ClassStartError, rdp.ClassShortSession, rdp.ClassFailed:
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
			note: clientNote(cr.Output, cred), fullscreen: m.app.fullscreenHint(p, cr.Outcome)}
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
// early when the host is unreachable or the window is closed. It is offered
// only when the password may be to blame (see rdp.Outcome.MaybeCredentials):
// under "could not connect" or a logoff it pointed at the wrong culprit.
const retryHint = "If the password may be wrong, press n for a new password."

// fullscreenHint is for FreeRDP's SDL client giving up before it connects
// with fullscreen on. On a fractionally scaled Wayland monitor it can misread
// the monitor's size (a 3840x2160 output at 1.6 as 102x102) and fail in
// pre-connect, exit 136, where the X11 client with /f works. That is a
// FreeRDP bug, so the hint offers ways round it: xfreerdp3 when it is
// installed, and fullscreen off either way. It searches PATH, so it is asked
// once, as the session ends, not as the overlay draws.
func (a *App) fullscreenHint(p config.Profile, o rdp.Outcome) string {
	if !p.Fullscreen || filepath.Base(o.Client) != rdp.ClientSDL || !o.PreConnectFailed() {
		return ""
	}
	const lead = "FreeRDP's SDL client can fail fullscreen on scaled monitors; "
	if a.Installed(rdp.ClientX11) {
		return lead + "try the " + rdp.ClientX11 + " client or turn fullscreen off."
	}
	return lead + "try turning fullscreen off."
}

// retryBlocks are the overlay's parts, most important first: what happened,
// how the client exited, the fullscreen hint, what the client last said, and
// the password hint.
func (m Model) retryBlocks(lo layout) (msg, detail, fullscreen, note, hint []string) {
	wrap := lipgloss.NewStyle().Width(max(lo.Inner-2, 1))
	msg = strings.Split(wrap.Render(m.retry.status), "\n")
	for i := range msg {
		msg[i] = strings.TrimRight(msg[i], " ")
	}
	block := func(text string) []string {
		return strings.Split(m.styles.muted.Render(lipgloss.NewStyle().Width(lo.Inner).Render(text)), "\n")
	}
	if d := retryDetail(m.retry); d != "" {
		detail = block(d)
	}
	if m.retry.fullscreen != "" {
		fullscreen = block(m.retry.fullscreen)
	}
	if m.retry.note != "" {
		// One line only: a client's log line can be long, and the hint
		// below it matters more than its tail.
		note = block(truncate("client: "+m.retry.note, lo.Inner))
	}
	if m.retry.class != rdp.ClassStartError && m.retry.outcome.MaybeCredentials() {
		hint = block(retryHint)
	}
	return msg, detail, fullscreen, note, hint
}

// viewRetry renders the overlay in at most room lines. The message is what
// happened and always stays; how the client exited comes next, since it is
// a fact the user cannot see anywhere else, then the fullscreen hint, which
// says what to do about a failure the rest only describe, then what the
// client itself last said; the password hint repeats a footer key and goes
// first.
func (m Model) viewRetry(lo layout, room int) []string {
	room = max(room, 1)
	msg, detail, fullscreen, note, hint := m.retryBlocks(lo)
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
	for _, block := range [][]string{detail, fullscreen, note, hint} {
		if len(block) > 0 && room-len(lines) >= len(block) {
			lines = append(lines, block...)
		}
	}
	return lines
}

// retryDetail says how long the session lasted and how the client exited,
// when the client ran at all. A start error has no outcome to report.
func retryDetail(r retryState) string {
	if r.class != rdp.ClassShortSession && r.class != rdp.ClassFailed {
		return ""
	}
	o := r.outcome
	d := "after " + o.Duration.Round(100*time.Millisecond).String()
	switch {
	case o.Duration < time.Second:
		// Rounded, the shortest runs read "after 0s", as if the client had
		// not run at all.
		d = "within a second"
	case o.Duration >= time.Minute:
		d = "after " + o.Duration.Round(time.Second).String()
	}
	if o.Signaled {
		return fmt.Sprintf("The client was stopped by signal %d %s.", o.ExitCode-128, d)
	}
	return fmt.Sprintf("The client exited with status %d %s.", o.ExitCode, d)
}
