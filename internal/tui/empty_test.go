package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmptyState_FirstLaunchCreatesConfig(t *testing.T) {
	h := newHarness(t, "", nil)
	if h.m.view != viewList {
		t.Fatalf("view %v", h.m.view)
	}
	out := screen(h.m)
	if !strings.Contains(out, "No saved connections") {
		t.Fatalf("empty copy missing:\n%s", out)
	}
	if !strings.Contains(out, "n") {
		t.Fatalf("should mention n:\n%s", out)
	}
	st, err := os.Stat(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %04o", st.Mode().Perm())
	}
	st, err = os.Stat(filepath.Dir(h.cfg))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %04o", st.Mode().Perm())
	}
	if len(h.m.profiles()) != 0 {
		t.Fatal("want zero profiles")
	}
}

func TestEmpty_EnterEditDeleteNoOps(t *testing.T) {
	h := newHarness(t, "", nil)
	for _, k := range []string{"enter", "e", "D"} {
		m := press(h.m, k)
		if m.view != viewList {
			t.Fatalf("%s opened view %v", k, m.view)
		}
		if len(m.profiles()) != 0 {
			t.Fatal("wrote a profile")
		}
	}
}
