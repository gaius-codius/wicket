package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
)

// clientTOML is one profile whose client is client.
func clientTOML(client string) string {
	return strings.Replace(fixtureTOML("work", "h", "u"), `client = "sdl-freerdp3"`, `client = "`+client+`"`, 1)
}

// clientHarness is a harness whose PATH holds only installed.
func clientHarness(t *testing.T, cfg string, installed ...string) *harness {
	t.Helper()
	h := newHarness(t, cfg, secret.NewMemory())
	h.m.app.LookPath = onPath(installed...)
	return h
}

// clientRow is the client row of m's form as drawn, colour removed.
func clientRow(t *testing.T, m Model) string {
	t.Helper()
	for _, ln := range strings.Split(screen(m), "\n") {
		if strings.Contains(ln, "client:") {
			return ln
		}
	}
	t.Fatalf("no client row:\n%s", screen(m))
	return ""
}

// The row cycles through the known clients PATH has, in order of
// preference, then "custom…"; wlfreerdp3 is not offered even when installed.
func TestFormClient_CyclesInstalledClientsOnly(t *testing.T) {
	h := clientHarness(t, "", rdp.ClientSDL, rdp.ClientX11, "wlfreerdp3")
	m := focusField(t, press(h.m, "n"), fieldClient)
	if !slices.Equal(m.form.clients, []string{rdp.ClientSDL, rdp.ClientX11}) {
		t.Fatalf("choices %q", m.form.clients)
	}
	if m.form.textFocused() {
		t.Fatal("a picked client should not be a text field")
	}
	if row := clientRow(t, m); !strings.Contains(row, "‹sdl-freerdp3›") || !strings.Contains(row, " xfreerdp3 ") ||
		!strings.Contains(row, customChoice) || strings.Contains(row, "wlfreerdp3") {
		t.Fatalf("row %q", row)
	}
	// h/l and the arrows step alike, and wrap.
	for _, step := range []struct{ key, want string }{
		{"right", rdp.ClientX11}, {"h", rdp.ClientSDL}, {"left", ""}, {"left", rdp.ClientX11}, {"h", rdp.ClientSDL}, {"l", rdp.ClientX11},
	} {
		if m.form.clientCustom() {
			m.form.inputs[fieldClient].CursorStart()
		}
		m = press(m, step.key)
		if m.form.p.Client != step.want {
			t.Fatalf("after %s: client %q, want %q", step.key, m.form.p.Client, step.want)
		}
	}
	if row := clientRow(t, m); !strings.Contains(row, "‹xfreerdp3›") {
		t.Fatalf("current choice not marked: %q", row)
	}
	// Only xfreerdp3 installed: it is the only client offered.
	h = clientHarness(t, "", rdp.ClientX11)
	m = press(h.m, "n")
	if !slices.Equal(m.form.clients, []string{rdp.ClientX11}) {
		t.Fatalf("choices %q", m.form.clients)
	}
}

// "custom…" makes the row a text input, which takes any name, and ← from its
// start goes back to the list with the text kept for next time.
func TestFormClient_CustomSwitchesToTextInput(t *testing.T) {
	h := clientHarness(t, "", rdp.ClientSDL, rdp.ClientX11)
	m := press(h.m, "n")
	m = typeInto(m, "work")
	m = press(m, "tab")
	m = typeInto(m, "host1")
	m = press(m, "tab")
	m = typeInto(m, "user1")
	m = focusField(t, m, fieldClient)
	m = press(m, "right", "right")
	if !m.form.clientCustom() || !m.form.textFocused() || !m.form.inputs[fieldClient].Focused() {
		t.Fatal("custom… should be a focused text input")
	}
	if m.form.p.Client != "" {
		t.Fatalf("custom starts empty, got %q", m.form.p.Client)
	}
	// l and h are letters here, not steps.
	m = typeInto(m, "hl-rdp")
	if m.form.p.Client != "hl-rdp" || !m.form.clientCustom() {
		t.Fatalf("typed %q custom=%v", m.form.p.Client, m.form.clientCustom())
	}
	if row := clientRow(t, m); !strings.Contains(row, "hl-rdp") || strings.Contains(row, "‹") {
		t.Fatalf("row %q", row)
	}
	// ← inside the text moves the cursor; only from the start does it leave.
	m = press(m, "left")
	if !m.form.clientCustom() {
		t.Fatal("← mid-text left the input")
	}
	m.form.inputs[fieldClient].CursorStart()
	m = press(m, "left")
	if m.form.clientCustom() || m.form.p.Client != rdp.ClientX11 {
		t.Fatalf("← at the start: custom=%v client %q", m.form.clientCustom(), m.form.p.Client)
	}
	m = press(m, "right")
	if m.form.p.Client != "hl-rdp" {
		t.Fatalf("custom text lost: %q", m.form.p.Client)
	}
	m = press(m, "ctrl+s")
	if m.view != viewList {
		t.Fatalf("view %v err %s", m.view, m.form.err)
	}
	if p, _ := m.app.Cfg.Profile("work"); p.Client != "hl-rdp" {
		t.Fatalf("saved client %q", p.Client)
	}
}

// A configured client that is not an installed known one is kept exactly:
// opening the form and saving it changes nothing, and one PATH does not have
// is marked.
func TestFormClient_UnknownOrMissingClientIsKept(t *testing.T) {
	for _, tc := range []struct {
		name, client string
		installed    []string
		missing      bool
	}{
		{"custom on PATH", "myrdp", []string{rdp.ClientSDL, "myrdp"}, false},
		{"custom missing", "myrdp", []string{rdp.ClientSDL, rdp.ClientX11}, true},
		{"known, not installed", rdp.ClientX11, []string{rdp.ClientSDL}, true},
		{"known, none installed", rdp.ClientX11, nil, true},
		{"custom, none installed", "myrdp", []string{"myrdp"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := clientHarness(t, clientTOML(tc.client), tc.installed...)
			m := focusField(t, press(h.m, "e"), fieldClient)
			if m.form.p.Client != tc.client || m.form.dirty() {
				t.Fatalf("opened with client %q, dirty %v", m.form.p.Client, m.form.dirty())
			}
			row := clientRow(t, m)
			if !strings.Contains(row, tc.client) {
				t.Fatalf("row does not show %q: %q", tc.client, row)
			}
			if got := strings.Contains(row, notFound); got != tc.missing {
				t.Fatalf("not-found marker %v, want %v: %q", got, tc.missing, row)
			}
			if len(tc.installed) > 0 && slices.ContainsFunc(tc.installed, func(c string) bool { _, ok := rdp.AboutClient(c); return ok }) {
				if m.form.clientCustom() {
					t.Fatal("with clients installed the configured one should be a choice")
				}
			}
			m = press(m, "ctrl+s")
			if m.view != viewList {
				t.Fatalf("view %v err %s", m.view, m.form.err)
			}
			if p, _ := m.app.Cfg.Profile("work"); p.Client != tc.client {
				t.Fatalf("saved client %q, want %q", p.Client, tc.client)
			}
		})
	}
}

// With no known client installed the row is the text input, and says so.
func TestFormClient_NoClientInstalled(t *testing.T) {
	h := clientHarness(t, "")
	nm, _ := h.m.Update(teaWin(100, 30))
	m := focusField(t, press(nm.(Model), "n"), fieldClient)
	if !m.form.clientCustom() || !m.form.textFocused() {
		t.Fatal("no client installed: the row should be a text input")
	}
	if m.form.p.Client != config.DefaultClient {
		t.Fatalf("client %q, want the fallback %q", m.form.p.Client, config.DefaultClient)
	}
	out := screen(m)
	if !strings.Contains(out, "No FreeRDP client found") {
		t.Fatalf("no hint:\n%s", out)
	}
	if row := clientRow(t, m); !strings.Contains(row, config.DefaultClient) || !strings.Contains(row, notFound) {
		t.Fatalf("row %q", row)
	}
	// ← at the start has no list to go back to.
	m.form.inputs[fieldClient].CursorStart()
	if m = press(m, "left"); !m.form.clientCustom() {
		t.Fatal("left the only choice")
	}
	m.form.inputs[fieldClient].SetValue("")
	m.form.p.Client = ""
	if row := clientRow(t, m); !strings.Contains(row, "no FreeRDP client found") {
		t.Fatalf("empty row %q", row)
	}
}

// A new profile starts with the first known client installed.
func TestNewProfile_DefaultsToAnInstalledClient(t *testing.T) {
	for _, tc := range []struct {
		installed []string
		want      string
	}{
		{[]string{rdp.ClientSDL, rdp.ClientX11}, rdp.ClientSDL},
		{[]string{rdp.ClientSDL}, rdp.ClientSDL},
		{[]string{rdp.ClientX11}, rdp.ClientX11},
		{nil, config.DefaultClient},
	} {
		h := clientHarness(t, "", tc.installed...)
		m := press(h.m, "n")
		if m.form.dirty() {
			t.Fatalf("%v: a fresh form is dirty", tc.installed)
		}
		m = typeInto(m, "work")
		m = press(m, "tab")
		m = typeInto(m, "host1")
		m = press(m, "tab")
		m = typeInto(m, "user1")
		m = press(m, "ctrl+s")
		if m.view != viewList {
			t.Fatalf("%v: view %v err %s", tc.installed, m.view, m.form.err)
		}
		if p, _ := m.app.Cfg.Profile("work"); p.Client != tc.want {
			t.Fatalf("%v: client %q, want %q", tc.installed, p.Client, tc.want)
		}
	}
}

// The client row's every state fits its row at every width and height.
func TestFormClient_RowFitsEverywhere(t *testing.T) {
	for _, tc := range []struct {
		cfg       string
		installed []string
		custom    bool
	}{
		{clientTOML("sdl-freerdp3"), []string{rdp.ClientSDL, rdp.ClientX11}, false},
		{clientTOML("a-long-custom-client-name"), []string{rdp.ClientSDL, rdp.ClientX11}, false},
		{clientTOML("a-long-custom-client-name"), []string{rdp.ClientSDL, rdp.ClientX11}, true},
		{clientTOML("xfreerdp3"), nil, false},
	} {
		h := clientHarness(t, tc.cfg, tc.installed...)
		opened := focusField(t, press(h.m, "e"), fieldClient)
		if tc.custom {
			opened = press(opened, "right")
			if !opened.form.clientCustom() {
				t.Fatal("not custom")
			}
		}
		widths := []int{130}
		for w := widthTiny; w <= panelNormal; w++ {
			widths = append(widths, w)
		}
		for _, w := range widths {
			for _, ht := range []int{heightTiny, 10, 24} {
				nm, _ := opened.Update(teaWin(w, ht))
				m := nm.(Model)
				if gotW, gotH := measure(m.render()); gotW > w || gotH > ht {
					t.Fatalf("%dx%d rendered %dx%d:\n%s", w, ht, gotW, gotH, stripANSI(m.render()))
				}
				lo := m.panelLayout()
				m.fitChrome(&lo)
				for _, ln := range strings.Split(m.viewForm(lo), "\n") {
					if lipgloss.Width(ln) > lo.Inner {
						t.Fatalf("%dx%d: %d cells in a %d-cell panel:\n%s", w, ht, lipgloss.Width(ln), lo.Inner, stripANSI(m.render()))
					}
				}
			}
		}
	}
}

// Without colour the current choice is still marked.
func TestFormClient_ChoiceReadsWithoutColour(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	h := clientHarness(t, clientTOML("xfreerdp3"), rdp.ClientSDL, rdp.ClientX11)
	m := focusField(t, press(h.m, "e"), fieldClient)
	if row := clientRow(t, m); !strings.Contains(row, "‹xfreerdp3›") || !strings.Contains(row, " sdl-freerdp3 ") {
		t.Fatalf("row %q", row)
	}
	// Narrow: only the current choice, still marked.
	nm, _ := m.Update(teaWin(40, 24))
	if row := clientRow(t, nm.(Model)); !strings.Contains(row, "‹ xfreerdp3 ›") || strings.Contains(row, "sdl") {
		t.Fatalf("narrow row %q", row)
	}
}

// The focused row's help says what the current choice is.
func TestFormClient_HelpDescribesTheChoice(t *testing.T) {
	h := clientHarness(t, "", rdp.ClientSDL, rdp.ClientX11)
	nm, _ := h.m.Update(teaWin(130, 30))
	m := focusField(t, press(nm.(Model), "n"), fieldClient)
	for _, want := range []string{"native Wayland and X11", "runs through XWayland", "FreeRDP-compatible binary"} {
		if out := screen(m); !strings.Contains(out, want) {
			t.Fatalf("want %q:\n%s", want, out)
		}
		m = press(m, "right")
	}
}

// failed ends a session with code, as sdl-freerdp3 does with 136 when it
// misreads a scaled monitor under /f.
func failed(client string, code int) ConnectResult {
	return ConnectResult{Class: rdp.ClassFailed, Status: "FreeRDP failed: could not start connecting",
		Outcome: rdp.Outcome{Client: client, ExitCode: code, Duration: 300 * time.Millisecond}}
}

// The fullscreen hint shows for sdl-freerdp3 exiting 136 with fullscreen on,
// and nowhere else; it names xfreerdp3 only when that is installed.
func TestRetry_FullscreenHint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		client     string
		code       int
		fullscreen bool
		installed  []string
		signaled   bool
		want       bool
		wantX11    bool
	}{
		{"sdl, 136, fullscreen, xfreerdp3 installed", "sdl-freerdp3", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, true, true},
		{"by basename", "/usr/bin/sdl-freerdp3", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, true, true},
		{"xfreerdp3 not installed", "sdl-freerdp3", 136, true, []string{rdp.ClientSDL}, false, true, false},
		{"fullscreen off", "sdl-freerdp3", 136, false, []string{rdp.ClientSDL, rdp.ClientX11}, false, false, false},
		{"another exit", "sdl-freerdp3", 131, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, false, false},
		{"xfreerdp3 itself", "xfreerdp3", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, false, false},
		{"another client", "myrdp", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, false, false},
		{"signal", "sdl-freerdp3", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := clientHarness(t, fixtureTOML("work", "h", "u"), tc.installed...)
			nm, _ := h.m.Update(teaWin(130, 30))
			m := nm.(Model)
			p, _ := m.app.Cfg.Profile("work")
			p.Fullscreen = tc.fullscreen
			cr := failed(tc.client, tc.code)
			cr.Outcome.Signaled = tc.signaled
			nm, _ = m.applyConnect(p, secret.Password{}, false, "", cr)
			m = nm.(Model)
			if m.view != viewRetry {
				t.Fatalf("view %v", m.view)
			}
			out := screen(m)
			if got := strings.Contains(out, "fail fullscreen on scaled monitors"); got != tc.want {
				t.Fatalf("hint shown %v, want %v:\n%s", got, tc.want, out)
			}
			if got := strings.Contains(out, "try the xfreerdp3 client"); got != tc.wantX11 {
				t.Fatalf("xfreerdp3 offered %v, want %v:\n%s", got, tc.wantX11, out)
			}
			if tc.want && !strings.Contains(out, "fullscreen off") {
				t.Fatalf("no fullscreen-off advice:\n%s", out)
			}
		})
	}
}

// The hint gives way to the failure itself and to how the client exited, and
// never pushes the overlay past its room.
func TestRetry_FullscreenHintShedsAfterTheReason(t *testing.T) {
	h := clientHarness(t, fixtureTOML("work", "h", "u"), rdp.ClientSDL, rdp.ClientX11)
	p, _ := h.m.app.Cfg.Profile("work")
	p.Fullscreen = true
	cr := failed("sdl-freerdp3", 136)
	cr.Output = []byte("[ERROR] ERRCONNECT_PRE_CONNECT_FAILED\n")
	nm, _ := h.m.applyConnect(p, secret.Password{}, false, "", cr)
	base := nm.(Model)
	sawHint := false
	for _, w := range []int{40, 80} {
		for ht := heightTiny; ht <= 20; ht++ {
			nm, _ := base.Update(teaWin(w, ht))
			out := stripANSI(nm.(Model).render())
			if gotW, gotH := measure(out); gotW > w || gotH > ht {
				t.Fatalf("%dx%d rendered %dx%d", w, ht, gotW, gotH)
			}
			if !strings.Contains(out, "▲ FreeRDP failed") {
				t.Fatalf("%dx%d lost the reason:\n%s", w, ht, out)
			}
			hint := strings.Contains(out, "FreeRDP's SDL client")
			sawHint = sawHint || hint
			if hint && !strings.Contains(out, "status 136") {
				t.Fatalf("%dx%d kept the hint over the exit status:\n%s", w, ht, out)
			}
		}
	}
	if !sawHint {
		t.Fatal("the hint never showed")
	}
	// Given the room for the reason, the exit status and the hint, the
	// overlay spends it on them rather than on the client's note.
	lo := newLayout(80, 24)
	msg, detail, fullscreen, note, _ := base.retryBlocks(lo)
	if len(fullscreen) == 0 || len(note) == 0 {
		t.Fatal("expected a hint and a note")
	}
	out := stripANSI(strings.Join(base.viewRetry(lo, len(msg)+len(detail)+len(fullscreen)), "\n"))
	if !strings.Contains(out, "FreeRDP's SDL client") || strings.Contains(out, "client: ") {
		t.Fatalf("the note outranked the hint:\n%s", out)
	}
}
