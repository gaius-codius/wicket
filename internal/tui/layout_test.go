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
			for _, keys := range [][]string{nil, {"/"}, {"n"}, {"?"}, {"D"}, {"enter"}} {
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

// Every form field has to be reachable. The form used to render all twelve
// rows regardless of height, so the last ones were off screen while ctrl+s
// still saved them.
func TestForm_ScrollsToTheFocusedField(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 14, "n")
	for id := range fieldCount {
		m = focusField(t, m, id)
		out := stripANSI(m.render())
		label := truncate(formLabels[id]+":", len(formLabels[id])+1)
		if !strings.Contains(out, label) {
			t.Fatalf("field %q not on screen at 80x14:\n%s", formLabels[id], out)
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
