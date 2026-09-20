package tui

import (
	"io"

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
}

type connectDoneMsg struct {
	profile     config.Profile
	cred        rdp.Credential
	keepUseOnce bool
	extra       string
	cr          ConnectResult
	execErr     error
}

// connectJob is a tea.ExecCommand that does not touch stdin. The child already
// has Wicket's password pipe; tea.ExecProcess would replace it.
type connectJob struct {
	run func()
}

func (c *connectJob) Run() error {
	if c != nil && c.run != nil {
		c.run()
	}
	return nil
}
func (*connectJob) SetStdin(io.Reader)  {}
func (*connectJob) SetStdout(io.Writer) {}
func (*connectJob) SetStderr(io.Writer) {}

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
		m.setStatus(err.Error(), true)
		m.view = viewList
		return m, nil
	}
	res := m.app.ResolveCredential(p, typed)
	if res.NeedModal {
		return m.openModal(p, res.Err)
	}
	extra := ""
	if res.Multiple {
		extra = "multiple matching secrets; using the most recently modified"
	}
	return m.runConnect(p, res.Cred, typed != nil, extra)
}

func (m Model) runConnect(p config.Profile, cred rdp.Credential, keepUseOnce bool, extra string) (tea.Model, tea.Cmd) {
	switch m.app.term().(type) {
	case nopTerm:
		m.connecting = true
		app := m.app
		var cr ConnectResult
		return m, tea.Exec(&connectJob{run: func() {
			cr = app.Connect(p, cred)
		}}, func(err error) tea.Msg {
			return connectDoneMsg{
				profile: p, cred: cred, keepUseOnce: keepUseOnce, extra: extra,
				cr: cr, execErr: err,
			}
		})
	default:
		return m.applyConnect(p, cred, keepUseOnce, extra, m.app.Connect(p, cred))
	}
}

func (m Model) handleConnectDone(msg connectDoneMsg) (tea.Model, tea.Cmd) {
	m.connecting = false
	if msg.execErr != nil && msg.cr.Status == "" {
		m.clearUseOnce()
		m.retry = retryState{}
		m.view = viewList
		m.setStatus(msg.execErr.Error(), true)
		return m, nil
	}
	return m.applyConnect(msg.profile, msg.cred, msg.keepUseOnce, msg.extra, msg.cr)
}

func (m Model) applyConnect(p config.Profile, cred rdp.Credential, keepUseOnce bool, extra string, cr ConnectResult) (tea.Model, tea.Cmd) {
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
		m.retry = retryState{profile: p, held: &hp, useOnce: keepUseOnce, status: cr.Status, class: cr.Class}
		m.view = viewRetry
		m.setStatus(warn, warn != "")
	default:
		m.retry = retryState{}
		m.view = viewList
		m.clearUseOnce()
		m.setStatus(status, cr.IsError || warn != "")
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
		return m.openModal(p, nil)
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

func (m Model) viewRetry(lo layout) string {
	_ = lo
	hint := "If the password may be wrong, press n for a new password."
	width := newLayout(m.width, m.height).Inner
	wrap := lipgloss.NewStyle().Width(width)
	return m.styles.warning.Render("▲ ") + m.styles.primary.Render(m.retry.status) + "\n" +
		m.styles.muted.Render(wrap.Render(hint))
}
