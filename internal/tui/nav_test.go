package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/secret"
)

func paste(m Model, s string) Model {
	nm, _ := m.Update(tea.PasteMsg{Content: s})
	return nm.(Model)
}

func manyProfiles(n int) string {
	var b strings.Builder
	b.WriteString("[general]\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "[[profiles]]\nname = \"p%02d\"\nhost = \"h%02d\"\nuser = \"u\"\nscale = 100\n", i, i)
	}
	return b.String()
}

func TestCtrlC_QuitsFromEveryView(t *testing.T) {
	for _, keys := range [][]string{
		nil,        // list
		{"?"},      // help
		{"n"},      // form
		{"n", "x"}, // dirty form
		{"/"},      // filter
	} {
		h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
		h.m = press(h.m, keys...)
		h.m = press(h.m, "ctrl+c")
		if !h.m.quit {
			t.Fatalf("ctrl+c after %v did not quit (view %v)", keys, h.m.view)
		}
	}
}

func TestCtrlC_InModalDropsPassword(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	_ = withFakeRDP(t)
	h.m = press(h.m, "enter")
	if h.m.view != viewModal {
		t.Fatal("want modal")
	}
	h.m = typeInto(h.m, "secret")
	h.m = press(h.m, "ctrl+c")
	if !h.m.quit || h.m.modal.input != "" {
		t.Fatalf("quit=%v input=%q", h.m.quit, h.m.modal.input)
	}
}

func TestForm_TextCursorEditing(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	h.m = typeInto(h.m, "wrk")
	h.m = press(h.m, "left", "left")
	h.m = typeInto(h.m, "o")
	if h.m.form.p.Name != "work" {
		t.Fatalf("insert mid-value: %q", h.m.form.p.Name)
	}
	h.m = press(h.m, "home")
	h.m = typeInto(h.m, "my-")
	if h.m.form.p.Name != "my-work" {
		t.Fatalf("home then type: %q", h.m.form.p.Name)
	}
	h.m = press(h.m, "end", "ctrl+u")
	if h.m.form.p.Name != "" {
		t.Fatalf("ctrl+u: %q", h.m.form.p.Name)
	}
}

func TestForm_PasteCleansInput(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	h.m = paste(h.m, "  lab-dc01 \n")
	if h.m.form.p.Name != "lab-dc01" {
		t.Fatalf("name paste: %q", h.m.form.p.Name)
	}
	for h.m.form.field != fieldPassword {
		h.m = press(h.m, "down")
	}
	h.m = paste(h.m, " pass word \r\n")
	if h.m.form.password != " pass word " {
		t.Fatalf("password paste should keep spaces and drop the newline: %q", h.m.form.password)
	}
	if !h.m.form.store {
		t.Fatal("pasting a password should turn Store on, like typing")
	}
	h.m = paste(h.m, "a\nb")
	if h.m.form.password != " pass word " || h.m.form.err == "" {
		t.Fatalf("multi-line paste should be rejected: %q err=%q", h.m.form.password, h.m.form.err)
	}
}

func TestModal_PasteAndCursor(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	_ = withFakeRDP(t)
	h.m = press(h.m, "enter")
	h.m = paste(h.m, "hunter2\n")
	if h.m.modal.input != "hunter2" {
		t.Fatalf("modal paste: %q", h.m.modal.input)
	}
	if strings.Contains(screen(h.m), "hunter2") {
		t.Fatal("password shown in clear")
	}
}

func TestForm_DiscardConfirmation(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	h.m = press(h.m, "e", "esc")
	if h.m.view != viewList {
		t.Fatal("esc on an unchanged form should close it straight away")
	}
	h.m = press(h.m, "e")
	h.m = typeInto(h.m, "2")
	h.m = press(h.m, "esc")
	if !h.m.form.confirmDiscard || !strings.Contains(screen(h.m), "Discard unsaved changes?") {
		t.Fatalf("want discard prompt:\n%s", screen(h.m))
	}
	h.m = press(h.m, "n")
	if h.m.view != viewForm || h.m.form.confirmDiscard || h.m.form.p.Name != "work2" {
		t.Fatal("n should keep editing with the change intact")
	}
	h.m = press(h.m, "esc", "y")
	if h.m.view != viewList {
		t.Fatal("y should discard")
	}
	if p, _ := h.m.selected(); p.Name != "work" {
		t.Fatalf("discarded edit was saved: %q", p.Name)
	}
}

func TestForm_QOnToggleDoesNotCancel(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	for h.m.form.field != fieldFullscreen {
		h.m = press(h.m, "down")
	}
	h.m = press(h.m, "q")
	if h.m.view != viewForm {
		t.Fatal("q on a checkbox cancelled the form")
	}
}

func TestList_JumpKeys(t *testing.T) {
	h := newHarness(t, manyProfiles(30), panicStore{})
	nm, _ := h.m.Update(teaWin(80, 24))
	h.m = nm.(Model)
	for _, c := range []struct {
		key  string
		want int
	}{{"G", 29}, {"g", 0}, {"end", 29}, {"home", 0}, {"pgdown", 12}, {"pgup", 0}} {
		h.m = press(h.m, c.key)
		if h.m.cursor != c.want {
			t.Fatalf("%s: cursor %d, want %d", c.key, h.m.cursor, c.want)
		}
	}
}

func TestList_Filter(t *testing.T) {
	h := newHarness(t, manyProfiles(12), panicStore{})
	h.m = press(h.m, "/")
	if !h.m.filtering {
		t.Fatal("/ should start filtering")
	}
	h.m = typeInto(h.m, "h1")
	out := screen(h.m)
	if !strings.Contains(out, "2 of 12 connections") || strings.Contains(out, "p05") {
		t.Fatalf("filter h1 should show only p10 and p11:\n%s", out)
	}
	if p, ok := h.m.selected(); !ok || p.Name != "p10" {
		t.Fatalf("selection should move to the first match, got %q", p.Name)
	}
	h.m = press(h.m, "down")
	if p, _ := h.m.selected(); p.Name != "p11" {
		t.Fatalf("down should move within matches, got %q", p.Name)
	}
	h.m = press(h.m, "enter")
	if h.m.filtering || h.m.filter.Value() != "h1" {
		t.Fatal("enter should keep the filter and leave the input")
	}
	h.m = press(h.m, "esc")
	if h.m.filter.Value() != "" || !strings.Contains(screen(h.m), "12 connections") {
		t.Fatal("esc should clear the filter")
	}
}

func TestList_FilterNoMatches(t *testing.T) {
	h := newHarness(t, manyProfiles(3), panicStore{})
	h.m = press(h.m, "/")
	h.m = typeInto(h.m, "zzz")
	h.m = press(h.m, "enter")
	if !strings.Contains(screen(h.m), "No matches.") {
		t.Fatal(screen(h.m))
	}
	for _, k := range []string{"enter", "e", "D"} {
		if m := press(h.m, k); m.view != viewList {
			t.Fatalf("%s acted on a hidden profile (view %v)", k, m.view)
		}
	}
}

// F-02: a detail-heavy selection must not push the footer or frame off a
// short terminal.
func TestList_ShortTerminalKeepsFrameAndFooter(t *testing.T) {
	body := `[general]
[[profiles]]
name = "alpha"
host = "alpha.invalid"
user = "alice"
domain = "LAB"
size = "1920x1080"
scale = 100
[[profiles]]
name = "bravo"
host = "b"
user = "u"
scale = 100
`
	for _, h0 := range []int{8, 9, 10, 12, 16} {
		h := newHarness(t, body, panicStore{})
		nm, _ := h.m.Update(teaWin(80, h0))
		h.m = nm.(Model)
		out := screen(h.m)
		lines := strings.Split(out, "\n")
		if len(lines) > h0 {
			t.Fatalf("height %d: %d lines:\n%s", h0, len(lines), out)
		}
		if !strings.Contains(out, "q quit") || !strings.Contains(out, "╰") {
			t.Fatalf("height %d: footer or bottom border missing:\n%s", h0, out)
		}
		if !strings.Contains(out, "alpha") {
			t.Fatalf("height %d: selection missing:\n%s", h0, out)
		}
	}
}

// F-03: ? is password text while the field has focus, so the footer must not
// advertise it there.
func TestModal_FooterMatchesFocus(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	_ = withFakeRDP(t)
	h.m = press(h.m, "enter")
	if out := screen(h.m); strings.Contains(out, "? help") {
		t.Fatalf("focused modal should not advertise ? help:\n%s", out)
	}
	h.m = press(h.m, "?")
	if h.m.view != viewModal || h.m.modal.input != "?" {
		t.Fatal("? should type into the focused password field")
	}
	h.m = press(h.m, "tab")
	if out := screen(h.m); !strings.Contains(out, "? help") {
		t.Fatalf("unfocused modal should advertise ? help:\n%s", out)
	}
	h.m = press(h.m, "?")
	if h.m.view != viewHelp {
		t.Fatal("? should open help once the field is unfocused")
	}
}

// F-04: an input error clears once that field is edited successfully.
func TestForm_PasteErrorClearsOnNextEdit(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	h.m = paste(h.m, "one\ntwo")
	if h.m.form.err == "" {
		t.Fatal("want paste error")
	}
	h.m = typeInto(h.m, "ok")
	if h.m.form.err != "" {
		t.Fatalf("error should clear after a valid edit: %q", h.m.form.err)
	}
}

// F-05: the retry overlay carries the session message; the status line must
// not repeat it, but it still shows warnings.
func TestRetry_MessageNotDuplicated(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	_ = withFakeRDP(t)
	h.m = press(h.m, "enter")
	h.m = typeInto(h.m, "pw")
	h.m = press(h.m, "enter")
	if h.m.view != viewRetry {
		t.Fatalf("want retry, got %v", h.m.view)
	}
	out := screen(h.m)
	if n := strings.Count(out, "session ended quickly"); n != 1 {
		t.Fatalf("message appears %d times:\n%s", n, out)
	}
}

// F-06: starting a connection from the modal must not draw a half-cleared
// dialog.
func TestModal_LeavesDialogBeforeConnecting(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	_ = withFakeRDP(t)
	h.m = press(h.m, "enter")
	h.m = typeInto(h.m, "pw")
	h.m = press(h.m, "enter")
	if out := screen(h.m); strings.Contains(out, "No stored password for .") {
		t.Fatalf("empty modal drawn while connecting:\n%s", out)
	}
}
