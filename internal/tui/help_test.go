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
	for _, k := range []string{"j/k", "enter", "n", "e", "copy selected", "D", "?", "q"} {
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
	for _, k := range []string{"tab", "ctrl+s", "esc", "folder share name", "remove folder share"} {
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

const testVersion = "wicket 0.3.1 (abc1234)"

func TestHelp_ListShowsVersion(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	h.m.version = testVersion
	h.m = press(h.m, "?")
	if out := screen(h.m); !strings.Contains(out, testVersion) {
		t.Fatalf("list help missing version:\n%s", out)
	}
}

func TestHelp_VersionOnlyOnListHelp(t *testing.T) {
	m := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{}).m
	m.version = testVersion
	lo := layout{Inner: 76, Budget: 40}
	for _, v := range []view{viewLoadErr, viewForm, viewModal, viewDelete, viewSession, viewRetry} {
		m.helpFor = v
		if out := stripANSI(m.viewHelp(lo)); strings.Contains(out, "0.3.1") {
			t.Fatalf("help for view %d shows the version:\n%s", v, out)
		}
	}
	m.helpFor = viewList
	if out := stripANSI(m.viewHelp(lo)); !strings.Contains(out, testVersion) {
		t.Fatalf("list help missing version:\n%s", out)
	}
}

// The version line is the first thing a short panel sheds: with room for
// exactly the key lines, every key stays and the version goes.
func TestHelp_VersionDroppedBeforeKeys(t *testing.T) {
	m := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{}).m
	m.version = testVersion
	m.helpFor = viewList
	keys := helpKeys(viewList)
	lo := layout{Inner: 76, Budget: len(keys) + 1}
	if out := stripANSI(m.viewHelp(lo)); !strings.Contains(out, testVersion) {
		t.Fatalf("version should fit at budget %d:\n%s", lo.Budget, out)
	}
	lo.Budget = len(keys)
	out := stripANSI(m.viewHelp(lo))
	if strings.Contains(out, "0.3.1") {
		t.Fatalf("version should be dropped at budget %d:\n%s", lo.Budget, out)
	}
	for _, k := range keys {
		if !strings.Contains(out, k.label) {
			t.Fatalf("key %q dropped at budget %d:\n%s", k.label, lo.Budget, out)
		}
	}
	// Shorter still, help scrolls as before and the version never appears.
	lo.Budget = len(keys) - 3
	for top := 0; top <= len(keys); top++ {
		m.helpTop = top
		if out := stripANSI(m.viewHelp(lo)); strings.Contains(out, "0.3.1") {
			t.Fatalf("version shown while scrolling at top %d:\n%s", top, out)
		}
	}
	m.helpTop = len(keys)
	if out := stripANSI(m.viewHelp(lo)); !strings.Contains(out, keys[len(keys)-1].label) {
		t.Fatalf("last key unreachable:\n%s", out)
	}
}

func TestHelp_NoVersionLineWhenUnset(t *testing.T) {
	m := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{}).m
	m.helpFor = viewList
	lo := layout{Inner: 76, Budget: 40}
	if got, want := len(strings.Split(m.viewHelp(lo), "\n")), len(helpKeys(viewList)); got != want {
		t.Fatalf("got %d help lines, want %d", got, want)
	}
}
