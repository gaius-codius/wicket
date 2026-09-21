package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/secret"
)

// Exactly one trailing line terminator is dropped. Trimming all of them would
// accept a multi-line paste whose extra lines happen to be empty, while the
// same paste with text on the second line is rejected.
func TestPaste_DropsOneLineEndAndRejectsTheRest(t *testing.T) {
	m := sized(t, fixtureTOML("work", "h", "u"), 80, 24)
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"alpha", "alpha", false},
		{"alpha\n", "alpha", false},
		{"alpha\r\n", "alpha", false},
		{"alpha\n\n", "", true},
		{"alpha\r\n\r\n", "", true},
		{"alpha\nbeta", "", true},
		{"  spaced  \n", "spaced", false},
	} {
		ti := m.newInput("", false)
		ti.Focus()
		got, err := updateInput(ti, tea.PasteMsg{Content: tc.in}, false)
		switch {
		case tc.wantErr && !errors.Is(err, errMultilinePaste):
			t.Errorf("paste %q: err = %v, want a multi-line rejection", tc.in, err)
		case !tc.wantErr && err != nil:
			t.Errorf("paste %q: unexpected err %v", tc.in, err)
		case !tc.wantErr && got.Value() != tc.want:
			t.Errorf("paste %q: value %q, want %q", tc.in, got.Value(), tc.want)
		}
	}
}

// A rejected filter paste must not leave its error behind once the filter is
// usable again, nor discard a warning it did not raise.
func TestFilter_RejectedPasteErrorClearsAndRestores(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "host.invalid", "u"), secret.NewMemory())
	h.m.setStatus("saved, but the password could not be stored", statusWarning)

	h.m = press(h.m, "/")
	nm, _ := h.m.Update(tea.PasteMsg{Content: "a\nb"})
	h.m = nm.(Model)
	if h.m.statusKind != statusError || !strings.Contains(h.m.status, "line break") {
		t.Fatalf("status = %q kind=%v, want the paste rejection", h.m.status, h.m.statusKind)
	}

	h.m = typeInto(h.m, "work")
	// The warning comes back as a warning: restoring it as a routine note
	// would hide that something still needs fixing.
	if h.m.statusKind != statusWarning {
		t.Fatalf("status kind %v after a valid edit, want the warning back: %q", h.m.statusKind, h.m.status)
	}
	if h.m.status != "saved, but the password could not be stored" {
		t.Fatalf("status = %q, want the earlier warning restored", h.m.status)
	}
}

// Clearing the filter restores the status too, so esc is not a way to lose a
// warning.
func TestFilter_EscRestoresTheEarlierStatus(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "host.invalid", "u"), secret.NewMemory())
	h.m.setStatus("warning worth keeping", statusWarning)
	h.m = press(h.m, "/")
	nm, _ := h.m.Update(tea.PasteMsg{Content: "a\nb"})
	h.m = nm.(Model)
	h.m = press(h.m, "esc")
	if h.m.status != "warning worth keeping" || h.m.statusKind != statusWarning {
		t.Fatalf("status = %q kind=%v", h.m.status, h.m.statusKind)
	}
}
