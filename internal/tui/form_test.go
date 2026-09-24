package tui

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

func TestForm_AddAllFields(t *testing.T) {
	h := newHarness(t, "", secret.NewMemory())
	h.m.form = formState{
		p: config.Profile{
			Name: "work", Host: "192.168.1.20", User: "jdoe", Domain: "CORP",
			Client: "sdl-freerdp3", Size: "1920x1080", Fullscreen: true,
			DynamicResolution: true, Scale: 140,
		},
		password: "s3cret",
	}
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("view %v err=%s", h.m.view, h.m.form.err)
	}
	p, ok := h.m.app.Cfg.Profile("work")
	if !ok {
		t.Fatal("profile not saved")
	}
	if p.Host != "192.168.1.20" || p.User != "jdoe" || p.Domain != "CORP" || p.Client != "sdl-freerdp3" {
		t.Fatalf("%+v", p)
	}
	if p.Size != "1920x1080" || !p.Fullscreen || !p.DynamicResolution || p.Scale != 140 {
		t.Fatalf("%+v", p)
	}
	if h.m.cursor != 0 {
		t.Fatal("should select saved profile")
	}
	raw, _ := os.ReadFile(h.cfg)
	if !strings.Contains(string(raw), "scale = 140") || !strings.Contains(string(raw), "fullscreen = true") {
		t.Fatalf("toml:\n%s", raw)
	}
	id := secret.IdentityFor(h.m.app.Cfg.Path(), p)
	if _, err := h.store.Lookup(bg, id); err != nil {
		t.Fatal(err)
	}
}

// openedForm opens the form on p the way n and e do, so its text inputs
// exist: a failed save moves focus, which a bare formState cannot take.
func openedForm(m Model, oldName string, p config.Profile) Model {
	nm, _ := m.openForm(oldName, p)
	return nm.(Model)
}

func TestForm_EscAfterPasswordCreatesNothing(t *testing.T) {
	h := newHarness(t, "", secret.NewMemory())
	h.m = press(h.m, "n")
	h.m = typeInto(h.m, "work")
	h.m.form.password = "typed-secret"
	h.m = press(h.m, "esc")
	if h.m.view != viewForm || !h.m.form.confirmDiscard {
		t.Fatal("esc on an edited form should ask before discarding")
	}
	h.m = press(h.m, "y")
	if h.m.view != viewList {
		t.Fatal(h.m.view)
	}
	if len(h.m.profiles()) != 0 {
		t.Fatal("profile written")
	}
	raw, _ := os.ReadFile(h.cfg)
	if strings.Contains(string(raw), "work") {
		t.Fatal(string(raw))
	}
	if h.m.form.password != "" {
		t.Fatal("password retained")
	}
}

func TestForm_QInFieldTypesQ(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	h.m = press(h.m, "q")
	if h.m.form.p.Name != "q" {
		t.Fatalf("q should type into name, got %q view=%v", h.m.form.p.Name, h.m.view)
	}
}

func TestForm_QuestionInFieldTypes(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	h.m = press(h.m, "?")
	if h.m.view == viewHelp {
		t.Fatal("? in name field opened help")
	}
	if h.m.form.p.Name != "?" {
		t.Fatalf("got %q", h.m.form.p.Name)
	}
}

func TestForm_NewProfileSavesWithoutPassword(t *testing.T) {
	h := newHarness(t, "", secret.NewMemory())
	h.m = press(h.m, "n")
	h.m = typeInto(h.m, "work")
	h.m = press(h.m, "tab")
	h.m = typeInto(h.m, "host1")
	h.m = press(h.m, "tab")
	h.m = typeInto(h.m, "user1")
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("view=%v err=%s", h.m.view, h.m.form.err)
	}
	p, ok := h.m.app.Cfg.Profile("work")
	if !ok {
		t.Fatal("profile not saved")
	}
	if p.Host != "host1" || p.User != "user1" {
		t.Fatalf("%+v", p)
	}
	if _, err := h.store.Lookup(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p)); err == nil {
		t.Fatal("must not store a secret")
	}
}

func TestForm_TypeThenClearPasswordSavesUnchangedSecret(t *testing.T) {
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	id := secret.IdentityFor(h.m.app.Cfg.Path(), p)
	_ = store.Upsert(bg, id, mustPassword(t, "keep-me"))
	h.m = press(h.m, "e")
	h.m = focusField(t, h.m, fieldPassword)
	h.m = typeInto(h.m, "x")
	if h.m.form.password != "x" {
		t.Fatalf("password field holds %q; the keystroke never reached the input", h.m.form.password)
	}
	h.m = press(h.m, "backspace")
	if h.m.form.password != "" {
		t.Fatalf("password field holds %q after backspace", h.m.form.password)
	}
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("view=%v err=%s", h.m.view, h.m.form.err)
	}
	got, err := store.Lookup(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Password.Empty() {
		t.Fatal("stored secret was dropped")
	}
}

func TestForm_SizeTrimmedOnSave(t *testing.T) {
	h := newHarness(t, "", secret.NewMemory())
	base := config.Profile{Name: "n", Host: "h", User: "u", Client: config.DefaultClient, Scale: 100, DynamicResolution: true}
	h.m.form = formState{p: base}
	h.m.form.p.Size = "  1920x1080  "
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("err %s", h.m.form.err)
	}
	got, _ := h.m.app.Cfg.Profile("n")
	if got.Size != "1920x1080" {
		t.Fatalf("size %q", got.Size)
	}
	h.m.form = formState{p: base}
	h.m.form.p.Name = "blanksize"
	h.m.form.p.Size = "   "
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	got, _ = h.m.app.Cfg.Profile("blanksize")
	if got.Size != "" {
		t.Fatalf("whitespace size stored as %q", got.Size)
	}
}

func TestForm_DuplicateNameRejected(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	h.m = openedForm(h.m, "", config.Profile{Name: "work", Host: "h2", User: "u2", Client: config.DefaultClient, Scale: 100, DynamicResolution: true})
	before, _ := os.ReadFile(h.cfg)
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewForm {
		t.Fatal("should reject")
	}
	after, _ := os.ReadFile(h.cfg)
	if string(before) != string(after) {
		t.Fatal("file written")
	}
}

func TestForm_SizeValidation(t *testing.T) {
	h := newHarness(t, "", secret.NewMemory())
	base := config.Profile{Name: "n", Host: "h", User: "u", Client: config.DefaultClient, Scale: 100, DynamicResolution: true}
	nope := base
	nope.Size = "nope"
	h.m = openedForm(h.m, "", nope)
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewForm || h.m.form.err == "" {
		t.Fatal("size=nope should fail")
	}
	for _, size := range []string{"1920x1080", "1920X1080", "100%", ""} {
		h.m.form = formState{p: base}
		h.m.form.p.Name = "s" + size
		h.m.form.p.Size = size
		h.m.view = viewForm
		h.m = press(h.m, "ctrl+s")
		if h.m.view != viewList {
			t.Fatalf("size %q err %s", size, h.m.form.err)
		}
	}
}

func TestForm_EditFieldsRoundTrip(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	p, _ := h.m.app.Cfg.Profile("work")
	p.Host = "other"
	p.User = "alice"
	p.Domain = "CORP"
	p.Client = "xfreerdp3"
	p.Size = "100%"
	p.Fullscreen = true
	p.DynamicResolution = false
	p.Scale = 180
	h.m.form = formState{oldName: "work", p: p}
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	got, _ := h.m.app.Cfg.Profile("work")
	if got != p {
		t.Fatalf("got %+v want %+v", got, p)
	}
}

func TestForm_PreserveUnknownKeys(t *testing.T) {
	src := `
[general]
keep = 1.5
flag = true
[other]
nested_int = 3
[[profiles]]
name = "work"
host = "h"
user = "old"
mystery = true
ratio = 1.25
password = "x"
`
	h := newHarness(t, src, secret.NewMemory())
	p, _ := h.m.app.Cfg.Profile("work")
	p.User = "new"
	h.m.form = formState{oldName: "work", p: p}
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	raw := map[string]any{}
	b, _ := os.ReadFile(h.cfg)
	if _, err := toml.Decode(string(b), &raw); err != nil {
		t.Fatal(err)
	}
	g := raw["general"].(map[string]any)
	if g["keep"] != 1.5 || g["flag"] != true {
		t.Fatalf("general %+v", g)
	}
	other, ok := raw["other"].(map[string]any)
	if !ok {
		t.Fatalf("top-level [other] dropped: %s", b)
	}
	if other["nested_int"] != int64(3) && other["nested_int"] != 3 {
		t.Fatalf("other.nested_int %+v (%T)", other["nested_int"], other["nested_int"])
	}
	profs, ok := raw["profiles"].([]map[string]any)
	if !ok {
		if arr, ok2 := raw["profiles"].([]any); ok2 && len(arr) == 1 {
			profs = []map[string]any{arr[0].(map[string]any)}
		} else {
			t.Fatalf("profiles %+T", raw["profiles"])
		}
	}
	if len(profs) != 1 {
		t.Fatalf("profiles %d", len(profs))
	}
	pr := profs[0]
	if pr["user"] != "new" {
		t.Fatalf("user %+v", pr["user"])
	}
	if pr["mystery"] != true {
		t.Fatalf("mystery %+v", pr["mystery"])
	}
	if pr["ratio"] != 1.25 {
		t.Fatalf("ratio %+v (%T)", pr["ratio"], pr["ratio"])
	}
	if strings.Contains(string(b), "password") {
		t.Fatal("password key rewritten")
	}
}

func TestForm_ForgetThenEnterShowsModal(t *testing.T) {
	_ = withFakeRDP(t)
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	_ = store.Upsert(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, "secret"))
	h.m.form = formState{oldName: "work", p: p, forget: true}
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	if _, err := store.Lookup(bg, secret.IdentityFor(h.m.app.Cfg.Path(), p)); err == nil {
		t.Fatal("item should be gone")
	}
	h.m = press(h.m, "enter")
	if h.m.view != viewModal {
		t.Fatalf("view %v want modal", h.m.view)
	}
}

func TestForm_HostChangeDeletesOldSecret(t *testing.T) {
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	oldID := secret.IdentityFor(h.m.app.Cfg.Path(), p)
	_ = store.Upsert(bg, oldID, mustPassword(t, "secret"))
	p.Host = "other"
	h.m.form = formState{oldName: "work", p: p}
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	if _, err := store.Lookup(bg, oldID); err == nil {
		t.Fatal("old identity remains")
	}
	// The password moved rather than vanished: deleting the old entry alone
	// is what the bug this once guarded against did too.
	if got := storedAs(t, store, h.m.app, p); got != "secret" {
		t.Fatalf("new identity holds %q, want the password carried to it", got)
	}
}

func TestForm_FallbackThemeStillRenders(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	out := screen(h.m)
	if !strings.Contains(out, "name:") {
		t.Fatalf("%s", out)
	}
}

func TestForm_ArrowKeysMoveFields(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	h.m = press(h.m, "up")
	if h.m.form.field != fieldName {
		t.Fatalf("up on first field: %d", h.m.form.field)
	}
	// Down works from text fields and from toggles alike.
	h.m = press(h.m, "down", "down", "down", "down", "down", "down")
	if h.m.form.field != fieldMultimon {
		t.Fatalf("after 6 downs: %d", h.m.form.field)
	}
	h.m = press(h.m, "down", "down", "down", "down", "down", "down", "down")
	if h.m.form.field != fieldClient {
		t.Fatalf("down past last field should stop at last: %d", h.m.form.field)
	}
	h.m = press(h.m, "up")
	if h.m.form.field != fieldPassword {
		t.Fatalf("up: %d", h.m.form.field)
	}
	if h.m.form.p.Name != "" {
		t.Fatalf("arrows typed into a field: %q", h.m.form.p.Name)
	}
}

// "forget password" deletes what is already in the keyring, so it only means
// something on an edit. On the add form it was inert but still counted as an
// unsaved change, so ticking it made esc ask whether to discard nothing.
func TestForm_ForgetRowIsEditOnly(t *testing.T) {
	cfg := fixtureTOML("work", "h", "u")
	add := stripANSI(sized(t, cfg, 80, 30, "n").render())
	if strings.Contains(add, "forget password") {
		t.Fatalf("add form offers a password to forget:\n%s", add)
	}
	edit := stripANSI(sized(t, cfg, 80, 30, "e").render())
	if !strings.Contains(edit, "forget password") {
		t.Fatalf("edit form hides it:\n%s", edit)
	}
}

// Typing a replacement and asking to forget cancel each other out, so the form
// never sends both. Whichever the user does last wins.
func TestForm_ForgetAndTypedPasswordAreExclusive(t *testing.T) {
	cfg := fixtureTOML("work", "h", "u")

	m := sized(t, cfg, 80, 30, "e")
	m = focusField(t, m, fieldForget)
	m = press(m, "space")
	if !m.form.forget {
		t.Fatal("space should tick forget password")
	}
	m = focusField(t, m, fieldPassword)
	m = typeInto(m, "x")
	if m.form.forget {
		t.Fatal("typing a password should clear a pending forget")
	}

	m = sized(t, cfg, 80, 30, "e")
	m = focusField(t, m, fieldPassword)
	m = typeInto(m, "x")
	m = focusField(t, m, fieldForget)
	m = press(m, "space")
	if !m.form.forget || m.form.password != "" {
		t.Fatalf("forget should drop the typed password: forget=%v password=%q", m.form.forget, m.form.password)
	}
}

// A typed password is stored on save; there is no second box to tick.
func TestForm_TypedPasswordOnEditReplacesTheStoredOne(t *testing.T) {
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	id := secret.IdentityFor(h.m.app.Cfg.Path(), p)
	if err := store.Upsert(bg, id, mustPassword(t, "old")); err != nil {
		t.Fatal(err)
	}
	h.m = press(h.m, "e")
	h.m = focusField(t, h.m, fieldPassword)
	h.m = typeInto(h.m, "new")
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("view=%v err=%s", h.m.view, h.m.form.err)
	}
	got, err := store.Lookup(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := got.Password.WriteLine(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "new\n" {
		t.Fatalf("stored %q, want the typed password", buf.String())
	}
}

// A warning from a save or a delete is something that did not happen, so it
// gets the error marker. It used to share the informational "•" with routine
// notes, which read as though the password had been stored.
func TestStatus_SaveAndDeleteWarningsAreErrors(t *testing.T) {
	cfg := fixtureTOML("work", "h", "u")
	store := &wrapStore{inner: secret.NewMemory(), upsertErr: errors.New("boom")}
	h := newHarness(t, cfg, store)
	h.m = press(h.m, "e")
	h.m = focusField(t, h.m, fieldPassword)
	h.m = typeInto(h.m, "pw")
	h.m = press(h.m, "ctrl+s")
	if !strings.Contains(h.m.status, "could not save password") {
		t.Fatalf("status %q", h.m.status)
	}
	if h.m.statusKind != statusError || !strings.Contains(stripANSI(h.m.render()), "✗ Saved work, but could not save password") {
		t.Fatalf("warning is not marked as an error:\n%s", stripANSI(h.m.render()))
	}

	h2 := newHarness(t, cfg, &wrapStore{inner: secret.NewMemory(), deleteErr: errors.New("boom")})
	h2.m = press(h2.m, "D", "y")
	if h2.m.statusKind != statusError || !strings.Contains(stripANSI(h2.m.render()), "✗ Deleted work, but a leftover secret") {
		t.Fatalf("delete warning is not marked as an error: %q\n%s", h2.m.status, stripANSI(h2.m.render()))
	}
}

// A paste is trimmed on its way into the field, so typing the same trailing
// space was the only way to be told off for it. Both are trimmed on save now.
func TestForm_TypedWhitespaceIsTrimmedNotRejected(t *testing.T) {
	h := newHarness(t, "", secret.NewMemory())
	h.m = press(h.m, "n")
	h.m = typeInto(h.m, "  work  ")
	h.m = focusField(t, h.m, fieldHost)
	h.m = typeInto(h.m, " host.invalid ")
	h.m = focusField(t, h.m, fieldUser)
	h.m = typeInto(h.m, " jdoe ")
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("view=%v err=%s", h.m.view, h.m.form.err)
	}
	p, ok := h.m.app.Cfg.Profile("work")
	if !ok {
		t.Fatalf("profile not saved under a trimmed name: %+v", h.m.app.Cfg.Profiles())
	}
	if p.Host != "host.invalid" || p.User != "jdoe" {
		t.Fatalf("%+v", p)
	}
}

// The saved profile is the one selected afterwards, even when the name was
// typed with whitespace around it: the form used to look for the untrimmed
// name, find nothing, and silently leave the cursor where it was.
func TestForm_SelectsTheSavedProfileAfterTrimming(t *testing.T) {
	h := newHarness(t, fixtureTOML("aaa", "h", "u"), secret.NewMemory())
	h.m = press(h.m, "n")
	h.m = typeInto(h.m, "  qqq  ")
	h.m = focusField(t, h.m, fieldHost)
	h.m = typeInto(h.m, "192.0.2.9")
	h.m = focusField(t, h.m, fieldUser)
	h.m = typeInto(h.m, "u")
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("view=%v err=%s", h.m.view, h.m.form.err)
	}
	sel, ok := h.m.selected()
	if !ok || sel.Name != "qqq" {
		t.Fatalf("selected %+v, want the profile just saved", sel)
	}
}

// Validation errors name config keys and the form shows labels, and the two
// differ for dynamic resolution. The error used to be matched against the
// label, so that one never reached its row.
func TestForm_ErrorAttachesToItsFieldByKey(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 30, "e")
	m.form.setError(&config.FieldError{Field: "dynamic_resolution", Msg: "must be a boolean"})
	if m.form.errField != fieldDynamic {
		t.Fatalf("error attached to field %d, want dynamic resolution", m.form.errField)
	}
	labelW, valueW := formColumns(m.panelLayout().Inner)
	if row := m.formRow(fieldDynamic, labelW, valueW); !strings.Contains(row, m.styles.danger.Render(padRight("dynamic resolution:", labelW))) {
		t.Fatalf("label not marked as the invalid one: %q", row)
	}
	lines := strings.Split(stripANSI(m.render()), "\n")
	for i, ln := range lines {
		if strings.Contains(ln, "dynamic resolution:") {
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "✗ must be a boolean") {
				t.Fatalf("error is not under its field:\n%s", strings.Join(lines, "\n"))
			}
			return
		}
	}
	t.Fatalf("no dynamic resolution row:\n%s", strings.Join(lines, "\n"))
}

// Tab walks the fields in the order the sections show them.
func TestForm_TabFollowsTheSections(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 36, "e")
	want := []int{fieldName, fieldHost, fieldUser, fieldDomain, fieldSize, fieldFullscreen,
		fieldMultimon, fieldDynamic, fieldScale, fieldClipboard, fieldShareHome,
		fieldPassword, fieldForget, fieldClient}
	for i, id := range want {
		if m.form.field != id {
			t.Fatalf("tab stop %d is %q, want %q", i, formLabels[m.form.field], formLabels[id])
		}
		m = press(m, "tab")
	}
	if m.form.field != fieldName {
		t.Fatalf("tab from the last field went to %q, want name", formLabels[m.form.field])
	}

	// The screen shows them in the same order, under their headings.
	out := stripANSI(m.render())
	at := -1
	for _, s := range []string{"CONNECTION", "name:", "host:", "user:", "domain:", "DISPLAY", "size:",
		"fullscreen:", "all monitors:", "dynamic resolution:", "scale:", "SHARING", "clipboard:",
		"home folder:", "PASSWORD", "  password:", "forget password:",
		"ADVANCED", "client:"} {
		i := strings.Index(out, s)
		if i <= at {
			t.Fatalf("%q is out of order:\n%s", s, out)
		}
		at = i
	}
}

var cuePattern = regexp.MustCompile(`▲ (\d+) (?:more )?above|▼ (\d+) (?:more )?below`)

// The scroll cues count exactly the rows the window leaves out, above and
// below, wherever the focus is.
func TestForm_ScrollCuesCountHiddenFields(t *testing.T) {
	for _, h := range []int{11, 14, 18} {
		for _, key := range []string{"n", "e"} {
			m := sized(t, fixtureTOML("work", "h", "u"), 80, h, key)
			ids := m.form.fields()
			for _, focus := range ids {
				m = focusField(t, m, focus)
				out := stripANSI(m.render())
				first, shown := -1, 0
				for i, id := range ids {
					if strings.Contains(out, "│ ▌ "+formLabels[id]+":") || strings.Contains(out, "│   "+formLabels[id]+":") {
						if first < 0 {
							first = i
						}
						shown++
					}
				}
				above, below := 0, 0
				for _, c := range cuePattern.FindAllStringSubmatch(out, -1) {
					if c[1] != "" {
						above, _ = strconv.Atoi(c[1])
					} else {
						below, _ = strconv.Atoi(c[2])
					}
				}
				if above != first || below != len(ids)-first-shown {
					t.Fatalf("80x%d %q, %s focused: cues say %d above and %d below; %d rows shown from %d of %d:\n%s",
						h, key, formLabels[focus], above, below, shown, first, len(ids), out)
				}
			}
		}
	}
}

// With a line or two to spare, the focused field comes first and its error
// second. Clipping the whole form from the bottom used to keep the field and
// replace the error with an ellipsis.
func TestForm_FocusedFieldAndErrorSurviveATinyBudget(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 30, "e")
	m = focusField(t, m, fieldSize)
	m.form.setError(&config.FieldError{Field: "size", Msg: "use dimension (1920x1080), N% (e.g. 100%), or empty (FreeRDP chooses)"})
	lo := m.panelLayout()
	m.fitChrome(&lo)
	for budget := 1; budget <= 2; budget++ {
		lo.Budget = budget
		body := strings.Split(stripANSI(m.viewForm(lo)), "\n")
		if len(body) != budget || !strings.HasPrefix(body[0], "▌ size:") {
			t.Fatalf("budget %d: want the focused row first:\n%s", budget, strings.Join(body, "\n"))
		}
		if budget == 2 && !strings.Contains(body[1], "✗ use dimension") {
			t.Fatalf("budget 2: want the error second:\n%s", strings.Join(body, "\n"))
		}
	}
}

// A failed save moves focus to the field that failed, which scrolls it on
// screen however far it was from where the cursor stood.
func TestForm_FailedSaveFocusesTheInvalidField(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 12, "e")
	m = focusField(t, m, fieldSize)
	m = typeInto(m, "nope")
	m = focusField(t, m, fieldName)
	m = press(m, "ctrl+s")
	if m.view != viewForm || m.form.field != fieldSize {
		t.Fatalf("view %v, focus on %q; want the form, focused on size", m.view, formLabels[m.form.field])
	}
	if out := stripANSI(m.render()); !strings.Contains(out, "▌ size:") || !strings.Contains(out, "✗ use dimension") {
		t.Fatalf("the invalid field or its error is off screen:\n%s", out)
	}
}

// The three scales are shown side by side where they fit, and only the
// current one, with a filled dot, where they do not.
func TestForm_ScaleFallsBackWhenNarrow(t *testing.T) {
	scaleRow := func(w int) string {
		m := sized(t, fixtureTOML("work", "h", "u"), w, 30, "e")
		m = focusField(t, m, fieldScale)
		for _, ln := range strings.Split(stripANSI(m.render()), "\n") {
			if strings.Contains(ln, "scale:") {
				return ln
			}
		}
		t.Fatalf("%d wide: no scale row", w)
		return ""
	}
	if row := scaleRow(80); !strings.Contains(row, "● 100%") || !strings.Contains(row, "○ 140%") || !strings.Contains(row, "○ 180%") {
		t.Fatalf("80 wide shows %q, want all three choices with dots", row)
	}
	if row := scaleRow(44); !strings.Contains(row, "● 100%") || strings.Contains(row, "180%") {
		t.Fatalf("44 wide shows %q, want only the current scale", row)
	}
}

// The header says when the form holds changes, and says so ahead of the
// name when the two do not both fit.
func TestForm_HeaderMarksUnsavedChanges(t *testing.T) {
	header := func(m Model) string {
		for _, ln := range strings.Split(stripANSI(m.render()), "\n") {
			if strings.Contains(ln, brandMark) {
				return ln
			}
		}
		t.Fatal("no header")
		return ""
	}
	m := sized(t, fixtureTOML("a-connection-with-a-long-name", "h", "u"), 80, 24, "e")
	if h := header(m); strings.Contains(h, "modified") || !strings.Contains(h, "edit a-connection") {
		t.Fatalf("unchanged form: %q", h)
	}
	m = typeInto(m, "x")
	if h := header(m); !strings.Contains(h, "edit a-connection") || !strings.Contains(h, "● modified") {
		t.Fatalf("changed form: %q", h)
	}
	nm, _ := m.Update(teaWin(30, 24))
	if h := header(nm.(Model)); !strings.Contains(h, "● modified") {
		t.Fatalf("30 wide dropped the marker: %q", h)
	}
}

// A new profile shares the clipboard, as FreeRDP does, and nothing else.
func TestForm_NewProfileSharingDefaults(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	p := h.m.form.p
	if !p.Clipboard || p.Multimon || p.ShareHome {
		t.Fatalf("clipboard %v multimon %v share home %v", p.Clipboard, p.Multimon, p.ShareHome)
	}
}

// All monitors means full screen on each, so the two rows move together:
// all monitors on turns fullscreen on, and fullscreen off turns all
// monitors off. The other rows switch alone.
func TestForm_MultimonAndFullscreenMoveTogether(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), nil)
	h.m = press(h.m, "e")
	h.m = focusField(t, h.m, fieldMultimon)
	h.m = press(h.m, "space")
	if !h.m.form.p.Multimon || !h.m.form.p.Fullscreen {
		t.Fatalf("multimon on: multimon %v fullscreen %v", h.m.form.p.Multimon, h.m.form.p.Fullscreen)
	}
	h.m = press(h.m, "space")
	if h.m.form.p.Multimon || !h.m.form.p.Fullscreen {
		t.Fatalf("multimon off: multimon %v fullscreen %v", h.m.form.p.Multimon, h.m.form.p.Fullscreen)
	}
	h.m = press(h.m, "space")
	h.m = focusField(t, h.m, fieldFullscreen)
	h.m = press(h.m, "space")
	if h.m.form.p.Multimon || h.m.form.p.Fullscreen {
		t.Fatalf("fullscreen off: multimon %v fullscreen %v", h.m.form.p.Multimon, h.m.form.p.Fullscreen)
	}

	for _, tc := range []struct {
		id  int
		get func(config.Profile) bool
	}{
		{fieldClipboard, func(p config.Profile) bool { return p.Clipboard }},
		{fieldShareHome, func(p config.Profile) bool { return p.ShareHome }},
	} {
		h.m = focusField(t, h.m, tc.id)
		before := h.m.form.p
		h.m = press(h.m, "space")
		after := h.m.form.p
		if tc.get(after) == tc.get(before) {
			t.Fatalf("%s did not switch", formLabels[tc.id])
		}
		// Only that field changed.
		h.m = press(h.m, "space")
		if h.m.form.p != before {
			t.Fatalf("%s: %+v, want %+v", formLabels[tc.id], h.m.form.p, before)
		}
	}
}

// The rows reach the config file.
func TestForm_SharingSaves(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	h.m = press(h.m, "e")
	for _, id := range []int{fieldMultimon, fieldClipboard, fieldShareHome} {
		h.m = focusField(t, h.m, id)
		h.m = press(h.m, "space")
	}
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("save failed: %s", h.m.form.err)
	}
	got, _ := h.m.app.Cfg.Profile("work")
	if !got.Multimon || !got.Fullscreen || got.Clipboard || !got.ShareHome {
		t.Fatalf("saved %+v", got)
	}
}
