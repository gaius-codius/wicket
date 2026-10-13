package tui

import (
	"os"
	"os/exec"
	"path/filepath"
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

// clientHarness is a harness whose PATH holds only installed, every one of
// them FreeRDP 3 when asked.
func clientHarness(t *testing.T, cfg string, installed ...string) *harness {
	t.Helper()
	h := newHarness(t, cfg, secret.NewMemory())
	h.m.app.LookPath = onPath(installed...)
	h.m.app.FreeRDPMajor = func(string) (int, bool) { return 3, true }
	return h
}

// freerdp2Harness is a clientHarness whose unsuffixed xfreerdp is FreeRDP 2.
// runs counts the version probes.
func freerdp2Harness(t *testing.T, cfg string, runs *int, installed ...string) *harness {
	t.Helper()
	h := clientHarness(t, cfg, installed...)
	h.m.app.FreeRDPMajor = func(path string) (int, bool) {
		*runs++
		if path == "/fake/bin/"+rdp.ClientX11Homebrew {
			return 2, true
		}
		return 3, true
	}
	return h
}

// clientRow is the client row of m's form as drawn, colour removed.
func clientRow(t *testing.T, m Model) string {
	t.Helper()
	for ln := range strings.SplitSeq(screen(m), "\n") {
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
	if row := clientRow(t, m); !strings.Contains(row, "● sdl-freerdp3") || !strings.Contains(row, "○ xfreerdp3") ||
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
	if row := clientRow(t, m); !strings.Contains(row, "● xfreerdp3") {
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
	if row := clientRow(t, m); !strings.Contains(row, "hl-rdp") || strings.Contains(row, "●") || strings.Contains(row, "○") {
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
	if !strings.Contains(out, "No FreeRDP 3 client found") {
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
	if row := clientRow(t, m); !strings.Contains(row, "no FreeRDP 3 client found") {
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
		{[]string{rdp.ClientSDLHomebrew, rdp.ClientX11Homebrew}, rdp.ClientSDLHomebrew},
		{[]string{rdp.ClientX11Homebrew}, rdp.ClientX11Homebrew},
		{[]string{rdp.ClientX11, rdp.ClientSDLHomebrew}, rdp.ClientSDLHomebrew},
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

// An unsuffixed xfreerdp that is FreeRDP 2 is neither offered nor picked
// for a new profile, and the form asks it once however often it opens.
func TestNewProfile_SkipsFreeRDP2(t *testing.T) {
	for _, tc := range []struct {
		installed []string
		choices   []string
	}{
		{[]string{rdp.ClientX11Homebrew}, nil},
		{[]string{rdp.ClientSDLHomebrew, rdp.ClientX11Homebrew}, []string{rdp.ClientSDLHomebrew}},
		{[]string{rdp.ClientX11, rdp.ClientX11Homebrew}, []string{rdp.ClientX11}},
	} {
		runs := 0
		h := freerdp2Harness(t, "", &runs, tc.installed...)
		m := press(h.m, "n")
		if !slices.Equal(m.form.clients[:m.form.detected], tc.choices) {
			t.Fatalf("%v: choices %q, want %q", tc.installed, m.form.clients, tc.choices)
		}
		want := config.DefaultClient
		if len(tc.choices) > 0 {
			want = tc.choices[0]
		}
		if m.form.p.Client != want {
			t.Fatalf("%v: new profile client %q, want %q", tc.installed, m.form.p.Client, want)
		}
		before := runs
		m = press(m, "esc")
		m = press(m, "n")
		if runs != before || m.form.p.Client != want {
			t.Fatalf("%v: second form ran %d more probes, client %q", tc.installed, runs-before, m.form.p.Client)
		}
	}
}

// A probe that fails, a client that hangs say, is remembered like any other
// answer: discovery runs in Update, so asking again on every form would
// freeze the TUI every time.
func TestInstalledClients_RemembersAFailedProbe(t *testing.T) {
	h := clientHarness(t, "", rdp.ClientX11Homebrew)
	runs := 0
	h.m.app.FreeRDPMajor = func(string) (int, bool) { runs++; return 0, false }
	for range 3 {
		if got := h.m.app.InstalledClients(); len(got) != 0 {
			t.Fatalf("failed probe offered %v", got)
		}
	}
	if runs != 1 {
		t.Fatalf("failed probe ran %d times", runs)
	}
}

// A client replaced in place is asked again: what it said before was about
// another build.
func TestInstalledClients_ReprobesAReplacedBinary(t *testing.T) {
	bin := filepath.Join(t.TempDir(), rdp.ClientX11Homebrew)
	if err := os.WriteFile(bin, []byte("3"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := clientHarness(t, "")
	h.m.app.LookPath = func(file string) (string, error) {
		if file == rdp.ClientX11Homebrew {
			return bin, nil
		}
		return "", exec.ErrNotFound
	}
	runs := 0
	h.m.app.FreeRDPMajor = func(path string) (int, bool) {
		runs++
		b, _ := os.ReadFile(path)
		return int(b[0] - '0'), true
	}
	if got := h.m.app.InstalledClients(); len(got) != 1 {
		t.Fatalf("version 3 not offered: %v", got)
	}
	h.m.app.InstalledClients()
	if runs != 1 {
		t.Fatalf("unchanged binary probed %d times", runs)
	}
	if err := os.WriteFile(bin, []byte("2 "), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := h.m.app.InstalledClients(); len(got) != 0 || runs != 2 {
		t.Fatalf("after the swap to version 2: offered %v after %d probes", got, runs)
	}
}

// A profile that already names a FreeRDP 2 xfreerdp keeps it: the form shows
// it as it is, not as missing, and saving does not change it.
func TestFormClient_KeepsAFreeRDP2Profile(t *testing.T) {
	runs := 0
	h := freerdp2Harness(t, clientTOML(rdp.ClientX11Homebrew), &runs, rdp.ClientSDL, rdp.ClientX11Homebrew)
	m := focusField(t, press(h.m, "e"), fieldClient)
	if m.form.p.Client != rdp.ClientX11Homebrew || m.form.dirty() {
		t.Fatalf("opened with client %q, dirty %v", m.form.p.Client, m.form.dirty())
	}
	if row := clientRow(t, m); !strings.Contains(row, rdp.ClientX11Homebrew) || strings.Contains(row, notFound) {
		t.Fatalf("row %q", row)
	}
	if help := m.form.clientHelp(); !strings.HasPrefix(help, "Not FreeRDP 3") {
		t.Fatalf("help %q", help)
	}
	m = press(m, "ctrl+s")
	if p, _ := m.app.Cfg.Profile("work"); m.view != viewList || p.Client != rdp.ClientX11Homebrew {
		t.Fatalf("view %v, saved client %q", m.view, p.Client)
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
				for ln := range strings.SplitSeq(m.viewForm(lo), "\n") {
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
	if row := clientRow(t, m); !strings.Contains(row, "● xfreerdp3") || !strings.Contains(row, "○ sdl-freerdp3") {
		t.Fatalf("row %q", row)
	}
	// Narrow: only the current choice, still marked.
	nm, _ := m.Update(teaWin(40, 24))
	if row := clientRow(t, nm.(Model)); !strings.Contains(row, "● xfreerdp3") || strings.Contains(row, "sdl") {
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
	return ConnectResult{
		Class: rdp.ClassFailed, Status: "FreeRDP failed: could not start connecting",
		Outcome: rdp.Outcome{Client: client, ExitCode: code, Duration: 300 * time.Millisecond},
	}
}

// The fullscreen hint shows for an SDL client (sdl-freerdp3 or the Homebrew
// sdl-freerdp) exiting 136 with fullscreen on, and nowhere else. It names
// the preferred installed X11 client, or none when only SDL is installed.
func TestRetry_FullscreenHint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		client     string
		code       int
		fullscreen bool
		installed  []string
		signaled   bool
		want       bool
		wantX11    string
	}{
		{"sdl, 136, fullscreen, xfreerdp3 installed", "sdl-freerdp3", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, true, "xfreerdp3"},
		{"by basename", "/usr/bin/sdl-freerdp3", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, true, "xfreerdp3"},
		{"homebrew sdl, xfreerdp installed", "sdl-freerdp", 136, true, []string{rdp.ClientSDLHomebrew, rdp.ClientX11Homebrew}, false, true, "xfreerdp"},
		{"homebrew sdl by path", "/opt/homebrew/bin/sdl-freerdp", 136, true, []string{rdp.ClientX11Homebrew}, false, true, "xfreerdp"},
		{"suffixed x11 preferred over brew", "sdl-freerdp", 136, true, []string{rdp.ClientX11, rdp.ClientX11Homebrew}, false, true, "xfreerdp3"},
		{"xfreerdp3 not installed", "sdl-freerdp3", 136, true, []string{rdp.ClientSDL}, false, true, ""},
		{"fullscreen off", "sdl-freerdp3", 136, false, []string{rdp.ClientSDL, rdp.ClientX11}, false, false, ""},
		{"another exit", "sdl-freerdp3", 131, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, false, ""},
		{"xfreerdp3 itself", "xfreerdp3", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, false, ""},
		{"xfreerdp itself", "xfreerdp", 136, true, []string{rdp.ClientSDLHomebrew, rdp.ClientX11Homebrew}, false, false, ""},
		{"another client", "myrdp", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, false, false, ""},
		{"signal", "sdl-freerdp3", 136, true, []string{rdp.ClientSDL, rdp.ClientX11}, true, false, ""},
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
			if got := strings.Contains(out, "fail fullscreen on a scaled monitor"); got != tc.want {
				t.Fatalf("hint shown %v, want %v:\n%s", got, tc.want, out)
			}
			offered := ""
			for _, name := range []string{rdp.ClientX11, rdp.ClientX11Homebrew} {
				if strings.Contains(out, "Try the "+name+" client") {
					offered = name
				}
			}
			if offered != tc.wantX11 {
				t.Fatalf("x11 offered %q, want %q:\n%s", offered, tc.wantX11, out)
			}
			if tc.want && !strings.Contains(out, "fullscreen off") {
				t.Fatalf("no fullscreen-off advice:\n%s", out)
			}
		})
	}
}

// The fullscreen hint does not suggest an unsuffixed xfreerdp that is
// FreeRDP 2: it falls back to a suffixed X11 client, or to fullscreen off.
func TestRetry_FullscreenHintSkipsFreeRDP2(t *testing.T) {
	for _, tc := range []struct {
		installed []string
		want      string
	}{
		{[]string{rdp.ClientSDLHomebrew, rdp.ClientX11Homebrew}, "Try turning fullscreen off."},
		{[]string{rdp.ClientSDL, rdp.ClientX11, rdp.ClientX11Homebrew}, "Try the xfreerdp3 client, or turn fullscreen off."},
	} {
		runs := 0
		h := freerdp2Harness(t, fixtureTOML("work", "h", "u"), &runs, tc.installed...)
		p, _ := h.m.app.Cfg.Profile("work")
		p.Fullscreen = true
		if action, _ := h.m.app.fullscreenHint(p, failed(rdp.ClientSDLHomebrew, 136).Outcome); action != tc.want {
			t.Fatalf("%v: hint %q, want %q", tc.installed, action, tc.want)
		}
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
			// The way out is what the user acts on, so it outlives the
			// exit status and the sentence explaining it.
			action := strings.Contains(out, "Try the") || strings.Contains(out, "Try turning")
			sawHint = sawHint || action
			if strings.Contains(out, "FreeRDP's SDL client") && !action {
				t.Fatalf("%dx%d kept the reason over the way out:\n%s", w, ht, out)
			}
			if strings.Contains(out, "status 136") && !action {
				t.Fatalf("%dx%d kept the exit status over the way out:\n%s", w, ht, out)
			}
		}
	}
	if !sawHint {
		t.Fatal("the hint never showed")
	}
	// Given the room for the reason, the way out and the exit status, the
	// overlay spends it on them rather than on the client's note.
	lo := newLayout(80, 24)
	msg, fullscreen, detail, _, note, _ := base.retryBlocks(lo)
	if len(fullscreen) == 0 || len(note) == 0 {
		t.Fatal("expected a hint and a note")
	}
	out := stripANSI(strings.Join(base.viewRetry(lo, len(msg)+len(detail)+len(fullscreen)), "\n"))
	if !strings.Contains(out, "Try the") || strings.Contains(out, "client: ") {
		t.Fatalf("the note outranked the hint:\n%s", out)
	}
	// The way out is drawn as a key hint, not as another muted report line:
	// its own marker, and the accent the footer keys use.
	full := base.viewRetry(lo, 12)
	var marked string
	for _, ln := range full {
		if strings.Contains(stripANSI(ln), "Try the") {
			marked = ln
		}
	}
	if !strings.HasPrefix(stripANSI(marked), "→ ") {
		t.Fatalf("the way out is not marked: %q", stripANSI(marked))
	}
	if !strings.Contains(marked, base.styles.accent.Render("→ ")) {
		t.Fatalf("the marker is not in the accent: %q", marked)
	}
}

// The way out is what the user acts on, so a shorter block behind it never
// takes its line. The overlay used to fill the last line with whichever
// block fitted, which let a one-line "client:" note in ahead of a hint that
// wrapped to two.
func TestRetry_ShorterBlocksNeverDisplaceTheWayOut(t *testing.T) {
	h := clientHarness(t, fixtureTOML("work", "p777-svr01", "777admin"), rdp.ClientSDL, rdp.ClientX11)
	nm, _ := h.m.Update(teaWin(44, 30))
	m := nm.(Model)
	p, _ := m.app.Cfg.Profile("work")
	p.Fullscreen = true
	cr := failed(rdp.ClientSDL, 136)
	cr.Output = []byte("[ERROR][com.freerdp.core] - ERRCONNECT_PRE_CONNECT_FAILED")
	nm, _ = m.applyConnect(p, secret.Password{}, false, "", cr)
	m = nm.(Model)
	lo := newLayout(44, 30)
	_, fullscreen, _, _, note, _ := m.retryBlocks(lo)
	if len(fullscreen) < 2 || len(note) != 1 {
		t.Fatalf("want a wrapped hint and a one-line note, got %d and %d", len(fullscreen), len(note))
	}
	for room := 1; room <= 12; room++ {
		out := stripANSI(strings.Join(m.viewRetry(lo, room), "\n"))
		if strings.Contains(out, "client:") && !strings.Contains(out, "Try the") {
			t.Fatalf("room %d: the client note displaced the way out:\n%s", room, out)
		}
	}
}

// The explanation gives way to the client's own line, which says something
// the user cannot see anywhere else, but it is still drawn above it.
func TestRetry_ExplanationRanksBelowTheClientLineButReadsAbove(t *testing.T) {
	h := clientHarness(t, fixtureTOML("work", "p777-svr01", "777admin"), rdp.ClientSDL, rdp.ClientX11)
	nm, _ := h.m.Update(teaWin(100, 30))
	m := nm.(Model)
	p, _ := m.app.Cfg.Profile("work")
	p.Fullscreen = true
	cr := failed(rdp.ClientSDL, 136)
	cr.Output = []byte("[ERROR][com.freerdp.core] - ERRCONNECT_PRE_CONNECT_FAILED")
	nm, _ = m.applyConnect(p, secret.Password{}, false, "", cr)
	m = nm.(Model)
	lo := newLayout(100, 30)
	msg, fullscreen, detail, why, note, _ := m.retryBlocks(lo)
	room := len(msg) + len(fullscreen) + len(detail) + len(note)
	out := stripANSI(strings.Join(m.viewRetry(lo, room), "\n"))
	if !strings.Contains(out, "client:") {
		t.Fatalf("the explanation outranked the client line:\n%s", out)
	}
	full := stripANSI(strings.Join(m.viewRetry(lo, room+len(why)), "\n"))
	if i, j := strings.Index(full, "can fail fullscreen"), strings.Index(full, "client:"); i < 0 || j < 0 || i > j {
		t.Fatalf("the explanation is not drawn above the client line:\n%s", full)
	}
}
