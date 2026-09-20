package tui

import (
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
	p := m.modal.profile
	m.modal.input = ""
	warn := ""
	if save {
		if err := m.app.StoreSecret(p, pw); err != nil {
			warn = "could not save password: " + err.Error()
		}
	}
	m.modal = modalState{}
	m.view = viewList
	cp := pw
	m.useOnce = &cp
	m.useOnceName = p.Name
	return m.runConnect(p, pw, true, warn)
}

func (m Model) viewModal(lo layout) string {
	md := m.modal
	mark := "  "
	if md.focused {
		mark = m.styles.accent.Render("▌ ")
	}
	note := "No stored password for " + md.profile.Name + "."
	switch {
	case md.lookupErr != nil:
		note = "Secret store unavailable; enter a password to continue."
	case md.replacing:
		// Reached by pressing n after a session failed, which is exactly when
		// a password is stored and suspected of being wrong.
		note = "Enter a new password for " + md.profile.Name + "."
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
	// first, then the error, then the note -- which only restates what the
	// user can see. Clipping the whole view from the bottom instead lost the
	// field at any height under ten, and keeping the error ahead of it lost
	// the field again as soon as there was an error to show.
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
	head := append(strings.Split(m.styles.muted.Render(wrap.Render(note)), "\n"), "")
	return strings.Join(append(fit(head, lo.Budget-len(lines)), lines...), "\n")
}
