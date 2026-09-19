package tui

import (
	"strings"
	"testing"
)

func TestStyles_Chrome(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), panicStore{})
	raw := h.m.View().Content
	plain := stripANSI(raw)
	if !strings.Contains(plain, "WICKET") {
		t.Fatal("title")
	}
	if !strings.ContainsAny(raw, "╭╮╯╰┌┐┘└") {
		t.Fatalf("want rounded/box border in %q", raw)
	}
	if !strings.Contains(plain, "enter connect") || !strings.Contains(plain, "q quit") {
		t.Fatalf("footer keys:\n%s", plain)
	}
	if !strings.Contains(plain, "1 connection") || !strings.Contains(plain, "────") {
		t.Fatalf("header context and divider:\n%s", plain)
	}
}
