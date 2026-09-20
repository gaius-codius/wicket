package tui

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// errMultilinePaste rejects a paste that would span lines. Truncating it
// could silently save the wrong password.
var errMultilinePaste = errors.New("paste contains a line break; paste a single line")

// newInput returns a single-line text input with Wicket's styles. Bindings
// that clash with form navigation (tab, up/down) are disabled, as is ctrl+v,
// which would spawn an external clipboard tool; terminal paste still works.
func (m Model) newInput(value string, password bool) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.KeyMap.Paste.SetEnabled(false)
	ti.KeyMap.AcceptSuggestion.SetEnabled(false)
	ti.KeyMap.NextSuggestion.SetEnabled(false)
	ti.KeyMap.PrevSuggestion.SetEnabled(false)
	if password {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}
	ti.SetStyles(m.styles.input)
	ti.SetValue(value)
	return ti
}

// updateInput feeds a key or paste to ti. Pastes are cleaned first: one
// trailing line break is dropped, and a paste that still spans lines is
// rejected. Outside passwords, surrounding whitespace is trimmed.
func updateInput(ti textinput.Model, msg tea.Msg, password bool) (textinput.Model, error) {
	if p, ok := msg.(tea.PasteMsg); ok {
		s := trimOneLineEnd(p.Content)
		if strings.ContainsAny(s, "\r\n") {
			return ti, errMultilinePaste
		}
		if !password {
			s = strings.TrimSpace(s)
		}
		msg = tea.PasteMsg{Content: s}
	}
	ti, _ = ti.Update(msg)
	return ti, nil
}

// trimOneLineEnd drops a single trailing line terminator. Trimming every one
// of them would quietly accept a multi-line paste whose extra lines happen to
// be empty, while rejecting the same paste with text on the second line.
func trimOneLineEnd(s string) string {
	switch {
	case strings.HasSuffix(s, "\r\n"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "\n"), strings.HasSuffix(s, "\r"):
		return s[:len(s)-1]
	}
	return s
}

// inputView renders ti at width cells.
func inputView(ti textinput.Model, width int) string {
	ti.SetWidth(max(width, 1))
	return ti.View()
}
