package tui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
)

// A save or delete with nothing to report used to leave the status line
// empty, so the only sign it had worked was the list changing underneath.
func TestStatus_SaveAndDeleteReportSuccess(t *testing.T) {
	h := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory())
	h.m = press(h.m, "e", "ctrl+s")
	if h.m.statusKind != statusSuccess || !strings.Contains(screen(h.m), "✓ Saved work.") {
		t.Fatalf("save: kind %v status %q\n%s", h.m.statusKind, h.m.status, screen(h.m))
	}
	h.m = press(h.m, "D", "y")
	if h.m.statusKind != statusSuccess || !strings.Contains(screen(h.m), "✓ Deleted work.") {
		t.Fatalf("delete: kind %v status %q\n%s", h.m.statusKind, h.m.status, screen(h.m))
	}
}

// A warning is part of the request that failed, so a save that raised one
// must never be reported with the success marker, however it is worded.
func TestStatus_PartialSaveIsNotASuccess(t *testing.T) {
	msg, kind := outcome("Saved", "work", []string{"could not save password: boom"})
	if kind == statusSuccess || kind == statusInfo {
		t.Fatalf("kind %v for a save with a warning", kind)
	}
	if !strings.Contains(msg, "Saved work") || !strings.Contains(msg, "could not save password") {
		t.Fatalf("status %q hides what happened or what did not", msg)
	}
}

// A long name is cut, so it cannot wrap the status line on its own.
func TestStatus_LongNameIsTruncated(t *testing.T) {
	long := strings.Repeat("n", 100)
	msg, _ := outcome("Saved", long, nil)
	if strings.Contains(msg, long) || !strings.Contains(msg, "…") {
		t.Fatalf("status %q quotes the whole name", msg)
	}
}

func TestStatus_MarkerPerKind(t *testing.T) {
	m := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory()).m
	for kind, want := range map[statusKind]string{
		statusInfo: "• note", statusSuccess: "✓ note", statusWarning: "▲ note", statusError: "✗ note",
	} {
		m.setStatus("note", kind)
		if out := screen(m); !strings.Contains(out, want) {
			t.Errorf("kind %v: want %q:\n%s", kind, want, out)
		}
	}
}

func intentOf(t *testing.T, hs []keyHint, key string) intent {
	t.Helper()
	for _, h := range hs {
		if h.key == key {
			return h.intent
		}
	}
	t.Fatalf("no %q in footer %v", key, hs)
	return intentNormal
}

// y deletes a profile in one dialog and throws away typing in another. Only
// the first may look dangerous, which is why the intent is set where the key
// is offered rather than read off the key.
func TestHints_DangerIsForDeleteOnly(t *testing.T) {
	t.Setenv("WICKET_THEME", "wicket-dark")
	cfg := fixtureTOML("work", "h", "u")

	del := newHarness(t, cfg, secret.NewMemory()).m
	del = press(del, "D")
	if _, hs := del.chrome(); intentOf(t, hs, "y") != intentDanger {
		t.Fatal("delete y is not a danger key")
	}
	dangerY := del.styles.danger.Bold(true).Render("y")
	if !strings.Contains(del.View().Content, dangerY) {
		t.Fatalf("delete footer does not draw y in the danger colour:\n%q", del.View().Content)
	}

	disc := newHarness(t, cfg, secret.NewMemory()).m
	disc = press(disc, "e", "x", "esc")
	if !disc.form.confirmDiscard {
		t.Fatal("want the discard question")
	}
	if _, hs := disc.chrome(); intentOf(t, hs, "y") == intentDanger {
		t.Fatal("discard y is drawn as a danger")
	}
	if strings.Contains(disc.View().Content, dangerY) {
		t.Fatalf("discard footer draws y in the danger colour:\n%q", disc.View().Content)
	}
}

func TestHints_PrimaryKeys(t *testing.T) {
	cfg := fixtureTOML("work", "h", "u")
	check := func(name string, m Model, key string) {
		t.Helper()
		if _, hs := m.chrome(); intentOf(t, hs, key) != intentPrimary {
			t.Errorf("%s: %q is not the primary key", name, key)
		}
	}
	m := newHarness(t, cfg, secret.NewMemory()).m
	check("list", m, "enter")
	check("filter", press(m, "/"), "enter")
	check("form", press(m, "e"), "ctrl+s")
	check("modal", press(m, "enter"), "enter")
	check("empty", newHarness(t, "", secret.NewMemory()).m, "n")
	p, _ := m.app.Cfg.Profile("work")
	m.view, m.retry = viewRetry, retryState{profile: p, status: "session ended quickly", class: rdp.ClassShortSession}
	check("retry", m, "enter")

	// Nothing else in the list footer competes with the primary key.
	_, hs := newHarness(t, cfg, secret.NewMemory()).m.chrome()
	for _, h := range hs {
		if h.key != "enter" && h.intent != intentNormal {
			t.Errorf("list %q has intent %v", h.key, h.intent)
		}
	}
}

func TestHeader_Brand(t *testing.T) {
	t.Setenv("WICKET_THEME", "wicket-dark")
	m := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory()).m
	if raw := m.View().Content; !strings.Contains(raw, m.styles.brand.Render(brandMark)) {
		t.Fatalf("header does not draw the brand mark in the brand style:\n%q", raw)
	}
	del := press(m, "D")
	if raw := del.View().Content; !strings.Contains(raw, del.styles.danger.Render("delete")) {
		t.Fatalf("delete context is not in the danger colour:\n%q", raw)
	}
}

// The question has to survive a one-line body, and the note, which only
// explains, is the first thing to go.
func TestDelete_ShedsTheNoteBeforeTheHost(t *testing.T) {
	m := newHarness(t, fixtureTOML("work", "host.invalid", "u"), secret.NewMemory()).m
	m = press(m, "D")
	lo := m.panelLayout()
	for budget := 1; budget <= 8; budget++ {
		lo.Budget = budget
		out := stripANSI(m.viewDelete(lo))
		lines := strings.Split(out, "\n")
		if len(lines) > budget {
			t.Fatalf("budget %d: %d lines:\n%s", budget, len(lines), out)
		}
		if lines[0] != "▲ Delete work?" {
			t.Fatalf("budget %d: first line %q", budget, lines[0])
		}
		hasHost := strings.Contains(out, "host.invalid")
		hasNote := strings.Contains(out, "cannot be undone")
		if budget >= 2 && !hasHost {
			t.Errorf("budget %d: host shed before the note:\n%s", budget, out)
		}
		if hasNote && !hasHost {
			t.Errorf("budget %d: note kept over the host:\n%s", budget, out)
		}
	}
}

// Each way into the password dialog says only what Wicket knows.
func TestModal_SubtitlePerState(t *testing.T) {
	_ = withFakeRDP(t)
	cfg := fixtureTOML("work", "h", "u")

	missing := press(newHarness(t, cfg, secret.NewMemory()).m, "enter")
	if out := screen(missing); !strings.Contains(out, "Connect to work") || !strings.Contains(out, "No password is saved") {
		t.Fatalf("missing:\n%s", out)
	}

	locked := press(newHarness(t, cfg, &wrapStore{inner: secret.NewMemory(), lookupErr: secret.ErrUnavailable}).m, "enter")
	out := screen(locked)
	if !strings.Contains(out, "keyring is unavailable") || strings.Contains(out, "No password is saved") {
		t.Fatalf("unavailable must not claim there is no password:\n%s", out)
	}

	p, _ := missing.app.Cfg.Profile("work")
	nm, _ := missing.openModal(p, nil, true)
	out = screen(nm.(Model))
	if !strings.Contains(out, "Type a new password") || strings.Contains(out, "No password is saved") {
		t.Fatalf("replacing:\n%s", out)
	}
}

// The title and subtitle give way to the field; the title outlasts the
// subtitle, and the subtitle is never cut to a stub.
func TestModal_TitleOutlastsSubtitle(t *testing.T) {
	_ = withFakeRDP(t)
	m := press(newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory()).m, "enter")
	lo := m.panelLayout()
	for budget := 1; budget <= 6; budget++ {
		lo.Budget = budget
		out := stripANSI(m.viewModal(lo))
		if n := len(strings.Split(out, "\n")); n > budget {
			t.Fatalf("budget %d: %d lines:\n%s", budget, n, out)
		}
		if !strings.Contains(out, "password") {
			t.Fatalf("budget %d: no field:\n%s", budget, out)
		}
		if strings.Contains(out, "No password is saved") && !strings.Contains(out, "Connect to work") {
			t.Fatalf("budget %d: subtitle kept over the title:\n%s", budget, out)
		}
		if budget >= 2 && !strings.Contains(out, "Connect to work") {
			t.Fatalf("budget %d: title shed with room for it:\n%s", budget, out)
		}
	}
}

// The empty state keeps its lines in rank order: a short panel keeps the
// heading and the key that gets the user started, not the explanation.
func TestEmpty_KeepsTheKeyAheadOfTheDetails(t *testing.T) {
	t.Setenv("WICKET_THEME", "wicket-dark")
	m := newHarness(t, "", secret.NewMemory()).m
	lo := m.panelLayout()
	ranked := []string{
		"No saved connections yet.",
		"add your first connection",
		"Save a FreeRDP profile once",
		"config",
		"wicket · dark",
		"wicket connect <profile>",
	}
	for budget := 1; budget <= 12; budget++ {
		lo.Budget = budget
		out := stripANSI(m.viewEmpty(lo))
		if n := len(strings.Split(out, "\n")); n > budget {
			t.Fatalf("budget %d: %d lines:\n%s", budget, n, out)
		}
		if strings.Contains(out, "…\n") || strings.HasSuffix(out, "…") {
			t.Fatalf("budget %d: clipped rather than shed:\n%s", budget, out)
		}
		for i := 1; i < len(ranked); i++ {
			if strings.Contains(out, ranked[i]) && !strings.Contains(out, ranked[i-1]) {
				t.Fatalf("budget %d: %q kept over %q:\n%s", budget, ranked[i], ranked[i-1], out)
			}
		}
	}
	lo.Budget = 2
	if out := stripANSI(m.viewEmpty(lo)); !strings.Contains(out, "n  add your first connection") {
		t.Fatalf("budget 2 lost the key:\n%s", out)
	}
	lo.Budget = 20
	if out := stripANSI(m.viewEmpty(lo)); !strings.Contains(out, ranked[len(ranked)-1]) {
		t.Fatalf("a tall panel should show everything:\n%s", out)
	}
}

func TestEmpty_ThemeLine(t *testing.T) {
	for _, tc := range []struct {
		env  string
		bg   color.Color
		want string
		not  string
	}{
		{env: "wicket-dark", want: "wicket · dark", not: "from your terminal"},
		{env: "wicket-light", want: "wicket · light", not: "from your terminal"},
		{env: "terminal", want: "terminal colours"},
		{env: "wicket", bg: color.Black, want: "wicket · dark, from your terminal"},
		{env: "wicket", bg: color.White, want: "wicket · light, from your terminal"},
	} {
		m := themeModel(t, tc.env, "", true)
		if tc.bg != nil {
			nm, _ := m.Update(tea.BackgroundColorMsg{Color: tc.bg})
			m = nm.(Model)
		}
		out := screen(m)
		if !strings.Contains(out, tc.want) || (tc.not != "" && strings.Contains(out, tc.not)) {
			t.Errorf("%s: want %q:\n%s", tc.env, tc.want, out)
		}
	}
}

func TestEmpty_PathIsSanitisedAndKeepsItsEnd(t *testing.T) {
	if got := sanitize("/tmp/a\x1b[31mb\n"); got != "/tmp/a[31mb" {
		t.Fatalf("sanitize = %q", got)
	}
	if got := truncateLeft("/very/long/path/config.toml", 12); got != "…config.toml" {
		t.Fatalf("truncateLeft = %q", got)
	}
}

// How the client exited is a fact the user cannot see anywhere else, so the
// overlay says it, without claiming the password was wrong.
func TestRetry_ShowsHowTheClientExited(t *testing.T) {
	m := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory()).m
	p, _ := m.app.Cfg.Profile("work")
	m.view = viewRetry
	m.retry = retryState{profile: p, status: "session ended quickly", class: rdp.ClassShortSession,
		outcome: rdp.Outcome{ExitCode: 131, Duration: 2300 * time.Millisecond}}
	out := screen(m)
	for _, want := range []string{"▲ session ended quickly", "exited with status 131 after 2.3s", "If the password may be wrong"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q:\n%s", want, out)
		}
	}
	m.retry.outcome.Signaled = true
	if out := screen(m); !strings.Contains(out, "stopped by signal 3") {
		t.Fatalf("signal:\n%s", out)
	}
}

// The overlay's message is its point: every height that is not tiny shows it.
func TestRetry_MessageSurvivesShortTerminals(t *testing.T) {
	m := newHarness(t, fixtureTOML("work", "h", "u"), secret.NewMemory()).m
	p, _ := m.app.Cfg.Profile("work")
	m.view = viewRetry
	m.retry = retryState{profile: p, status: "session ended quickly", class: rdp.ClassShortSession}
	for h := heightTiny; h <= 14; h++ {
		nm, _ := m.Update(teaWin(80, h))
		out := stripANSI(nm.(Model).render())
		if !strings.Contains(out, "session ended quickly") {
			t.Fatalf("height %d lost the message:\n%s", h, out)
		}
		if _, gotH := measure(out); gotH > h {
			t.Fatalf("height %d rendered %d lines", h, gotH)
		}
	}
}
