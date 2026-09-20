package tui

import (
	"os"
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
		store:    true,
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
	if _, err := h.store.Lookup(id); err != nil {
		t.Fatal(err)
	}
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
	if _, err := h.store.Lookup(secret.IdentityFor(h.m.app.Cfg.Path(), p)); err == nil {
		t.Fatal("must not store a secret")
	}
}

func TestForm_TypeThenClearPasswordSavesUnchangedSecret(t *testing.T) {
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	id := secret.IdentityFor(h.m.app.Cfg.Path(), p)
	_ = store.Upsert(id, mustPassword(t, "keep-me"))
	h.m = press(h.m, "e")
	h.m = focusField(t, h.m, fieldPassword)
	h.m = typeInto(h.m, "x")
	if h.m.form.password != "x" {
		t.Fatalf("password field holds %q; the keystroke never reached the input", h.m.form.password)
	}
	if !h.m.form.store {
		t.Fatal("typing a password should turn store on")
	}
	h.m = press(h.m, "backspace")
	if h.m.form.password != "" {
		t.Fatalf("password field holds %q after backspace", h.m.form.password)
	}
	if h.m.form.store {
		t.Fatal("cleared password must not leave store on")
	}
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewList {
		t.Fatalf("view=%v err=%s", h.m.view, h.m.form.err)
	}
	got, err := store.Lookup(id)
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
	h.m.form = formState{p: base, store: false}
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
	h.m.form = formState{p: base, store: false}
	h.m.form.p.Name = "blanksize"
	h.m.form.p.Size = "   "
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	got, _ = h.m.app.Cfg.Profile("blanksize")
	if got.Size != "" {
		t.Fatalf("whitespace size stored as %q", got.Size)
	}
}

func TestForm_BlankStoreInlineError(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m.form = formState{
		p:     config.Profile{Name: "n", Host: "h", User: "u", Client: config.DefaultClient, Scale: 100, DynamicResolution: true},
		store: true,
	}
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewForm {
		t.Fatal("should stay on form")
	}
	if !strings.Contains(h.m.form.err, "blank") {
		t.Fatalf("err %q", h.m.form.err)
	}
}

func TestForm_DuplicateNameRejected(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	h.m.form = formState{
		p:     config.Profile{Name: "work", Host: "h2", User: "u2", Client: config.DefaultClient, Scale: 100, DynamicResolution: true},
		store: false,
	}
	h.m.view = viewForm
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
	h.m.form = formState{p: base, store: false}
	h.m.form.p.Size = "nope"
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	if h.m.view != viewForm || h.m.form.err == "" {
		t.Fatal("size=nope should fail")
	}
	for _, size := range []string{"1920x1080", "1920X1080", "100%", ""} {
		h.m.form = formState{p: base, store: false}
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
	h.m.form = formState{oldName: "work", p: p, store: false}
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
	h.m.form = formState{oldName: "work", p: p, store: false}
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
	_ = store.Upsert(secret.IdentityFor(h.m.app.Cfg.Path(), p), mustPassword(t, "secret"))
	h.m.form = formState{oldName: "work", p: p, store: false, forget: true}
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	if _, err := store.Lookup(secret.IdentityFor(h.m.app.Cfg.Path(), p)); err == nil {
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
	_ = store.Upsert(oldID, mustPassword(t, "secret"))
	p.Host = "other"
	h.m.form = formState{oldName: "work", p: p, store: false}
	h.m.view = viewForm
	h.m = press(h.m, "ctrl+s")
	if _, err := store.Lookup(oldID); err == nil {
		t.Fatal("old identity remains")
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
	if h.m.form.field != fieldFullscreen {
		t.Fatalf("after 6 downs: %d", h.m.form.field)
	}
	h.m = press(h.m, "down", "down", "down", "down", "down", "down", "down")
	if h.m.form.field != fieldForget {
		t.Fatalf("down past last field should stop at last: %d", h.m.form.field)
	}
	h.m = press(h.m, "up")
	if h.m.form.field != fieldStore {
		t.Fatalf("up: %d", h.m.form.field)
	}
	if h.m.form.p.Name != "" {
		t.Fatalf("arrows typed into a field: %q", h.m.form.p.Name)
	}
}
