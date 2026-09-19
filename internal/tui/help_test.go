package tui

import (
	"strings"
	"testing"
)

func TestHelp_ListKeysAndClose(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	h.m = press(h.m, "?")
	if h.m.view != viewHelp {
		t.Fatal("want help")
	}
	out := screen(h.m)
	for _, k := range []string{"j/k", "enter", "n", "e", "D", "?", "q"} {
		if !strings.Contains(out, k) {
			t.Fatalf("help missing %q:\n%s", k, out)
		}
	}
	h.m = press(h.m, "?")
	if h.m.view != viewList || h.m.quit {
		t.Fatal("? should close help without quit")
	}
	h.m = press(h.m, "?")
	h.m = press(h.m, "esc")
	if h.m.view != viewList || h.m.quit {
		t.Fatal("esc should close help")
	}
	h.m = press(h.m, "?")
	h.m = press(h.m, "q")
	if h.m.view != viewList || h.m.quit {
		t.Fatal("q in help should close, not quit")
	}
	h.m = press(h.m, "q")
	if !h.m.quit {
		t.Fatal("q on list quits")
	}
}

func TestHelp_FormKeys(t *testing.T) {
	h := newHarness(t, "", nil)
	h.m = press(h.m, "n")
	// unfocus text field so ? opens help
	for i := 0; i < fieldCount; i++ {
		if !h.m.form.textFocused() {
			break
		}
		h.m = press(h.m, "tab")
	}
	h.m = press(h.m, "?")
	out := screen(h.m)
	for _, k := range []string{"tab", "ctrl+s", "esc"} {
		if !strings.Contains(out, k) {
			t.Fatalf("form help missing %q:\n%s", k, out)
		}
	}
	h.m = press(h.m, "q")
	if h.m.view != viewForm {
		t.Fatal("q in help returns to form")
	}
	h.m = press(h.m, "q")
	if h.m.view != viewForm || h.m.quit {
		t.Fatal("q on an unfocused form field should do nothing")
	}
	h.m = press(h.m, "esc")
	if h.m.view != viewList || h.m.quit {
		t.Fatal("esc on an unchanged form returns to list without process exit")
	}
}
