package tui

import (
	"reflect"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

const (
	fieldName = iota
	fieldHost
	fieldUser
	fieldDomain
	fieldClient
	fieldSize
	fieldFullscreen
	fieldDynamic
	fieldScale
	fieldPassword
	fieldForget
	fieldCount
)

type formState struct {
	oldName  string
	p        config.Profile
	field    int
	password string
	forget   bool
	err      string
	errField string

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

// fields lists the rows this form shows, in order. "forget password" only
// appears on an edit: a profile being added has no stored password to delete,
// so the row would be inert and would still mark the form dirty.
func (f formState) fields() []int {
	ids := make([]int, 0, fieldCount)
	for id := range fieldCount {
		if id == fieldForget && f.oldName == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

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
	f := formState{oldName: oldName, p: p, orig: p}
	for id := range fieldCount {
		if v := f.textValue(id); v != nil {
			f.inputs[id] = m.newInput(*v, id == fieldPassword)
		}
	}
	f.inputs[fieldSize].Placeholder = "dimension (1920x1080), N% (100%) or empty for default"
	f.focus(fieldName)
	m.form = f
	m.view = viewForm
	m.status = ""
	m.statusErr = false
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
		f.err, f.errField = err.Error(), fieldLabel(id)
		return
	}
	f.inputs[id] = in
	if f.errField == fieldLabel(id) {
		f.err, f.errField = "", ""
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

func fieldLabel(id int) string { return formLabels[id] }

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
			// checkbox drops the typed password the same way typing one drops
			// the checkbox.
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
	f.err = ""
	f.errField = ""
	// A typed password is stored, an empty one leaves the keyring as it is, and
	// the checkbox clears it. editText and toggleFormField keep the first and
	// last of those from being asked for at once.
	var intent PasswordIntent
	switch {
	case f.password != "":
		pw, err := secret.NewPassword(f.password)
		if err != nil {
			f.err = err.Error()
			f.errField = "password"
			m.form = f
			return m, nil
		}
		intent = PasswordIntent{Action: PasswordSet, Password: pw}
	case f.forget:
		intent = PasswordIntent{Action: PasswordForget}
	}
	warns, err := m.app.SaveProfile(f.oldName, f.p, intent)
	if err != nil {
		f.err = err.Error()
		if fe, ok := err.(*config.FieldError); ok {
			f.errField = fe.Field
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
	// they carry the error marker rather than the informational one.
	m.setStatus(strings.Join(warns, "; "), len(warns) > 0)
	return m, nil
}

func (m Model) viewForm(lo layout) string {
	f := m.form
	// The label column is sized to the panel, not the other way round: a
	// narrow terminal should truncate labels rather than render rows wider
	// than the frame.
	longest := 0
	for _, l := range formLabels {
		longest = max(longest, len(l)+1)
	}
	labelW := min(longest, max(lo.Inner-2-6, 3))
	valueW := max(lo.Inner-2-labelW-2, 1)
	rows := make([]string, 0, fieldCount)
	row := func(id int, value string) {
		label := formLabels[id]
		mark := "  "
		labelStyle := m.styles.muted
		if f.field == id {
			mark = m.styles.accent.Render("▌ ")
			labelStyle = m.styles.accent
		}
		if f.err != "" && f.errField == strings.ToLower(label) {
			labelStyle = m.styles.danger
		}
		if f.textValue(id) != nil {
			value = inputView(f.inputs[id], valueW)
		} else {
			value = m.styles.primary.Render(truncate(value, valueW))
		}
		rows = append(rows, mark+labelStyle.Render(padRight(truncate(label+":", labelW), labelW))+"  "+value)
	}
	for _, id := range f.fields() {
		switch id {
		case fieldFullscreen:
			row(id, check(f.p.Fullscreen))
		case fieldDynamic:
			row(id, check(f.p.DynamicResolution))
		case fieldScale:
			row(id, "‹ "+strconv.Itoa(f.p.Scale)+"% ›")
		case fieldForget:
			row(id, check(f.forget))
		default:
			row(id, "")
		}
	}
	wrap := lipgloss.NewStyle().Width(lo.Inner)
	var tail []string
	switch {
	case f.confirmDiscard:
		tail = []string{"", m.styles.warning.Render("▲ ") + m.styles.primary.Render(wrap.Render("Discard unsaved changes?"))}
	case f.err != "":
		tail = append([]string{""}, strings.Split(m.styles.danger.Render(wrap.Render("✗ "+f.err)), "\n")...)
	}
	// The form scrolls to the focused field instead of running past the
	// bottom of the panel, where the last rows were unreachable but still
	// saved by ctrl+s.
	budget := max(lo.Budget-len(tail), 1)
	start, end := listWindow(len(rows), f.fieldIndex(), budget, 1)
	out := append([]string{}, rows[start:end]...)
	return strings.Join(append(out, tail...), "\n")
}

var formLabels = [fieldCount]string{
	fieldName:       "name",
	fieldHost:       "host",
	fieldUser:       "user",
	fieldDomain:     "domain",
	fieldClient:     "client",
	fieldSize:       "size",
	fieldFullscreen: "fullscreen",
	fieldDynamic:    "dynamic_resolution",
	fieldScale:      "scale",
	fieldPassword:   "password",
	fieldForget:     "forget password",
}

func check(v bool) string {
	if v {
		return "[x]"
	}
	return "[ ]"
}
