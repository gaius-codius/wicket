package tui

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

type modalState struct {
	profile   config.Profile
	input     string
	focused   bool
	err       string
	lookupErr error
	// replacing is set when the user asked for a new password for a profile
	// that may already have one, rather than being asked for a missing one.
	replacing bool
	ti        textinput.Model
}

func (m Model) openModal(p config.Profile, lookupErr error, replacing bool) (tea.Model, tea.Cmd) {
	m.modal = modalState{profile: p, focused: true, lookupErr: lookupErr, replacing: replacing, ti: m.newInput("", true)}
	m.modal.ti.Focus()
	m.view = viewModal
	return m, nil
}

func (m Model) handleModalKey(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	md := m.modal
	switch key {
	case "esc":
		return m.cancelModal()
	case "ctrl+s":
		if !md.canSave() {
			md.err = "the keyring is not answering, so the password cannot be saved; enter connects once"
			break
		}
		return m.modalConnect(true)
	case "enter":
		return m.modalConnect(false)
	case "tab", "shift+tab", "up", "down":
		md.focused = !md.focused
		if md.focused {
			md.ti.Focus()
		} else {
			md.ti.Blur()
		}
	default:
		if !md.focused {
			if key == "?" {
				return m.openHelp()
			}
			break
		}
		ti, err := updateInput(md.ti, msg, true)
		if err != nil {
			md.err = err.Error()
			break
		}
		md.ti = ti
		md.input = ti.Value()
		md.err = ""
	}
	m.modal = md
	return m, nil
}

func (m Model) cancelModal() (tea.Model, tea.Cmd) {
	m.modal.input = ""
	m.modal = modalState{}
	m.clearUseOnce()
	m.view = viewList
	return m, nil
}

func (m Model) modalConnect(save bool) (tea.Model, tea.Cmd) {
	pw, err := secret.NewPassword(m.modal.input)
	if err != nil {
		m.modal.err = err.Error()
		return m, nil
	}
	if pw.Empty() {
		m.modal.err = "password required"
		return m, nil
	}
	if save {
		// The keyring may take a while, or wait on an unlock prompt, so
		// the store runs off the loop and the dialog stays up until it has
		// answered; see keyring.go.
		return m.storeAndConnect(m.modal.profile, pw)
	}
	return m.modalStart("")
}

// modalStart closes the dialog and connects with the password typed into it,
// kept for a retry as a use-once password. warn is anything to report beside
// the session.
func (m Model) modalStart(warn string) (tea.Model, tea.Cmd) {
	p := m.modal.profile
	pw, err := secret.NewPassword(m.modal.input)
	if err != nil || pw.Empty() {
		// modalConnect checked the input and nothing can change it while
		// the keyring is busy, so this is not reached.
		return m.cancelModal()
	}
	m.modal.input = ""
	m.modal = modalState{}
	m.view = viewList
	cp := pw
	m.useOnce = &cp
	m.useOnceName = p.Name
	return m.runConnect(p, pw, true, warn)
}

// canSave reports whether ctrl+s is worth offering. When the keyring could
// not be read, a save would only fail the same way -- or wait on it again --
// so the dialog offers to connect once and no more.
func (md modalState) canSave() bool { return md.lookupErr == nil }

// subtitle explains why the dialog is asking. Each of the three ways
// in says only what Wicket knows: a keyring that could not be read is not
// the same as one with no password in it.
func (md modalState) subtitle() string {
	switch {
	case errors.Is(md.lookupErr, context.Canceled):
		return "Wicket stopped waiting for the keyring, so it cannot tell whether a password is saved."
	case errors.Is(md.lookupErr, context.DeadlineExceeded):
		return "The keyring did not answer, so Wicket cannot tell whether a password is saved."
	case md.lookupErr != nil:
		return "The keyring is unavailable, so Wicket cannot tell whether a password is saved."
	case md.replacing:
		// Reached by pressing n after a session ended early, which is
		// when a saved password may be the wrong one.
		return "Type a new password; ctrl+s saves it over any saved one."
	default:
		return "No password is saved for this connection."
	}
}

func (m Model) viewModal(lo layout) string {
	md := m.modal
	mark := "  "
	if md.focused {
		mark = m.styles.accent.Render("▌ ")
	}
	// Wrapping here rather than letting the frame do it keeps the line count
	// honest, so a long profile name cannot push the panel past the window.
	wrap := lipgloss.NewStyle().Width(lo.Inner)
	label := "password"
	if lo.Inner < 24 {
		label = "pw"
	}
	// The field is the dialog: without it there is nothing to answer with,
	// and keystrokes reach it whether or not it is drawn. So it is kept
	// first, then the error, then the title and subtitle -- which only
	// restate what the header and footer already say. Clipping the whole
	// view from the bottom instead lost the field at any height under ten,
	// and keeping the error ahead of it lost the field again as soon as
	// there was an error to show.
	lines := []string{mark + m.styles.muted.Render(label+"  ") +
		inputView(md.ti, max(lo.Inner-len(label)-4, 1))}
	if md.err != "" {
		errText := strings.Split(m.styles.danger.Render(wrap.Render("✗ "+md.err)), "\n")
		// The blank line separating the error from the field is the first
		// thing to go, so a panel with room for one more line spends it on
		// the error rather than on the gap above it.
		if room := lo.Budget - len(lines); room > len(errText) {
			lines = append(lines, append([]string{""}, errText...)...)
		} else {
			lines = append(lines, fit(errText, room)...)
		}
	}
	return strings.Join(append(m.modalHead(lo, lo.Budget-len(lines)), lines...), "\n")
}

// modalHead is the title, subtitle and gap above the field, in room lines.
// The title names the connection, so it outlasts the subtitle; the subtitle
// goes whole or not at all, and the gap goes first.
func (m Model) modalHead(lo layout, room int) []string {
	md := m.modal
	if room < 1 {
		return nil
	}
	title := m.styles.primary.Bold(true).Render(truncate("Connect to "+md.profile.Name, lo.Inner))
	sub := strings.Split(m.styles.muted.Render(lipgloss.NewStyle().Width(lo.Inner).Render(md.subtitle())), "\n")
	head := []string{title}
	if room-1 >= len(sub) {
		head = append(head, sub...)
	}
	if room > len(head) {
		head = append(head, "")
	}
	return head
}
