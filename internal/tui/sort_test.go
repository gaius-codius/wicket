package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

// profilesTOML writes one profile per name, each on its own host.
func profilesTOML(names ...string) string {
	var b strings.Builder
	b.WriteString("[general]\n")
	for i, n := range names {
		fmt.Fprintf(&b, "[[profiles]]\nname = %q\nhost = \"h%d\"\nuser = \"u\"\nscale = 100\n", n, i)
	}
	return b.String()
}

// shownOrder is the names of the visible profiles, top to bottom.
func shownOrder(m Model) []string {
	ps := m.profiles()
	var out []string
	for _, i := range m.visible() {
		out = append(out, ps[i].Name)
	}
	return out
}

func selectedName(t *testing.T, m Model) string {
	t.Helper()
	p, ok := m.selected()
	if !ok {
		t.Fatal("nothing selected")
	}
	return p.Name
}

// usedAt stands in for state.toml: a, e used at the same moment, c later,
// b and d never.
func usedAt(m Model) Model {
	t0 := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	m.used = map[string]time.Time{"a": t0, "e": t0, "c": t0.Add(time.Hour)}
	return m
}

func TestList_SortRecentFirstIsStableAndKeepsSelection(t *testing.T) {
	h := newHarness(t, profilesTOML("a", "b", "c", "d", "e"), panicStore{})
	h.m = usedAt(h.m)
	h.m = press(h.m, "j") // b
	h.m = press(h.m, "s")
	// Most recent first; a and e tie and keep their file order; the
	// never-used b and d go last, also in file order.
	if got, want := strings.Join(shownOrder(h.m), ","), "c,a,e,b,d"; got != want {
		t.Fatalf("recent order = %s, want %s", got, want)
	}
	if got := selectedName(t, h.m); got != "b" {
		t.Fatalf("sorting moved the selection to %q", got)
	}
	if out := screen(h.m); !strings.Contains(out, "5 connections · recent first") {
		t.Fatalf("header should say the sort is on:\n%s", out)
	}
	// Moving follows the order on screen.
	h.m = press(h.m, "k")
	if got := selectedName(t, h.m); got != "e" {
		t.Fatalf("k from b in recent order selected %q, want e", got)
	}
	h.m = press(h.m, "s")
	if got, want := strings.Join(shownOrder(h.m), ","), "a,b,c,d,e"; got != want {
		t.Fatalf("file order = %s, want %s", got, want)
	}
	if got := selectedName(t, h.m); got != "e" {
		t.Fatalf("unsorting moved the selection to %q", got)
	}
	if out := screen(h.m); strings.Contains(out, "recent first") {
		t.Fatalf("header still says recent first:\n%s", out)
	}
}

// The filter picks the first match on screen, which in recent order is not
// the first in the file.
func TestList_FilterUnderSortSelectsFirstShown(t *testing.T) {
	h := newHarness(t, profilesTOML("a", "b", "c", "d", "e"), panicStore{})
	h.m = usedAt(h.m)
	h.m = press(h.m, "s", "j", "j") // a, then e, then b: "h" in the host keeps it
	h.m = press(h.m, "/")
	h.m = typeInto(h.m, "h")
	if got := selectedName(t, h.m); got != "b" {
		t.Fatalf("a filter that still matches b moved the selection to %q", got)
	}
	h.m = press(h.m, "backspace")
	h.m = typeInto(h.m, "h4") // only e's host
	if got := selectedName(t, h.m); got != "e" {
		t.Fatalf("selection %q, want e", got)
	}
	h.m = press(h.m, "esc")
	if got := selectedName(t, h.m); got != "e" {
		t.Fatalf("clearing the filter moved the selection to %q", got)
	}
}

// After a delete the selection goes to the profile drawn after it, which in
// recent order is not the next one in the file.
func TestList_DeleteSelectsNeighbourOnScreen(t *testing.T) {
	h := newHarness(t, profilesTOML("a", "b", "c", "d", "e"), nil)
	h.m = usedAt(h.m)
	h.m = press(h.m, "s") // c, a, e, b, d with a selected
	if got := selectedName(t, h.m); got != "a" {
		t.Fatalf("setup selected %q", got)
	}
	h.m = press(h.m, "D", "y")
	if got := selectedName(t, h.m); got != "e" {
		t.Fatalf("after deleting a, selected %q, want e (next on screen)", got)
	}
	// The last one on screen hands the selection back up.
	h.m = press(h.m, "G")
	last := selectedName(t, h.m)
	h.m = press(h.m, "D", "y")
	order := shownOrder(h.m)
	if got := selectedName(t, h.m); got != order[len(order)-1] || got == last {
		t.Fatalf("after deleting the last row %q, selected %q; order %v", last, got, order)
	}
}

func TestList_SortKeyIsOffered(t *testing.T) {
	h := newHarness(t, profilesTOML("a"), panicStore{})
	if !strings.Contains(screen(h.m), "s sort") {
		t.Fatalf("footer should offer s:\n%s", screen(h.m))
	}
	var found bool
	for _, k := range helpKeys(viewList) {
		found = found || k.key == "s"
	}
	if !found {
		t.Fatal("help should list s")
	}
}

// Compact keeps unselected rows to their names (UX-001): the last-used time
// is for the selected row only.
func TestList_CompactHidesLastUsedOnUnselectedRows(t *testing.T) {
	h := newHarness(t, profilesTOML("work", "lab"), panicStore{})
	nm, _ := h.m.Update(teaWin(50, 20))
	h.m = nm.(Model)
	out := screen(h.m)
	if ln := lineWith(out, "lab"); ln == "" || strings.Contains(ln, "never") || strings.Contains(ln, "h1") {
		t.Fatalf("compact unselected row shows more than its name: %q\n%s", ln, out)
	}
	if !strings.Contains(lineWith(out, "▌ work"), "never") {
		t.Fatalf("compact selected row should keep its last-used time:\n%s", out)
	}
	// At normal width every row has it.
	nm, _ = h.m.Update(teaWin(80, 20))
	h.m = nm.(Model)
	if ln := lineWith(screen(h.m), "lab"); !strings.Contains(ln, "never") {
		t.Fatalf("normal unselected row lacks last-used: %q", ln)
	}
}

// No list line may be wider than the panel at any width the overflow sweep
// covers, with long names, hosts and time labels and a filter drawing its
// highlight. A wider line wraps inside the frame, costing a line the budget
// never counted.
func TestList_RowsNeverOverflow(t *testing.T) {
	names := []string{"a-connection-with-a-long-name", "Élan", "short"}
	var b strings.Builder
	b.WriteString("[general]\n")
	for i, n := range names {
		fmt.Fprintf(&b, "[[profiles]]\nname = %q\nhost = \"host-%d.a-rather-long-domain.example.invalid\"\nuser = \"u\"\nscale = 100\n", n, i)
	}
	now := time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)
	for _, q := range []string{"", "o"} {
		for w := widthTiny; w <= 130; w++ {
			for _, hgt := range []int{8, 30} {
				m := sized(t, b.String(), w, hgt)
				m.now = func() time.Time { return now }
				// "yesterday 13:21" is the longest label humanTime gives.
				m.used = map[string]time.Time{names[0]: now.Add(-26 * time.Hour), names[1]: now.Add(-3 * time.Minute)}
				if q != "" {
					m = press(m, "/")
					m = typeInto(m, q)
					m = press(m, "enter")
				}
				for _, sel := range []int{0, 1, 2} {
					m.cursor = sel
					lo := m.panelLayout()
					if lo.Tiny {
						continue
					}
					m.fitChrome(&lo)
					for _, ln := range strings.Split(m.viewList(lo), "\n") {
						if got := lipgloss.Width(ln); got > lo.Inner {
							t.Fatalf("%dx%d q=%q sel=%d: %d-cell line in a %d-cell panel:\n%s",
								w, hgt, q, sel, got, lo.Inner, stripANSI(ln))
						}
					}
				}
			}
		}
	}
}

// When a row is short of room the host is cut before the name.
func TestList_HostGivesWayBeforeName(t *testing.T) {
	name := "a-connection-with-a-long"
	host := "host.a-rather-long-domain.example.invalid"
	ps := []config.Profile{{Name: name, Host: host}}
	c := columnWidths(ps, 56, len("yesterday 13:21"))
	if c.name != len(name) || c.time == 0 {
		t.Fatalf("name or time squeezed: %+v", c)
	}
	if c.host == 0 || c.host >= len(host) {
		t.Fatalf("host should be cut, not dropped or whole: %+v", c)
	}
	if c.width() > 56 {
		t.Fatalf("columns %+v are %d wide in 56", c, c.width())
	}
	// Narrower still, the host goes entirely and the name keeps its place.
	c = columnWidths(ps, 40, len("yesterday 13:21"))
	if c.host != 0 || c.name < 8 || c.width() > 40 {
		t.Fatalf("at 40: %+v", c)
	}
}

func TestMatchSpan_RuneSafeCaseFolding(t *testing.T) {
	cases := []struct {
		s, q       string
		start, end int
	}{
		{"École", "éco", 0, 3},
		{"xÉCOLE", "école", 1, 6},
		// Offsets are runes: after "ÉÉ-" a byte offset would be 5.
		{"ÉÉ-work", "work", 3, 7},
		// The Kelvin sign lowers to a one-byte "k", so byte offsets found
		// in a lowered copy would not even point into the same text.
		{"\u212Aé-École", "éco", 3, 6},
		{"\u212A", "k", 0, 1},
		{"work", "WORK", 0, 4},
		{"work", "lab", -1, -1},
		{"work", "", -1, -1},
	}
	for _, c := range cases {
		if s, e := matchSpan(c.s, c.q); s != c.start || e != c.end {
			t.Errorf("matchSpan(%q, %q) = %d, %d; want %d, %d", c.s, c.q, s, e, c.start, c.end)
		}
	}
}

var underlinedSelected = regexp.MustCompile(`\x1b\[(?:[0-9]+;)*4;[^m]*48;[^m]*mÉ`)

// A filter underlines what it matched, case-insensitively and by rune, on
// the selection background, without changing the row's width or text.
func TestList_FilterHighlightsMatchWidthNeutral(t *testing.T) {
	t.Setenv("WICKET_THEME", "wicket-dark")
	h := newHarness(t, profilesTOML("École", "xÉcole", "lab"), panicStore{})
	cols := columnWidths(h.m.profiles(), 76, h.m.timeWidth(h.m.profiles()))
	plain := h.m.row(h.m.profiles()[0], true, cols, 76)

	h.m = press(h.m, "/")
	h.m = typeInto(h.m, "éco")
	if got := strings.Join(shownOrder(h.m), ","); got != "École,xÉcole" {
		t.Fatalf("filter matched %s", got)
	}
	lit := h.m.row(h.m.profiles()[0], true, cols, 76)
	if stripANSI(lit) != stripANSI(plain) || lipgloss.Width(lit) != 76 {
		t.Fatalf("highlight changed the row:\n%q\n%q", stripANSI(plain), stripANSI(lit))
	}
	if !underlinedSelected.MatchString(lit) {
		t.Fatalf("match should be underlined on the selection background:\n%q", lit)
	}
	if !strings.Contains(stripANSI(lit), "École") {
		t.Fatalf("row lost its name: %q", stripANSI(lit))
	}
	// An unselected row underlines the same letters, after the x.
	other := h.m.row(h.m.profiles()[1], false, cols, 76)
	if !regexp.MustCompile(`x\x1b\[m\x1b\[(?:[0-9]+;)*4;[^m]*mÉ`).MatchString(other) {
		t.Fatalf("unselected match not underlined after the x:\n%q", other)
	}
}

// In terminal mode the selected row is reverse video, and so is its match.
func TestList_FilterHighlightKeepsReverseInTerminalMode(t *testing.T) {
	t.Setenv("WICKET_THEME", "terminal")
	h := newHarness(t, profilesTOML("École"), panicStore{})
	h.m = press(h.m, "/")
	h.m = typeInto(h.m, "éco")
	cols := columnWidths(h.m.profiles(), 76, h.m.timeWidth(h.m.profiles()))
	lit := h.m.row(h.m.profiles()[0], true, cols, 76)
	params := regexp.MustCompile(`\x1b\[([0-9;]*)mÉ`).FindStringSubmatch(lit)
	if params == nil {
		t.Fatalf("no style on the match:\n%q", lit)
	}
	set := map[string]bool{}
	for _, p := range strings.Split(params[1], ";") {
		set[p] = true
	}
	if !set["4"] || !set["7"] {
		t.Fatalf("match should be underlined and reversed, got %q:\n%q", params[1], lit)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"work":          "work",
		"lab-2.example": "lab-2.example",
		"my box":        "'my box'",
		"it's":          `'it'\''s'`,
		`a"b`:           `'a"b'`,
		"$HOME":         "'$HOME'",
		"a*":            "'a*'",
		"~x":            "'~x'",
		"Élan":          "'Élan'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestList_WideShowsQuotedShellCommand(t *testing.T) {
	h := newHarness(t, profilesTOML("my box", "it's"), panicStore{})
	nm, _ := h.m.Update(teaWin(140, 30))
	h.m = nm.(Model)
	if out := screen(h.m); !strings.Contains(out, "from a shell  wicket connect 'my box'") {
		t.Fatalf("missing quoted command:\n%s", out)
	}
	h.m = press(h.m, "j")
	if out := screen(h.m); !strings.Contains(out, `wicket connect 'it'\''s'`) {
		t.Fatalf("missing quoted command:\n%s", out)
	}
}

// The last-used snapshot is retaken after a connect, which is what records
// the time; without that the row would say "never" until Wicket restarts.
func TestList_LastUsedRefreshedAfterConnect(t *testing.T) {
	_ = withFakeRDP(t)
	store := secret.NewMemory()
	h := newHarness(t, fixtureTOML("work", "h", "u"), store)
	p, _ := h.m.app.Cfg.Profile("work")
	_ = store.Upsert(h.m.identity(p), mustPassword(t, "pw"))
	if !strings.Contains(lineWith(screen(h.m), "▌ work"), "never") {
		t.Fatalf("setup:\n%s", screen(h.m))
	}
	h.m = press(h.m, "enter")
	if ln := lineWith(screen(h.m), "▌ work"); !strings.Contains(ln, "just now") {
		t.Fatalf("row not refreshed after connect: %q\n%s", ln, screen(h.m))
	}
}

// recordElsewhere writes a last-used time the way `wicket connect` in another
// shell would: through its own store, behind this model's back.
func recordElsewhere(t *testing.T, h *harness, name string) {
	t.Helper()
	other, err := config.OpenState(h.state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Record(name); err != nil {
		t.Fatal(err)
	}
}

// A connect made by another Wicket shows up while the list is open; without
// the poll the row said "never" until Wicket restarted.
func TestList_LastUsedPollSeesAnotherWriter(t *testing.T) {
	h := newHarness(t, profilesTOML("a", "work"), panicStore{})
	h.m = press(h.m, "s") // recent first
	recordElsewhere(t, h, "work")
	if ln := lineWith(screen(h.m), "work"); !strings.Contains(ln, "never") {
		t.Fatalf("setup: %q", ln)
	}
	nm, cmd := h.m.Update(usedPollMsg{})
	h.m = nm.(Model)
	if ln := lineWith(screen(h.m), "work"); !strings.Contains(ln, "just now") {
		t.Fatalf("poll did not pick up the other write: %q\n%s", ln, screen(h.m))
	}
	if got := shownOrder(h.m); len(got) != 2 || got[0] != "work" {
		t.Fatalf("recent-first order not refreshed: %v", got)
	}
	if cmd == nil {
		t.Fatal("poll stopped while the list is shown")
	}
}

// Polling costs a stat: an unchanged file is not read again.
func TestList_LastUsedPollSkipsAnUnchangedFile(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	recordElsewhere(t, h, "work")
	h.m.refreshUsed()
	marker := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	h.m.used = map[string]time.Time{"work": marker}
	nm, _ := h.m.Update(usedPollMsg{})
	if got := nm.(Model).used["work"]; !got.Equal(marker) {
		t.Fatalf("unchanged state.toml was reread: %v", got)
	}
}

// The poll stops while the list is hidden, and coming back to the list both
// catches up at once and starts the poll again.
func TestList_LastUsedPollResumesOnReturn(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	h.m = press(h.m, "?")
	nm, cmd := h.m.Update(usedPollMsg{})
	h.m = nm.(Model)
	if cmd != nil || !h.m.usedPollIdle {
		t.Fatalf("poll kept running behind the help view (cmd %v, idle %v)", cmd != nil, h.m.usedPollIdle)
	}
	recordElsewhere(t, h, "work")
	h.m = press(h.m, "esc")
	if h.m.view != viewList {
		t.Fatalf("view %v, want list", h.m.view)
	}
	if ln := lineWith(screen(h.m), "▌ work"); !strings.Contains(ln, "just now") {
		t.Fatalf("return to the list did not catch up: %q", ln)
	}
	if h.m.usedPollIdle {
		t.Fatal("poll not restarted on return to the list")
	}
}
