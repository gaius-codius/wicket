package tui

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
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
	fieldStore
	fieldForget
	fieldCount
)

type formState struct {
	oldName  string
	p        config.Profile
	field    int
	password string
	store    bool
	forget   bool
	err      string
	errField string
}

func (m Model) openForm(oldName string, p config.Profile) (tea.Model, tea.Cmd) {
	if p.Client == "" {
		p.Client = config.DefaultClient
	}
	if p.Scale == 0 {
		p.Scale = config.DefaultScale
	}
	m.form = formState{oldName: oldName, p: p}
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

func (m Model) handleFormKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	f := m.form
	if key == "esc" {
		return m.cancelForm()
	}
	if key == "ctrl+s" {
		return m.saveForm()
	}
	if !f.textFocused() {
		switch key {
		case "q":
			return m.cancelForm()
		case "?":
			return m.openHelp()
		case "tab":
			f.field = (f.field + 1) % fieldCount
		case "shift+tab":
			f.field = (f.field + fieldCount - 1) % fieldCount
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
		m.form = f
		return m, nil
	}
	switch key {
	case "tab":
		f.field = (f.field + 1) % fieldCount
	case "shift+tab":
		f.field = (f.field + fieldCount - 1) % fieldCount
	case "backspace":
		f.backspace()
	case "enter":
		f.field = (f.field + 1) % fieldCount
	default:
		if msg.Text != "" && !ctrlHeld(msg) {
			f.insert(msg.Text)
		}
	}
	m.form = f
	return m, nil
}

func ctrlHeld(msg tea.KeyPressMsg) bool {
	return msg.Mod.Contains(tea.ModCtrl)
}

func (f *formState) insert(s string) {
	for _, r := range s {
		if r == 0 || !unicode.IsPrint(r) {
			continue
		}
		switch f.field {
		case fieldName:
			f.p.Name += string(r)
		case fieldHost:
			f.p.Host += string(r)
		case fieldUser:
			f.p.User += string(r)
		case fieldDomain:
			f.p.Domain += string(r)
		case fieldClient:
			f.p.Client += string(r)
		case fieldSize:
			f.p.Size += string(r)
		case fieldPassword:
			wasEmpty := f.password == ""
			f.password += string(r)
			if wasEmpty {
				f.store = true
			}
		}
	}
}

func (f *formState) backspace() {
	cut := func(s string) string {
		if s == "" {
			return s
		}
		rs := []rune(s)
		return string(rs[:len(rs)-1])
	}
	switch f.field {
	case fieldName:
		f.p.Name = cut(f.p.Name)
	case fieldHost:
		f.p.Host = cut(f.p.Host)
	case fieldUser:
		f.p.User = cut(f.p.User)
	case fieldDomain:
		f.p.Domain = cut(f.p.Domain)
	case fieldClient:
		f.p.Client = cut(f.p.Client)
	case fieldSize:
		f.p.Size = cut(f.p.Size)
	case fieldPassword:
		f.password = cut(f.password)
		if f.password == "" {
			f.store = false
		}
	}
}

func (m *Model) toggleFormField(f *formState) {
	switch f.field {
	case fieldFullscreen:
		f.p.Fullscreen = !f.p.Fullscreen
	case fieldDynamic:
		f.p.DynamicResolution = !f.p.DynamicResolution
	case fieldScale:
		f.p.Scale = nextScale(f.p.Scale)
	case fieldStore:
		f.store = !f.store
	case fieldForget:
		f.forget = !f.forget
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
	if f.store && f.password == "" && !f.forget {
		f.err = "cannot store a blank password"
		f.errField = "password"
		m.form = f
		return m, nil
	}
	f.p.Size = strings.TrimSpace(f.p.Size)
	intent := PasswordIntent{Store: f.store, Forget: f.forget}
	if f.password != "" {
		pw, err := secret.NewPassword(f.password)
		if err != nil {
			f.err = err.Error()
			f.errField = "password"
			m.form = f
			return m, nil
		}
		intent.Set = true
		intent.Password = pw
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
	m.selectName(name)
	if len(warns) > 0 {
		m.setStatus(strings.Join(warns, "; "), false)
	} else {
		m.setStatus("", false)
	}
	return m, nil
}

func (m Model) viewForm(lo layout) string {
	_ = lo
	f := m.form
	title := "NEW"
	if f.oldName != "" {
		title = "EDIT"
	}
	var b strings.Builder
	b.WriteString(m.styles.header.Render(title))
	b.WriteByte('\n')
	row := func(id int, label, value string) {
		mark := "  "
		style := m.styles.muted
		if f.field == id {
			mark = m.styles.accent.Render("▌ ")
			style = m.styles.primary
		}
		if f.errField == strings.ToLower(label) && f.err != "" {
			style = m.styles.danger
		}
		b.WriteString(mark + style.Render(label+": "+value) + "\n")
	}
	row(fieldName, "name", f.p.Name)
	row(fieldHost, "host", f.p.Host)
	row(fieldUser, "user", f.p.User)
	row(fieldDomain, "domain", f.p.Domain)
	row(fieldClient, "client", f.p.Client)
	sizeVal := f.p.Size
	if f.field == fieldSize && sizeVal == "" {
		sizeVal = m.styles.muted.Render("1920x1080, 100%, or empty")
		mark := m.styles.accent.Render("▌ ")
		b.WriteString(mark + m.styles.primary.Render("size: ") + sizeVal + "\n")
	} else {
		row(fieldSize, "size", f.p.Size)
	}
	row(fieldFullscreen, "fullscreen", check(f.p.Fullscreen))
	row(fieldDynamic, "dynamic_resolution", check(f.p.DynamicResolution))
	row(fieldScale, "scale", strconv.Itoa(f.p.Scale))
	masked := strings.Repeat("•", len([]rune(f.password)))
	row(fieldPassword, "password", masked)
	row(fieldStore, "store password", check(f.store))
	row(fieldForget, "forget password", check(f.forget))
	if f.err != "" {
		b.WriteString(m.styles.danger.Render(f.err) + "\n")
	}
	b.WriteString(m.styles.footer.Render("[ctrl+s] save  [esc] cancel  [?] help"))
	return b.String()
}

func check(v bool) string {
	if v {
		return "[x]"
	}
	return "[ ]"
}
