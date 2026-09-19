package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
)

func TestList_SelectedCardDetailsAndLastUsed(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "192.168.1.20", "jdoe"), panicStore{})
	if err := h.m.app.State.Record("work"); err != nil {
		t.Fatal(err)
	}
	out := screen(h.m)
	for _, want := range []string{"WICKET", "work", "192.168.1.20", "jdoe", "last used", "just now"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "never") {
		t.Fatalf("recorded last-used still never:\n%s", out)
	}
}

func TestList_NeverWhenAbsent(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	out := screen(h.m)
	if !strings.Contains(out, "never") {
		t.Fatalf("want never:\n%s", out)
	}
}

func TestList_CompactShowsNameAndHost(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "host1", "u"), panicStore{})
	nm, _ := h.m.Update(teaWin(50, 20))
	h.m = nm.(Model)
	out := screen(h.m)
	if !strings.Contains(out, "work") || !strings.Contains(out, "host1") {
		t.Fatalf("compact selected card:\n%s", out)
	}
	if !strings.Contains(out, "e edit") || !strings.Contains(out, "D delete") {
		t.Fatalf("compact footer missing edit/delete:\n%s", out)
	}
	if strings.Contains(out, "last used") {
		t.Fatalf("compact should not show last-used:\n%s", out)
	}
}

func TestList_UnselectedStayCompact(t *testing.T) {
	body := fixtureTOML("work", "h1", "u1") + `
[[profiles]]
name = "lab"
host = "h2"
user = "u2"
`
	h := newHarness(t, body, panicStore{})
	out := screen(h.m)
	if !strings.Contains(out, "lab") {
		t.Fatal(out)
	}
	if strings.Count(out, "h2") != 0 && strings.Contains(out, "last-used") && strings.Contains(out, "h2") {
		// unselected lab must not show host details
		lines := strings.Split(out, "\n")
		for _, ln := range lines {
			if strings.Contains(ln, "lab") && strings.Contains(ln, "h2") && strings.Contains(ln, "last-used") {
				t.Fatalf("unselected expanded:\n%s", out)
			}
		}
	}
}

func TestList_MoveKeys(t *testing.T) {
	body := fixtureTOML("a", "h", "u") + `
[[profiles]]
name = "b"
host = "h2"
user = "u2"
`
	h := newHarness(t, body, panicStore{})
	h.m = press(h.m, "j")
	if h.m.cursor != 1 {
		t.Fatalf("cursor %d", h.m.cursor)
	}
	h.m = press(h.m, "k")
	if h.m.cursor != 0 {
		t.Fatalf("cursor %d", h.m.cursor)
	}
}

func TestList_TinyResize(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	nm, _ := h.m.Update(teaWin(10, 3))
	h.m = nm.(Model)
	out := screen(h.m)
	if !strings.Contains(out, "resize terminal") {
		t.Fatalf("%s", out)
	}
	h.m = press(h.m, "n")
	if h.m.view != viewList {
		t.Fatal("tiny should ignore n")
	}
	h.m = press(h.m, "q")
	if !h.m.quit {
		t.Fatal("q should quit")
	}
}

func TestListWindow(t *testing.T) {
	start, end := listWindow(30, 29, 5, 4)
	if start > 29 || end <= 29 {
		t.Fatalf("selected not in window [%d,%d)", start, end)
	}
	if start == 0 {
		t.Fatal("window should not start at 0 when cursor is last")
	}
	start, end = listWindow(2, 0, 20, 4)
	if start != 0 || end != 2 {
		t.Fatalf("small list [%d,%d)", start, end)
	}
}

func TestList_ViewportKeepsSelectionVisible(t *testing.T) {
	var b strings.Builder
	b.WriteString("[general]\n")
	for i := 0; i < 30; i++ {
		b.WriteString(fmt.Sprintf(`[[profiles]]
name = "p%02d"
host = "h"
user = "u"
client = "sdl-freerdp3"
dynamic_resolution = true
scale = 100
`, i))
	}
	h := newHarness(t, b.String(), panicStore{})
	nm, _ := h.m.Update(teaWin(80, 12))
	h.m = nm.(Model)
	for i := 0; i < 29; i++ {
		h.m = press(h.m, "j")
	}
	out := screen(h.m)
	if !strings.Contains(out, "p29") {
		t.Fatalf("selected p29 missing:\n%s", out)
	}
	if strings.Contains(out, "p00") {
		t.Fatalf("first profile still visible:\n%s", out)
	}
}

func TestList_EscNoOp(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	h.m = press(h.m, "esc")
	if h.m.view != viewList || h.m.quit {
		t.Fatal("esc should no-op")
	}
}

func TestList_StripKeyWarningOnStatus(t *testing.T) {
	body := `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
password = "nope"
`
	h := newHarness(t, body, panicStore{})
	if h.m.status == "" {
		t.Fatal("want strip warning on status")
	}
}

func TestHumanTime(t *testing.T) {
	loc := time.FixedZone("AEST", 10*3600)
	now := time.Date(2026, 9, 20, 15, 0, 0, 0, loc)
	cases := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-20 * time.Second), "just now"},
		{now.Add(-12 * time.Minute), "12 min ago"},
		{time.Date(2026, 9, 20, 9, 5, 0, 0, loc), "today 09:05"},
		{time.Date(2026, 9, 19, 13, 21, 0, 0, loc), "yesterday 13:21"},
		{time.Date(2026, 9, 16, 8, 0, 0, 0, loc), "Wed 08:00"},
		{time.Date(2026, 3, 2, 8, 0, 0, 0, loc), "2 Mar"},
		{time.Date(2025, 12, 31, 8, 0, 0, 0, loc), "31 Dec 2025"},
	}
	for _, c := range cases {
		if got := humanTime(c.t, now); got != c.want {
			t.Errorf("humanTime(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}

func TestDisplayLine(t *testing.T) {
	p := config.Profile{Scale: 100, DynamicResolution: true}
	if got := displayLine(p); got != "window · dynamic resolution · scale 100%" {
		t.Fatalf("got %q", got)
	}
	p = config.Profile{Size: "1920x1080", Fullscreen: true, Scale: 140}
	if got := displayLine(p); got != "1920x1080 · fullscreen · scale 140%" {
		t.Fatalf("got %q", got)
	}
}

func twoProfiles() string {
	return fixtureTOML("work", "p-host-1", "u1") + `
[[profiles]]
name = "lab"
host = "p-host-2"
user = "u2"
`
}

func TestList_NormalShowsHostOnEveryRow(t *testing.T) {
	h := newHarness(t, twoProfiles(), panicStore{})
	nm, _ := h.m.Update(teaWin(80, 24))
	h.m = nm.(Model)
	out := screen(h.m)
	if !strings.Contains(out, "p-host-2") {
		t.Fatalf("unselected row should show host:\n%s", out)
	}
	if strings.Count(out, "user ") != 1 {
		t.Fatalf("only the selected row expands:\n%s", out)
	}
}

func TestList_WideTwoPane(t *testing.T) {
	h := newHarness(t, twoProfiles(), panicStore{})
	nm, _ := h.m.Update(teaWin(140, 30))
	h.m = nm.(Model)
	h.m = press(h.m, "j")
	out := screen(h.m)
	var detail string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "work") && strings.Contains(ln, "│") {
			detail = ln
		}
	}
	if detail == "" {
		t.Fatalf("want list and details side by side:\n%s", out)
	}
	for _, want := range []string{"host", "p-host-2", "u2", "last used", "display"} {
		if !strings.Contains(out, want) {
			t.Fatalf("details pane missing %q:\n%s", want, out)
		}
	}
	for _, ln := range strings.Split(out, "\n") {
		if w := lipgloss.Width(ln); w > 140 {
			t.Fatalf("line wider than terminal (%d):\n%s", w, ln)
		}
	}
}

func TestList_PanelCappedAndCentered(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	nm, _ := h.m.Update(teaWin(90, 30))
	h.m = nm.(Model)
	lines := strings.Split(screen(h.m), "\n")
	if len(lines) != 30 {
		t.Fatalf("want full-height canvas, got %d lines", len(lines))
	}
	var top string
	for _, ln := range lines {
		if strings.Contains(ln, "╭") {
			top = ln
			break
		}
	}
	left := strings.Index(top, "╭")
	if left <= 0 || lipgloss.Width(strings.TrimSpace(top)) != panelNormal {
		t.Fatalf("panel should be %d wide and centered:\n%q", panelNormal, top)
	}
}

func TestList_SelectedRowUsesSelectionBackground(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	raw := h.m.View().Content
	if !strings.Contains(raw, "48;") {
		t.Fatal("selected row should carry a background color")
	}
}

func TestList_FitsShortTerminal(t *testing.T) {
	var b strings.Builder
	b.WriteString("[general]\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "[[profiles]]\nname = \"p%02d\"\nhost = \"h\"\nuser = \"u\"\nscale = 100\n", i)
	}
	for _, size := range [][2]int{{50, 10}, {80, 12}, {140, 12}} {
		h := newHarness(t, b.String(), panicStore{})
		nm, _ := h.m.Update(teaWin(size[0], size[1]))
		h.m = nm.(Model)
		h.m.setStatus("session ended", false)
		out := screen(h.m)
		if n := len(strings.Split(out, "\n")); n > size[1] {
			t.Fatalf("%dx%d: %d lines:\n%s", size[0], size[1], n, out)
		}
		if !strings.Contains(out, "p00") || !strings.Contains(out, "session ended") || !strings.Contains(out, "quit") {
			t.Fatalf("%dx%d: selection, status, or footer missing:\n%s", size[0], size[1], out)
		}
	}
}
