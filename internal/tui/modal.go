package tui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

type modalState struct {
	profile   config.Profile
	input     string
	focused   bool
	err       string
	lookupErr error
}

func (m Model) openModal(p config.Profile, lookupErr error) (tea.Model, tea.Cmd) {
	m.modal = modalState{profile: p, focused: true, lookupErr: lookupErr}
	m.view = viewModal
	return m, nil
}

func (m Model) handleModalKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	md := m.modal
	if key == "esc" {
		return m.cancelModal()
	}
	if key == "ctrl+s" {
		return m.modalConnect(true)
	}
	if !md.focused {
		switch key {
		case "?":
			return m.openHelp()
		case "tab":
			md.focused = true
		case "enter":
			m.modal = md
			return m.modalConnect(false)
		}
		m.modal = md
		return m, nil
	}
	switch key {
	case "tab":
		md.focused = false
	case "enter":
		m.modal = md
		return m.modalConnect(false)
	case "backspace":
		if md.input != "" {
			rs := []rune(md.input)
			md.input = string(rs[:len(rs)-1])
		}
	default:
		if msg.Text != "" && !ctrlHeld(msg) {
			for _, r := range msg.Text {
				if r != 0 && unicode.IsPrint(r) {
					md.input += string(r)
				}
			}
		}
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
	cp := pw
	m.useOnce = &cp
	m.useOnceName = p.Name
	return m.runConnect(p, pw, true, warn)
}

func (m Model) viewModal(lo layout) string {
	_ = lo
	md := m.modal
	masked := strings.Repeat("•", len([]rune(md.input)))
	mark := "  "
	if md.focused {
		mark = m.styles.accent.Render("▌ ")
	}
	note := "No stored password for this profile."
	if md.lookupErr != nil {
		note = "Secret store unavailable; enter a password to continue."
	}
	body := m.styles.muted.Render(note) + "\n\n" +
		mark + m.styles.muted.Render("password  ") + m.styles.primary.Render(masked)
	if md.err != "" {
		body += "\n" + m.styles.danger.Render(md.err)
	}
	return body
}
