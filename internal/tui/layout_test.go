package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/secret"
)

func sized(t *testing.T, cfg string, w, h int, keys ...string) Model {
	t.Helper()
	hh := newHarness(t, cfg, secret.NewMemory())
	nm, _ := hh.m.Update(teaWin(w, h))
	m := nm.(Model)
	for _, k := range keys {
		m = press(m, k)
	}
	return m
}

func measure(s string) (w, h int) {
	lines := strings.Split(s, "\n")
	for _, ln := range lines {
		w = max(w, lipgloss.Width(ln))
	}
	return w, len(lines)
}

// Nothing may render past the window. Before this was enforced the panel had
// an eight-line floor, so every terminal shorter than that lost its footer and
// bottom border, and the form ran wider than a narrow panel.
func TestRender_NeverOverflowsTheWindow(t *testing.T) {
	cfg := fixtureTOML("a-connection-with-a-long-name", "host.example.invalid", "user")
	var bad int
	for w := widthTiny; w <= 130; w++ {
		for h := heightTiny; h <= 30; h++ {
			// "e" matters as much as "n": the edit form is the only one whose
			// fields hold values, and a value wider than its column is what
			// used to wrap the row and push the bottom border off screen.
			for _, keys := range [][]string{nil, {"/"}, {"n"}, {"e"}, {"?"}, {"D"}, {"enter"}} {
				m := sized(t, cfg, w, h, keys...)
				gotW, gotH := measure(m.render())
				if gotW > w || gotH > h {
					if bad < 10 {
						t.Errorf("%dx%d after %v rendered %dx%d", w, h, keys, gotW, gotH)
					}
					bad++
				}
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d size and view combinations overflowed", bad)
	}
}

// A value longer than its column scrolls inside the column; it must not wrap
// onto the next line, which cost the panel its bottom border on a short
// terminal.
func TestForm_LongValueStaysOnItsRow(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 24, "e")
	before := strings.Split(stripANSI(m.render()), "\n")
	m = typeInto(m, strings.Repeat("X", 60))
	after := strings.Split(stripANSI(m.render()), "\n")
	if len(after) != len(before) {
		t.Fatalf("row wrapped: %d lines, was %d:\n%s", len(after), len(before), strings.Join(after, "\n"))
	}
	var row string
	for _, ln := range after {
		if strings.Contains(ln, "name:") {
			row = ln
		}
		if w := lipgloss.Width(ln); w != 0 && w != 80 {
			t.Fatalf("line is %d cells wide:\n%s", w, strings.Join(after, "\n"))
		}
	}
	if !strings.Contains(row, "XXXX") {
		t.Fatalf("the value left its row: %q", row)
	}
}

// Every form row occupies exactly one line. A row that wraps costs the panel a
// line it never budgeted for; the size placeholder did that at narrow widths,
// because the text input draws one cell more than the width it is given.
func TestForm_RowsNeverWrap(t *testing.T) {
	cfg := fixtureTOML("work", "host.invalid", "user")
	for w := widthTiny; w <= 130; w++ {
		for _, key := range []string{"n", "e"} {
			m := sized(t, cfg, w, 30, key)
			lo := m.panelLayout()
			m.fitChrome(&lo)
			rows := strings.Split(m.viewForm(lo), "\n")
			if want := len(m.form.fields()); len(rows) != want {
				t.Fatalf("width %d, %q: %d lines for %d rows:\n%s",
					w, key, len(rows), want, stripANSI(m.render()))
			}
			for i, ln := range rows {
				// A row wider than the panel wraps inside the frame, which
				// the row count above cannot see.
				if got := lipgloss.Width(ln); got > lo.Inner {
					t.Fatalf("width %d, %q: row %d is %d cells in a %d-cell panel:\n%s",
						w, key, i, got, lo.Inner, stripANSI(m.render()))
				}
			}
		}
	}
}

// A short terminal drops chrome rather than the body: the list must still be
// visible at the smallest size that is not called tiny.
func TestRender_ShortTerminalKeepsTheList(t *testing.T) {
	cfg := fixtureTOML("work", "host.invalid", "u")
	for h := heightTiny; h <= 9; h++ {
		out := stripANSI(sized(t, cfg, 80, h).render())
		if !strings.Contains(out, "work") {
			t.Errorf("height %d does not show the profile:\n%s", h, out)
		}
	}
}

// Opening the filter costs two lines, which have to come out of the body
// rather than be added to the panel.
func TestRender_FilterHeadDoesNotGrowThePanel(t *testing.T) {
	cfg := fixtureTOML("work", "host.invalid", "u")
	for h := 8; h <= 20; h++ {
		_, before := measure(sized(t, cfg, 80, h).render())
		_, after := measure(sized(t, cfg, 80, h, "/").render())
		if after > before {
			t.Errorf("height %d: %d lines, %d after pressing /", h, before, after)
		}
	}
}

// Every visible form field has to be reachable. The form used to render all
// of its rows regardless of height, so the last ones were off screen while
// ctrl+s still saved them.
func TestForm_ScrollsToTheFocusedField(t *testing.T) {
	// "n" adds, "e" edits; the two differ by the forget password row.
	for _, key := range []string{"n", "e"} {
		m := sized(t, fixtureTOML("work", "h", "u"), 80, 14, key)
		for _, id := range m.form.fields() {
			m = focusField(t, m, id)
			out := stripANSI(m.render())
			label := truncate(formLabels[id]+":", len(formLabels[id])+1)
			if !strings.Contains(out, label) {
				t.Fatalf("%q: field %q not on screen at 80x14:\n%s", key, formLabels[id], out)
			}
		}
	}
}

// Help is longer than a short panel, so it has to scroll.
func TestHelp_ScrollsToTheLastEntry(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 12, "?")
	last := helpKeys(m.helpFor)
	want := last[len(last)-1].label
	if strings.Contains(stripANSI(m.render()), want) {
		t.Skip("help already fits; nothing to scroll")
	}
	m = press(m, "G")
	if out := stripANSI(m.render()); !strings.Contains(out, want) {
		t.Fatalf("last help entry %q unreachable:\n%s", want, out)
	}
}

// ? is text while a form field has focus, so the footer must not offer it
// there.
func TestForm_DoesNotAdvertiseHelpWhileTyping(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 24, "n")
	if _, hs := m.chrome(); slicesHasKey(hs, "?") {
		t.Fatal("footer offers ? while a text field has focus")
	}
	m = focusField(t, m, fieldFullscreen)
	if _, hs := m.chrome(); !slicesHasKey(hs, "?") {
		t.Fatal("footer should offer ? once a checkbox has focus")
	}
}

func slicesHasKey(hs []hint, key string) bool {
	for _, h := range hs {
		if h.key == key {
			return true
		}
	}
	return false
}

var _ = tea.WindowSizeMsg{}

// A dialog that asks a question has to show the question. Both of these used
// to be clipped from the bottom, so a short terminal replaced the delete
// target with an ellipsis and hid the password field altogether -- while
// keystrokes still reached the invisible field and enter still connected.
func TestDialogs_KeepWhatMatters(t *testing.T) {
	cfg := fixtureTOML("work", "host.invalid", "user")
	for w := 20; w <= 130; w += 2 {
		for h := heightTiny; h <= 30; h++ {
			del := stripANSI(sized(t, cfg, w, h, "D").render())
			if !strings.Contains(del, "Delete") {
				t.Fatalf("%dx%d: the delete confirmation names nothing:\n%s", w, h, del)
			}
			modal := sized(t, cfg, w, h, "enter")
			if modal.view != viewModal {
				t.Fatalf("%dx%d: view %v, want the password modal", w, h, modal.view)
			}
			out := stripANSI(modal.render())
			label := "password"
			if newLayout(w, h).Inner < 24 {
				label = "pw"
			}
			if !strings.Contains(out, label) {
				t.Fatalf("%dx%d: no password field:\n%s", w, h, out)
			}
		}
	}
}
