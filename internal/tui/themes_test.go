package tui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/theme"
)

// themeModel builds a model with the given WICKET_THEME, config body and
// terminal. home has no Omarchy theme unless the test writes one.
func themeModel(t *testing.T, env, body string, tty bool) Model {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	if body != "" {
		if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return New(Options{
		Home:             home,
		ConfigPath:       cfg,
		StatePath:        filepath.Join(home, "state.toml"),
		Store:            secret.NewMemory(),
		Getenv:           func(k string) string { return map[string]string{envTheme: env}[k] },
		StdoutIsTerminal: func() bool { return tty },
	})
}

func asksForBackground(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	return reflect.TypeOf(cmd()) == reflect.TypeOf(tea.RequestBackgroundColor())
}

// rgbFG is the SGR fragment for an RGB foreground from a #RRGGBB hex.
func rgbFG(hex string) string {
	var r, g, b int
	_, _ = fmt.Sscanf(hex, "#%02X%02X%02X", &r, &g, &b)
	return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
}

func TestTheme_AsksTheTerminalOnlyWhenItNeedsTo(t *testing.T) {
	for _, tc := range []struct {
		env  string
		tty  bool
		want bool
	}{
		{"", true, true}, // auto, no Omarchy: pick light or dark
		{"wicket", true, true},
		{"", false, false}, // no terminal to answer
		{"wicket", false, false},
		{"wicket-dark", true, false},
		{"wicket-light", true, false},
		{"terminal", true, false},
	} {
		m := themeModel(t, tc.env, fixtureTOML("work", "h", "u"), tc.tty)
		if got := asksForBackground(m.Init()); got != tc.want {
			t.Errorf("WICKET_THEME=%q tty=%v: asks for background = %v, want %v", tc.env, tc.tty, got, tc.want)
		}
	}
}

func TestTheme_BackgroundReplyPicksLightOrDark(t *testing.T) {
	m := themeModel(t, "wicket", fixtureTOML("work", "h", "u"), true)
	if m.look.Kind != theme.KindTerminal {
		t.Fatalf("before a reply the look is %d, want terminal", m.look.Kind)
	}
	if strings.Contains(m.View().Content, "38;2;") {
		t.Fatal("RGB colour drawn before the terminal said what its background is")
	}
	nm, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFA, 0xFA, 0xFA, 0xFF}})
	m = nm.(Model)
	if raw := m.View().Content; !strings.Contains(raw, rgbFG(theme.WicketLight().Hex["accent"])) {
		t.Fatalf("light background should draw Wicket light:\n%q", raw)
	}
	nm, _ = m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0x1E, 0x1E, 0x2E, 0xFF}})
	m = nm.(Model)
	if raw := m.View().Content; !strings.Contains(raw, rgbFG(theme.WicketDark().Hex["accent"])) {
		t.Fatalf("dark background should draw Wicket dark:\n%q", raw)
	}
}

// The inputs copy their styles when made, so a reply has to reach them too.
func TestTheme_BackgroundReplyRestylesTheFilter(t *testing.T) {
	m := themeModel(t, "wicket", fixtureTOML("work", "h", "u"), true)
	nm, _ := m.Update(tea.BackgroundColorMsg{Color: color.White})
	m = nm.(Model)
	if !reflect.DeepEqual(m.filter.Styles(), m.styles.input) {
		t.Fatal("filter input kept the styles from before the reply")
	}
}

func TestTheme_ForcedModeIgnoresAReply(t *testing.T) {
	m := themeModel(t, "wicket-dark", fixtureTOML("work", "h", "u"), true)
	nm, _ := m.Update(tea.BackgroundColorMsg{Color: color.White})
	m = nm.(Model)
	if raw := m.View().Content; !strings.Contains(raw, rgbFG(theme.WicketDark().Hex["accent"])) {
		t.Fatalf("wicket-dark changed on a reply:\n%q", raw)
	}
}

func TestTheme_EnvironmentBeatsConfig(t *testing.T) {
	body := "[ui]\ntheme = \"terminal\"\n" + fixtureTOML("work", "h", "u")
	if m := themeModel(t, "wicket-light", body, false); m.look.Kind != theme.KindWicket || m.look.Dark {
		t.Fatalf("WICKET_THEME should win: look %+v", m.look)
	}
	if m := themeModel(t, "", body, false); m.look.Kind != theme.KindTerminal {
		t.Fatalf("[ui] theme should apply without WICKET_THEME: look %+v", m.look)
	}
}

// A bad setting must not cost the user the connection list, but it must not
// be silently ignored either.
func TestTheme_BadSettingWarnsOnTheStatusLine(t *testing.T) {
	for _, tc := range []struct {
		name, env, body, want string
	}{
		{"unknown env", "solarized", "", "unknown theme"},
		{"unknown [ui] theme", "", "[ui]\ntheme = \"solarized\"\n", "unknown theme"},
		{"[ui] theme not a string", "", "[ui]\ntheme = 3\n", "must be a string"},
		{"omarchy without a theme file", "omarchy", "", "Omarchy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := themeModel(t, tc.env, tc.body+fixtureTOML("work", "h", "u"), true)
			if m.view != viewList {
				t.Fatalf("view %d, want the list", m.view)
			}
			out := screen(m)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("want a status containing %q:\n%s", tc.want, out)
			}
			if !strings.Contains(out, "work") {
				t.Fatalf("list missing:\n%s", out)
			}
		})
	}
}
