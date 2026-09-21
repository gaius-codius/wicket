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
	// The session view, before and after a stop has put a status line up.
	base := newHarness(t, cfg, secret.NewMemory())
	p, _ := base.m.app.Cfg.Profile("a-connection-with-a-long-name")
	for _, stopping := range []bool{false, true} {
		m := withSession(base.m, p, stopping)
		for w := widthTiny; w <= 130; w++ {
			for h := heightTiny; h <= 30; h++ {
				nm, _ := m.Update(teaWin(w, h))
				gotW, gotH := measure(nm.(Model).render())
				if gotW > w || gotH > h {
					if bad < 10 {
						t.Errorf("%dx%d session (stopping %v) rendered %dx%d", w, h, stopping, gotW, gotH)
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

// No form line is wider than the panel, the form never uses more lines than
// it was given, and every field can be brought on screen by moving focus to
// it -- at every size the overflow sweep covers, 80x14 among them. A line
// wider than the panel wraps inside the frame, costing a line the budget
// never counted; the size placeholder did that at narrow widths, because the
// text input draws one cell more than the width it is given.
func TestForm_RowsNeverWrap(t *testing.T) {
	cfg := fixtureTOML("work", "host.invalid", "user")
	for _, key := range []string{"n", "e"} {
		// One form, resized, rather than one harness per size: the sweep
		// is large and the config on disk plays no part in it.
		opened := sized(t, cfg, 80, 24, key)
		// Past panelNormal the form's panel stops growing, so every wider
		// window draws the same form; the wide layout and the sweep's
		// widest size stand in for the rest.
		widths := []int{widthWide, 130}
		for w := widthTiny; w <= panelNormal; w++ {
			widths = append(widths, w)
		}
		for _, w := range widths {
			for h := heightTiny; h <= 30; h++ {
				nm, _ := opened.Update(teaWin(w, h))
				m := nm.(Model)
				for _, id := range m.form.fields() {
					m = focusField(t, m, id)
					lo := m.panelLayout()
					m.fitChrome(&lo)
					body := strings.Split(m.viewForm(lo), "\n")
					if len(body) > max(lo.Budget, 1) {
						t.Fatalf("%dx%d %q, %s focused: %d lines for a %d-line budget:\n%s",
							w, h, key, formLabels[id], len(body), lo.Budget, stripANSI(m.render()))
					}
					for i, ln := range body {
						if got := lipgloss.Width(ln); got > lo.Inner {
							t.Fatalf("%dx%d %q: line %d is %d cells in a %d-cell panel:\n%s",
								w, h, key, i, got, lo.Inner, stripANSI(m.render()))
						}
					}
					if !focusedRowShown(body, id, lo.Inner) {
						t.Fatalf("%dx%d %q: focused field %q is not on screen:\n%s",
							w, h, key, formLabels[id], stripANSI(m.render()))
					}
				}
			}
		}
	}
}

// focusedRowShown reports whether body holds field id's row with the focus
// bar on it.
func focusedRowShown(body []string, id, inner int) bool {
	labelW, _ := formColumns(inner)
	want := "▌ " + truncate(formLabels[id]+":", labelW)
	for _, ln := range body {
		if strings.HasPrefix(stripANSI(ln), want) {
			return true
		}
	}
	return false
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

func slicesHasKey(hs []keyHint, key string) bool {
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
			label := "password"
			if newLayout(w, h).Inner < 24 {
				label = "pw"
			}
			// Once with nothing to report, once with an error on screen: the
			// error used to take the field's line with it.
			for _, keys := range [][]string{{"enter"}, {"enter", "enter"}} {
				modal := sized(t, cfg, w, h, keys...)
				if modal.view != viewModal {
					t.Fatalf("%dx%d after %v: view %v, want the password modal", w, h, keys, modal.view)
				}
				out := stripANSI(modal.render())
				if !strings.Contains(out, label) {
					t.Fatalf("%dx%d after %v: no password field:\n%s", w, h, keys, out)
				}
			}
		}
	}
}

// A warning must not be the first thing a short terminal gives up. The footer
// keys are in the help view and the README; a status line that is never drawn
// is simply lost.
func TestRender_ShortTerminalKeepsTheStatus(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 24)
	m.setStatus("could not save password", statusError)
	for h := heightTiny; h <= 12; h++ {
		nm, _ := m.Update(teaWin(80, h))
		out := stripANSI(nm.(Model).render())
		if !strings.Contains(out, "could not save password") {
			t.Fatalf("height %d dropped the status:\n%s", h, out)
		}
	}
}

// The blank line above the error is worth less than the error, so a panel
// with one line to spare spends it on the message rather than the gap.
func TestModal_ShowsTheErrorWhenOneLineIsLeft(t *testing.T) {
	m := sized(t, fixtureTOML("work", "host.invalid", "user"), 80, 9, "enter", "enter")
	out := stripANSI(m.render())
	if !strings.Contains(out, "password") || !strings.Contains(out, "✗ password required") {
		t.Fatalf("80x9 shows the field but not why it is still here:\n%s", out)
	}
}

// eightProfiles is the QA fixture: enough profiles that a short terminal
// cannot show them all, each with a domain so the selected card has details.
func eightProfiles() string {
	var b strings.Builder
	b.WriteString("[general]\n")
	for _, n := range []string{"work", "emile-pc", "cafe", "laptop", "bobs-box", "quote", "newbox", "testnet"} {
		b.WriteString("[[profiles]]\nname = \"" + n + "\"\nhost = \"" + n + ".example\"\nuser = \"alice\"\ndomain = \"CORP\"\nscale = 100\n")
	}
	return b.String()
}

// With a filter open, the filter line and the selected match outrank every
// piece of chrome. At ten rows or fewer the results used to collapse to a
// lone "…", and at eight or fewer the query went too, while the divider,
// blank rows and status line stayed.
func TestRender_FilterKeepsTheQueryAndAMatch(t *testing.T) {
	cfg := eightProfiles()
	for _, w := range []int{20, 30, 50, 60, 100, 130} {
		for h := heightTiny; h <= 14; h++ {
			m := sized(t, cfg, w, h, "/", "t")
			m.setStatus("session stopped", statusInfo)
			out := stripANSI(m.render())
			lines := strings.Split(out, "\n")
			var query, match bool
			for _, ln := range lines {
				ln = strings.Trim(ln, "│ ")
				query = query || strings.HasPrefix(ln, "/ t")
				match = match || strings.HasPrefix(ln, "▌ ")
				if ln == "…" {
					t.Errorf("%dx%d: a lone ellipsis stands in for the matches:\n%s", w, h, out)
				}
			}
			if !query || !match {
				t.Errorf("%dx%d: query shown %v, selected match shown %v:\n%s", w, h, query, match, out)
			}
		}
	}
}

// A short terminal gives up spacing, the divider and then footer lines before
// list rows, cutting the footer to one line before dropping it, and says in
// the header that more rows exist. It used to keep a four-line footer and the
// spacing and show the selected profile alone.
func TestRender_ShortListKeepsRowsBeforeTheFooter(t *testing.T) {
	cfg := eightProfiles()
	names := []string{"work", "emile-pc", "cafe", "laptop"}
	for _, size := range [][2]int{{30, 10}, {50, 10}, {60, 12}, {80, 12}, {100, 10}} {
		m := sized(t, cfg, size[0], size[1])
		m.setStatus("session ended", statusInfo)
		out := stripANSI(m.render())
		for _, n := range names {
			if !strings.Contains(out, "  "+n) && !strings.Contains(out, "▌ "+n) {
				t.Errorf("%dx%d: row %q missing:\n%s", size[0], size[1], n, out)
			}
		}
		if !strings.Contains(out, "of 8") {
			t.Errorf("%dx%d: nothing says the list is windowed:\n%s", size[0], size[1], out)
		}
		if !strings.Contains(out, "session ended") || !strings.Contains(out, "? help") {
			t.Errorf("%dx%d: status or the way to help missing:\n%s", size[0], size[1], out)
		}
	}
	// When everything fits, the header does not claim a window.
	if out := stripANSI(sized(t, cfg, 80, 30).render()); strings.Contains(out, "of 8") || !strings.Contains(out, "8 connections") {
		t.Errorf("80x30 claims a window:\n%s", out)
	}
}

// Blank spacer rows go before content: a scroll cue, the form's help line,
// the host a delete names, and who a session is connected to all used to be
// dropped while the blank rows around them stayed.
func TestRender_SpacersGoBeforeContent(t *testing.T) {
	cfg := eightProfiles()
	form := stripANSI(sized(t, cfg, 60, 8, "e").render())
	if !strings.Contains(form, "▼") || !strings.Contains(form, "What the list") {
		t.Errorf("form 60x8 lost its cue or help:\n%s", form)
	}
	del := stripANSI(sized(t, cfg, 40, 8, "D").render())
	if !strings.Contains(del, "work.example") {
		t.Errorf("delete 40x8 lost the host:\n%s", del)
	}
	base := newHarness(t, cfg, secret.NewMemory())
	p, _ := base.m.app.Cfg.Profile("work")
	nm, _ := withSession(base.m, p, false).Update(teaWin(30, 8))
	if ses := stripANSI(nm.(Model).render()); !strings.Contains(ses, "elapsed") {
		t.Errorf("session 30x8 lost its detail line:\n%s", ses)
	}
}

// Narrow layouts shorten text with an ellipsis rather than cutting it: an
// empty field's hint was clipped mid-word, and a value too long for its
// column lost its tail with no sign of it.
func TestForm_NarrowTextIsMarkedAsCut(t *testing.T) {
	cfg := fixtureTOML("work", "a-rather-long-host.example.invalid", "u")
	m := sized(t, cfg, 59, 30, "e")
	m = focusField(t, m, fieldScale)
	out := stripANSI(m.render())
	var pw, host string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "password:") && !strings.Contains(ln, "forget") {
			pw = ln
		}
		if strings.Contains(ln, "host:") {
			host = ln
		}
	}
	if !strings.Contains(pw, "…") {
		t.Errorf("the password hint is cut without an ellipsis: %q", pw)
	}
	m = sized(t, cfg, 30, 30, "e")
	m = focusField(t, m, fieldScale)
	out = stripANSI(m.render())
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "host:") {
			host = ln
		}
		if strings.Contains(ln, "scale:") && !strings.Contains(ln, "‹ 100% ›") {
			t.Errorf("30 wide: the scale does not say which: %q", ln)
		}
	}
	if !strings.Contains(host, "…") {
		t.Errorf("30 wide: the host is cut without an ellipsis: %q", host)
	}
}
