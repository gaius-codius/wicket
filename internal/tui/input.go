package tui

import (
	"errors"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// errMultilinePaste rejects a paste that would span lines. Truncating it
// could silently save the wrong password.
var errMultilinePaste = errors.New("paste contains a line break; paste a single line")

// errControlPaste rejects a paste holding a tab, an escape or another control
// character. The input would drop or rewrite them -- a tab became a space --
// so what was saved, or sent to FreeRDP as a password, was not what was
// pasted.
//
// A complete escape sequence cannot be caught this way. Bubble Tea v2 decodes
// a bracketed paste as it arrives, and a sequence it does not recognise as a
// key -- "\x1b[2J", "\x1b[31m" -- is dropped from the paste before PasteMsg
// is built, so "Zq9\x1b[2JPW" arrives here as "Zq9PW" with nothing left to
// show that anything was removed. A sequence it does read as a key, such as
// "\x1b[A", is kept raw and refused here. The raw bytes are not available to
// the model, so the first kind goes through altered; no printable character
// is lost, only the sequence itself.
var errControlPaste = errors.New("paste contains a tab or control character; paste plain text")

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
// trailing line break is dropped, and a paste that still spans lines, or
// holds any other control character, is rejected. Outside passwords,
// surrounding whitespace is trimmed. A typed control key carries no text, so
// only a paste can bring one in.
func updateInput(ti textinput.Model, msg tea.Msg, password bool) (textinput.Model, error) {
	if p, ok := msg.(tea.PasteMsg); ok {
		s := trimOneLineEnd(p.Content)
		if strings.ContainsAny(s, "\r\n") {
			return ti, errMultilinePaste
		}
		if strings.ContainsFunc(s, unicode.IsControl) {
			return ti, errControlPaste
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

// inputView renders ti in width cells.
//
// Two bubbles quirks have to be worked around here. The widget always draws
// one cell more than the width it is given -- a cursor cell past the end of
// the value, or an extra cell of padding -- so it is asked for one less. And
// it recomputes its horizontal scroll window only when the cursor moves, not
// when the width changes; because the width is set here, on a copy, rather
// than on the stored input, a long value would otherwise be drawn in full and
// wrap the row it sits in. Walking the cursor to the end forces the recompute,
// and walking it back anchors an unfocused value at its head, which is the
// interesting end of a name or a hostname.
func inputView(ti textinput.Model, width int) string {
	if width < 2 {
		return ""
	}
	ti.SetWidth(width - 1)
	if ti.Value() == "" && lipgloss.Width(ti.Placeholder) > width-1 {
		// The widget clips a long placeholder mid-word with no sign that
		// it was cut ("leave blank to keep any saved on").
		ti.Placeholder = truncate(ti.Placeholder, width-1)
	}
	pos := ti.Position()
	ti.CursorEnd()
	if ti.Focused() {
		ti.SetCursor(pos)
		return ti.View()
	}
	if v := ti.Value(); ti.EchoMode == textinput.EchoNormal && lipgloss.Width(v) > width-1 {
		// An unfocused value is shown from its head, and the widget cuts
		// its tail with no sign that there is more. This is a copy, so the
		// stored value is untouched.
		ti.SetValue(truncate(v, width-1))
	}
	ti.CursorStart()
	return ti.View()
}
