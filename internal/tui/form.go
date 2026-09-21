package tui

import (
	"errors"
	"reflect"
	"slices"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

// Field ids run in the order the form shows them, which is also tab order.
const (
	fieldName = iota
	fieldHost
	fieldUser
	fieldDomain
	fieldSize
	fieldFullscreen
	fieldDynamic
	fieldScale
	fieldPassword
	fieldForget
	fieldClient
	fieldCount
)

// fieldNone marks an error that belongs to no row.
const fieldNone = -1

// formLabels are what the form calls each field. They are for people, not
// for matching: a config key and a label differ ("dynamic_resolution" and
// "dynamic resolution"), and matching errors by label once left the error
// for that field unattached to it.
var formLabels = [fieldCount]string{
	fieldName:       "name",
	fieldHost:       "host",
	fieldUser:       "user",
	fieldDomain:     "domain",
	fieldSize:       "size",
	fieldFullscreen: "fullscreen",
	fieldDynamic:    "dynamic resolution",
	fieldScale:      "scale",
	fieldPassword:   "password",
	fieldForget:     "forget password",
	fieldClient:     "client",
}

// fieldKeys are the names validation errors use for each field: the config
// keys, plus "password", which SaveProfile uses for the keyring. "forget
// password" has none; nothing can be wrong with it.
var fieldKeys = [fieldCount]string{
	fieldName:       "name",
	fieldHost:       "host",
	fieldUser:       "user",
	fieldDomain:     "domain",
	fieldSize:       "size",
	fieldFullscreen: "fullscreen",
	fieldDynamic:    "dynamic_resolution",
	fieldScale:      "scale",
	fieldPassword:   "password",
	fieldClient:     "client",
}

// fieldForKey returns the field a validation error names.
func fieldForKey(key string) (int, bool) {
	for id, k := range fieldKeys {
		if k != "" && k == key {
			return id, true
		}
	}
	return fieldNone, false
}

// formSection is a titled group of rows.
type formSection struct {
	title  string
	fields []int
}

// formSections groups the rows. The order here is the order on screen and
// the order tab walks them.
var formSections = []formSection{
	{"CONNECTION", []int{fieldName, fieldHost, fieldUser, fieldDomain}},
	{"DISPLAY", []int{fieldSize, fieldFullscreen, fieldDynamic, fieldScale}},
	{"PASSWORD", []int{fieldPassword, fieldForget}},
	{"ADVANCED", []int{fieldClient}},
}

type formState struct {
	oldName  string
	p        config.Profile
	field    int
	password string
	forget   bool
	// err is the message on screen, and errField the row it belongs to,
	// or fieldNone for one shown below the form.
	err      string
	errField int

	// inputs holds a text input for each text field, indexed by field.
	inputs [fieldCount]textinput.Model
	// orig is the profile as opened, for detecting unsaved changes.
	orig config.Profile
	// confirmDiscard is set while asking whether to drop unsaved changes.
	confirmDiscard bool
}

// textValue returns a pointer to the string a text field edits.
func (f *formState) textValue(id int) *string {
	switch id {
	case fieldName:
		return &f.p.Name
	case fieldHost:
		return &f.p.Host
	case fieldUser:
		return &f.p.User
	case fieldDomain:
		return &f.p.Domain
	case fieldClient:
		return &f.p.Client
	case fieldSize:
		return &f.p.Size
	case fieldPassword:
		return &f.password
	}
	return nil
}

// sections lists the groups this form shows, holding only the rows it shows.
// "forget password" only appears on an edit: a profile being added has no
// stored password to delete, so the row would be inert and would still mark
// the form dirty.
func (f formState) sections() []formSection {
	out := make([]formSection, 0, len(formSections))
	for _, sec := range formSections {
		ids := make([]int, 0, len(sec.fields))
		for _, id := range sec.fields {
			if id == fieldForget && f.oldName == "" {
				continue
			}
			ids = append(ids, id)
		}
		out = append(out, formSection{sec.title, ids})
	}
	return out
}

// fields lists the rows this form shows, in order.
func (f formState) fields() []int {
	ids := make([]int, 0, fieldCount)
	for _, sec := range f.sections() {
		ids = append(ids, sec.fields...)
	}
	return ids
}

// shows reports whether field id is one of this form's rows.
func (f formState) shows(id int) bool {
	return slices.Contains(f.fields(), id)
}

// setError shows err, under the field it names when it names one this form
// shows. The field's own row already says which field it is, so only the
// message goes under it.
func (f *formState) setError(err error) {
	f.err, f.errField = err.Error(), fieldNone
	var fe *config.FieldError
	if errors.As(err, &fe) {
		if id, ok := fieldForKey(fe.Field); ok && f.shows(id) {
			f.err, f.errField = fe.Msg, id
		}
	}
}

func (f *formState) clearError() { f.err, f.errField = "", fieldNone }

// fieldIndex is the focused field's position among the visible rows, which is
// not its id once a row is hidden.
func (f formState) fieldIndex() int {
	for i, id := range f.fields() {
		if id == f.field {
			return i
		}
	}
	return 0
}

// step moves focus by delta over the visible rows. tab wraps; arrows stop at
// the first and last row.
func (f *formState) step(delta int, wrap bool) {
	ids := f.fields()
	i := f.fieldIndex() + delta
	if wrap {
		i = (i + len(ids)) % len(ids)
	} else {
		i = min(max(i, 0), len(ids)-1)
	}
	f.focus(ids[i])
}

// dirty reports whether anything differs from the profile as opened.
func (f formState) dirty() bool {
	return !reflect.DeepEqual(f.p, f.orig) || f.password != "" || f.forget
}

func (m Model) openForm(oldName string, p config.Profile) (tea.Model, tea.Cmd) {
	if p.Client == "" {
		p.Client = config.DefaultClient
	}
	if p.Scale == 0 {
		p.Scale = config.DefaultScale
	}
	f := formState{oldName: oldName, p: p, orig: p, errField: fieldNone}
	for id := range fieldCount {
		if v := f.textValue(id); v != nil {
			f.inputs[id] = m.newInput(*v, id == fieldPassword)
			f.inputs[id].Placeholder = f.emptyHint(id)
		}
	}
	f.focus(fieldName)
	m.form = f
	m.view = viewForm
	m.setStatus("", statusInfo)
	return m, nil
}

func (f formState) textFocused() bool {
	switch f.field {
	case fieldName, fieldHost, fieldUser, fieldDomain, fieldClient, fieldSize, fieldPassword:
		return true
	default:
		return false
	}
}

func (m Model) handleFormKey(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	f := m.form
	if f.confirmDiscard {
		switch key {
		case "y", "Y":
			return m.cancelForm()
		case "n", "N", "esc":
			f.confirmDiscard = false
		}
		m.form = f
		return m, nil
	}
	switch key {
	case "esc":
		if f.dirty() {
			f.confirmDiscard = true
			m.form = f
			return m, nil
		}
		return m.cancelForm()
	case "ctrl+s":
		return m.saveForm()
	case "tab":
		f.step(1, true)
	case "shift+tab":
		f.step(-1, true)
	case "up":
		f.step(-1, false)
	case "down":
		f.step(1, false)
	default:
		if f.textFocused() {
			if key == "enter" {
				f.step(1, true)
				break
			}
			f.editText(msg)
			break
		}
		switch key {
		case "?":
			return m.openHelp()
		case "enter", "space":
			m.toggleFormField(&f)
		case "left", "h":
			if f.field == fieldScale {
				f.p.Scale = prevScale(f.p.Scale)
			}
		case "right", "l":
			if f.field == fieldScale {
				f.p.Scale = nextScale(f.p.Scale)
			}
		}
	}
	m.form = f
	return m, nil
}

// editText sends msg to the focused text input and copies the result back.
// A typed password is always stored, so typing one clears a pending "forget":
// the two requests contradict each other and the typed password is the more
// explicit of the pair.
func (f *formState) editText(msg tea.Msg) {
	id := f.field
	in, err := updateInput(f.inputs[id], msg, id == fieldPassword)
	if err != nil {
		f.err, f.errField = err.Error(), id
		return
	}
	f.inputs[id] = in
	if f.err != "" && f.errField == id {
		f.clearError()
	}
	v := f.textValue(id)
	before := *v
	*v = in.Value()
	if id == fieldPassword && before == "" && *v != "" {
		f.forget = false
	}
}

// focus moves to field id, moving the text cursor with it.
func (f *formState) focus(id int) {
	if f.textValue(f.field) != nil {
		f.inputs[f.field].Blur()
	}
	f.field = id
	if f.textValue(id) != nil {
		f.inputs[id].Focus()
		f.inputs[id].CursorEnd()
	}
}

// emptyHint is what an empty text field shows in place of a value: whether
// it has to be filled, or what leaving it empty does. The password hints say
// only what a blank does; the form never asks the keyring whether a password
// is stored.
func (f formState) emptyHint(id int) string {
	switch id {
	case fieldName, fieldHost, fieldUser, fieldClient:
		return "required"
	case fieldDomain:
		return "optional"
	case fieldSize:
		return "client default"
	case fieldPassword:
		if f.oldName == "" {
			return "type to save in the keyring"
		}
		return "leave blank to keep any saved one"
	}
	return ""
}

func (m *Model) toggleFormField(f *formState) {
	switch f.field {
	case fieldFullscreen:
		f.p.Fullscreen = !f.p.Fullscreen
	case fieldDynamic:
		f.p.DynamicResolution = !f.p.DynamicResolution
	case fieldScale:
		f.p.Scale = nextScale(f.p.Scale)
	case fieldForget:
		f.forget = !f.forget
		if f.forget {
			// Forgetting and typing a replacement are contradictory, so the
			// switch drops the typed password the same way typing one drops
			// the switch.
			f.password = ""
			f.inputs[fieldPassword].SetValue("")
		}
	}
}

func nextScale(s int) int {
	switch s {
	case 100:
		return 140
	case 140:
		return 180
	default:
		return 100
	}
}

func prevScale(s int) int {
	switch s {
	case 180:
		return 140
	case 140:
		return 100
	default:
		return 180
	}
}

func (m Model) cancelForm() (tea.Model, tea.Cmd) {
	m.form.password = ""
	m.form = formState{}
	m.view = viewList
	return m, nil
}

func (m Model) saveForm() (tea.Model, tea.Cmd) {
	f := m.form
	f.clearError()
	// Trim before saving rather than after: SaveProfile trims its own copy,
	// so the form was left holding " work " and selected a profile by a name
	// that no longer existed.
	trim(&f.p.Name, &f.p.Host, &f.p.User, &f.p.Domain, &f.p.Client, &f.p.Size)
	// A typed password is stored, an empty one leaves the keyring as it is,
	// and the forget switch clears it. editText and toggleFormField keep the
	// first and last of those from being asked for at once.
	var intent PasswordIntent
	switch {
	case f.password != "":
		pw, err := secret.NewPassword(f.password)
		if err != nil {
			f.err, f.errField = err.Error(), fieldPassword
			f.focus(fieldPassword)
			m.form = f
			return m, nil
		}
		intent = PasswordIntent{Action: PasswordSet, Password: pw}
	case f.forget:
		intent = PasswordIntent{Action: PasswordForget}
	}
	warns, err := m.app.SaveProfile(f.oldName, f.p, intent)
	if err != nil {
		f.setError(err)
		// The invalid field may be scrolled out of a short panel, so focus
		// goes to it: the viewport follows focus, and the fix is typed
		// there anyway.
		if f.errField != fieldNone {
			f.focus(f.errField)
		}
		m.form = f
		return m, nil
	}
	name := f.p.Name
	m.form.password = ""
	m.form = formState{}
	m.view = viewList
	m.clearFilter()
	m.selectName(name)
	// Every warning SaveProfile returns is something that did not happen, so
	// a save with any of them is not reported as a success.
	m.setStatus(outcome("Saved", name, warns))
	return m, nil
}
