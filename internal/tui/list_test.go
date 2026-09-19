package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestList_SelectedCardDetailsAndLastUsed(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "192.168.1.20", "jdoe"), panicStore{})
	if err := h.m.app.State.Record("work"); err != nil {
		t.Fatal(err)
	}
	out := screen(h.m)
	for _, want := range []string{"WICKET", "work", "192.168.1.20", "jdoe", "last-used"} {
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
	if !strings.Contains(out, "[e] edit") || !strings.Contains(out, "[D]") || !strings.Contains(out, "delete") {
		t.Fatalf("compact footer missing edit/delete:\n%s", out)
	}
	if strings.Contains(out, "last-used") {
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
