package tui

import (
	"errors"
	"reflect"
	"slices"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
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
	fieldMultimon
	fieldDynamic
	fieldScale
	fieldClipboard
	fieldShareHome
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
	fieldMultimon:   "all monitors",
	fieldDynamic:    "dynamic resolution",
	fieldScale:      "scale",
	fieldClipboard:  "clipboard",
	fieldShareHome:  "home folder",
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
	fieldMultimon:   "multimon",
	fieldDynamic:    "dynamic_resolution",
	fieldScale:      "scale",
	fieldClipboard:  "clipboard",
	fieldShareHome:  "share_home",
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
	{"DISPLAY", []int{fieldSize, fieldFullscreen, fieldMultimon, fieldDynamic, fieldScale}},
	{"SHARING", []int{fieldClipboard, fieldShareHome}},
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
	// quitOnDiscard is set when ctrl+c raised the question, so a yes quits
	// Wicket, as the ctrl+c meant, rather than going back to the list.
	quitOnDiscard bool

	// clients are the choices the client row cycles through, "custom…"
	// aside: the known FreeRDP clients found on PATH when the form opened,
	// in order of preference, then the configured client if it is not one
	// of them, so that opening and saving a profile never changes it.
	// clientAt is the current choice; len(clients) is "custom…", where the
	// row is a text input. With no known client installed that is the
	// only choice.
	clients  []string
	clientAt int
	// detected is how many of clients were found on PATH.
	detected int
	// clientMissing is the configured client when PATH did not have it as
	// the form opened. PATH is searched once, then, rather than per frame.
	clientMissing string
}

// clientCustom reports whether the client row is the free-text input.
func (f formState) clientCustom() bool { return f.clientAt >= len(f.clients) }

// setClientChoice moves the client row to choice at, wrapping, and takes
// the value that choice stands for. The custom text is kept while another
// choice is shown, so coming back to "custom…" finds it as it was left.
func (f *formState) setClientChoice(at int) {
	n := len(f.clients) + 1
	at = (at%n + n) % n
	wasCustom := f.clientCustom()
	f.clientAt = at
	focused := f.field == fieldClient
	if f.clientCustom() {
		f.p.Client = f.inputs[fieldClient].Value()
		if focused && !wasCustom {
			f.inputs[fieldClient].Focus()
			f.inputs[fieldClient].CursorEnd()
		}
		return
	}
	if focused && wasCustom {
		f.inputs[fieldClient].Blur()
	}
	f.p.Client = f.clients[at]
}

// clientLeavesText reports whether key takes the client row out of its text
// input and back to the choices: ← with the cursor already at the start,
// where it would otherwise do nothing. Everywhere else ← moves in the text.
func (f formState) clientLeavesText(key string) bool {
	return f.field == fieldClient && f.clientCustom() && len(f.clients) > 0 &&
		key == "left" && f.inputs[fieldClient].Position() == 0
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
		// Only "custom…" is typed; the other choices are picked.
		if f.clientCustom() {
			return &f.p.Client
		}
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
	// One PATH search per form: the row's choices, a new profile's client
	// and the "not found" marker all come from it.
	installed := m.app.InstalledClients()
	if p.Client == "" {
		p.Client = rdp.PreferredClient(installed)
	}
	if p.Scale == 0 {
		p.Scale = config.DefaultScale
	}
	f := formState{oldName: oldName, p: p, orig: p, errField: fieldNone}
	for _, c := range installed {
		f.clients = append(f.clients, c.Name)
	}
	f.detected = len(f.clients)
	custom := ""
	if at := slices.Index(f.clients, p.Client); at >= 0 {
		f.clientAt = at
	} else {
		if !m.app.Installed(p.Client) {
			f.clientMissing = p.Client
		}
		// Something no choice stands for is shown as it is: as a choice of
		// its own beside the installed clients, or in the text input when
		// there are none.
		custom = p.Client
		if f.detected > 0 {
			f.clients = append(f.clients, p.Client)
			f.clientAt = f.detected
		} else {
			f.clientAt = len(f.clients)
		}
	}
	for id := range fieldCount {
		v := f.textValue(id)
		if id == fieldClient {
			// The input exists whatever the choice, for "custom…".
			v = &custom
		}
		if v != nil {
			f.inputs[id] = m.newInput(*v, id == fieldPassword)
			f.inputs[id].Placeholder = f.emptyHint(id)
		}
	}
	f.focus(fieldName)
	m.form = f
	m.view = viewForm
	// A stale note is cleared by Update as the form opens; a warning stays.
	return m, nil
}

func (f formState) textFocused() bool {
	switch f.field {
	case fieldName, fieldHost, fieldUser, fieldDomain, fieldSize, fieldPassword:
		return true
	case fieldClient:
		return f.clientCustom()
	default:
		return false
	}
}

func (m Model) handleFormKey(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	f := m.form
	if f.confirmDiscard {
		switch key {
		case "y", "Y":
			if f.quitOnDiscard {
				return m.interrupt()
			}
			return m.cancelForm()
		case "n", "N", "esc":
			f.confirmDiscard, f.quitOnDiscard = false, false
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
		if f.clientLeavesText(key) {
			f.setClientChoice(f.clientAt - 1)
			break
		}
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
			switch f.field {
			case fieldScale:
				f.p.Scale = prevScale(f.p.Scale)
			case fieldClient:
				f.setClientChoice(f.clientAt - 1)
			}
		case "right", "l":
			switch f.field {
			case fieldScale:
				f.p.Scale = nextScale(f.p.Scale)
			case fieldClient:
				f.setClientChoice(f.clientAt + 1)
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
	case fieldName, fieldHost, fieldUser:
		return "required"
	case fieldClient:
		if f.detected == 0 {
			return "no FreeRDP client found"
		}
		return "binary name"
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
		// All monitors means full screen on each of them, so a window is
		// one monitor.
		if !f.p.Fullscreen {
			f.p.Multimon = false
		}
	case fieldMultimon:
		f.p.Multimon = !f.p.Multimon
		if f.p.Multimon {
			f.p.Fullscreen = true
		}
	case fieldClipboard:
		f.p.Clipboard = !f.p.Clipboard
	case fieldShareHome:
		f.p.ShareHome = !f.p.ShareHome
	case fieldDynamic:
		f.p.DynamicResolution = !f.p.DynamicResolution
	case fieldScale:
		f.p.Scale = nextScale(f.p.Scale)
	case fieldClient:
		f.setClientChoice(f.clientAt + 1)
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
	// The save's keyring steps run off the update loop; see keyring.go.
	return m.beginSave(f, intent)
}
