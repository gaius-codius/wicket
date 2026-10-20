package tui

import (
	"errors"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
)

// A FreeRDP client started from the TUI cannot ask the user anything: its
// stdin is the null device, so it refuses a server certificate it has never
// seen, or one that changed since it was trusted, and exits "TLS connection
// failed". Which certificate, and whether to trust it, is FreeRDP's to show
// and to decide; Wicket never reads or writes its certificate store. The
// retry overlay offers c instead: the same connect, run with Bubble Tea's
// hold on the terminal released and the terminal handed to the client,
// where FreeRDP shows the certificate and asks. The answer, and the store,
// stay FreeRDP's.

// certificateAction and certificateWhy are the retry overlay's hint after a
// FreeRDP client's TLS connection failed.
const (
	certificateAction = "Press c to retry in the terminal, where FreeRDP shows the server's certificate and asks before trusting it."
	certificateWhy    = "The certificate may be new, or changed since it was trusted: FreeRDP refuses one it cannot ask about."
)

// certificateAskedWhy is the retry overlay's note after a TLS failure in a
// client that had the terminal.
const certificateAskedWhy = "FreeRDP had the terminal: the certificate was refused there, or TLS failed for another reason."

// errNoTerminal is a retry in the terminal when Wicket has none to give: its
// input is not a terminal, or Wicket is not in the terminal's foreground.
var errNoTerminal = errors.New("cannot retry in the terminal: Wicket is not running in the foreground of one")

// execTerminal is tea.Exec, which tests replace: a test has no Bubble Tea
// program to release the terminal.
var execTerminal = tea.Exec

// terminalConnect is a connect run through tea.Exec: Bubble Tea releases the
// terminal, sets the streams, calls Run, and takes the terminal back after.
type terminalConnect struct {
	app     *App
	p       config.Profile
	cred    rdp.Credential
	checked config.CheckedShares

	stdin          io.Reader
	stdout, stderr io.Writer

	// started says the client ran, and res how it ended.
	started bool
	res     ConnectResult
}

func (c *terminalConnect) SetStdin(r io.Reader)  { c.stdin = r }
func (c *terminalConnect) SetStdout(w io.Writer) { c.stdout = w }
func (c *terminalConnect) SetStderr(w io.Writer) { c.stderr = w }

// Run starts the client on the terminal and waits for it. The last-used
// time is recorded once Run is done, off the loop: its lock may be held by
// another Wicket, and Bubble Tea's loop waits for Run.
func (c *terminalConnect) Run() error {
	tty, ok := c.stdin.(*os.File)
	if !ok || !rdp.OwnsTerminal(tty) {
		return errNoTerminal
	}
	c.app.setInTerminal(true)
	defer c.app.setInTerminal(false)
	s, err := c.app.StartInTerminal(c.p, c.cred, c.checked, tty, c.stdout, c.stderr)
	if err != nil {
		return err
	}
	c.started = true
	c.res = c.app.Wait(s)
	return nil
}

// terminalEndedMsg is a connect in the terminal that has ended, or never
// started.
type terminalEndedMsg struct {
	p           config.Profile
	cred        rdp.Credential
	keepUseOnce bool
	extra       string
	cr          ConnectResult
	// started says the client ran, and so had the terminal to ask on.
	started bool
}

// runInTerminal is runConnect for a connect that hands the client the
// terminal. Bubble Tea's loop waits while it runs, so there is no session
// view to draw: the client has the screen.
func (m Model) runInTerminal(p config.Profile, cred rdp.Credential, keepUseOnce bool, extra string, checked config.CheckedShares) (tea.Model, tea.Cmd) {
	c := &terminalConnect{app: m.app, p: p, cred: cred, checked: checked}
	m.view = viewList
	m.setStatus("", statusInfo)
	return m, execTerminal(c, func(err error) tea.Msg {
		msg := terminalEndedMsg{p: p, cred: cred, keepUseOnce: keepUseOnce, extra: extra, cr: c.res, started: c.started}
		switch {
		case !c.started:
			if err == nil {
				err = errNoTerminal
			}
			msg.cr = startFailed(err)
		case err != nil:
			// The client ran; it is Bubble Tea that could not take the
			// terminal back.
			msg.cr.Warning = joinWarnings(msg.cr.Warning, "terminal: "+err.Error())
		}
		return msg
	})
}

// handleTerminalEnded reports a connect in the terminal as any other, and
// records the last-used time of a client that ran, as runConnect does. A TLS
// failure there is not one FreeRDP could not ask about: it had the terminal,
// so the certificate was refused there, or TLS failed for another reason,
// and another retry in the terminal is not offered as the way out.
func (m Model) handleTerminalEnded(msg terminalEndedMsg) (tea.Model, tea.Cmd) {
	nm, cmd := m.applyConnect(msg.p, msg.cred, msg.keepUseOnce, msg.extra, msg.cr)
	out := nm.(Model)
	if !msg.started {
		return out, cmd
	}
	if out.retry.certificate {
		out.retry.certificate = false
		out.retry.certificateAsked = true
	}
	// Session ids start at 1, so 0 is never the current session: the
	// result refreshes the list and reports a failure on its own.
	return out, tea.Batch(cmd, recordUse(m.app, 0, msg.p.Name))
}

func joinWarnings(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
