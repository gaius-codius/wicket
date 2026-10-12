package rdp

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/config"
)

func onPath(names ...string) func(string) (string, error) {
	return func(file string) (string, error) {
		if slices.Contains(names, file) {
			return "/fake/bin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
}

// reports is a version probe under which every client is FreeRDP major.
func reports(major int) func(string) (int, bool) {
	return func(string) (int, bool) { return major, true }
}

// Only the known clients are offered, in order of preference, and a new
// profile gets the first one installed or the fallback.
func TestInstalledClients_PreferenceOrder(t *testing.T) {
	for _, tc := range []struct {
		path      []string
		want      []string
		preferred string
	}{
		{[]string{"xfreerdp3", "sdl-freerdp3", "wlfreerdp3"}, []string{"sdl-freerdp3", "xfreerdp3"}, "sdl-freerdp3"},
		{[]string{"xfreerdp3", "wlfreerdp3"}, []string{"xfreerdp3"}, "xfreerdp3"},
		{[]string{"wlfreerdp3"}, nil, config.DefaultClient},
		{nil, nil, config.DefaultClient},
		// Homebrew installs the unsuffixed names. SDL still beats X11, and a
		// suffixed binary still beats the unsuffixed one of the same client.
		{[]string{"xfreerdp", "sdl-freerdp"}, []string{"sdl-freerdp", "xfreerdp"}, "sdl-freerdp"},
		{[]string{"xfreerdp"}, []string{"xfreerdp"}, "xfreerdp"},
		{[]string{"sdl-freerdp3", "sdl-freerdp", "xfreerdp3", "xfreerdp"}, []string{"sdl-freerdp3", "sdl-freerdp", "xfreerdp3", "xfreerdp"}, "sdl-freerdp3"},
		{[]string{"xfreerdp3", "sdl-freerdp"}, []string{"sdl-freerdp", "xfreerdp3"}, "sdl-freerdp"},
	} {
		got := InstalledClients(onPath(tc.path...), reports(3))
		var names []string
		for _, c := range got {
			names = append(names, c.Name)
		}
		if !slices.Equal(names, tc.want) {
			t.Errorf("PATH %v: installed %v, want %v", tc.path, names, tc.want)
		}
		if p := PreferredClient(got); p != tc.preferred {
			t.Errorf("PATH %v: preferred %q, want %q", tc.path, p, tc.preferred)
		}
	}
}

// An unsuffixed client counts only when it reports FreeRDP 3; a suffixed
// one is taken at its name and never run. The order is unchanged by which
// ones qualify.
func TestInstalledClients_UnversionedNeedFreeRDP3(t *testing.T) {
	all := []string{"sdl-freerdp3", "sdl-freerdp", "xfreerdp3", "xfreerdp"}
	for _, tc := range []struct {
		name      string
		path      []string
		majors    map[string]int // by path; absent means the probe failed
		want      []string
		preferred string
	}{
		{"both 3", all, map[string]int{"/fake/bin/sdl-freerdp": 3, "/fake/bin/xfreerdp": 3}, all, "sdl-freerdp3"},
		{"xfreerdp 2 beside suffixed", all, map[string]int{"/fake/bin/sdl-freerdp": 3, "/fake/bin/xfreerdp": 2},
			[]string{"sdl-freerdp3", "sdl-freerdp", "xfreerdp3"}, "sdl-freerdp3"},
		{"only xfreerdp 2", []string{"xfreerdp"}, map[string]int{"/fake/bin/xfreerdp": 2}, nil, config.DefaultClient},
		{"xfreerdp 2 with homebrew sdl", []string{"sdl-freerdp", "xfreerdp"}, map[string]int{"/fake/bin/sdl-freerdp": 3, "/fake/bin/xfreerdp": 2},
			[]string{"sdl-freerdp"}, "sdl-freerdp"},
		{"xfreerdp 3 alone", []string{"xfreerdp"}, map[string]int{"/fake/bin/xfreerdp": 3}, []string{"xfreerdp"}, "xfreerdp"},
		{"xfreerdp 4", []string{"xfreerdp"}, map[string]int{"/fake/bin/xfreerdp": 4}, nil, config.DefaultClient},
		{"probe failed", []string{"sdl-freerdp", "xfreerdp"}, nil, nil, config.DefaultClient},
		{"probe failed beside suffixed", []string{"xfreerdp3", "xfreerdp"}, nil, []string{"xfreerdp3"}, "xfreerdp3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ran []string
			probe := func(path string) (int, bool) {
				ran = append(ran, path)
				v, ok := tc.majors[path]
				return v, ok
			}
			got := InstalledClients(onPath(tc.path...), probe)
			var names []string
			for _, c := range got {
				names = append(names, c.Name)
			}
			if !slices.Equal(names, tc.want) {
				t.Errorf("installed %v, want %v", names, tc.want)
			}
			if p := PreferredClient(got); p != tc.preferred {
				t.Errorf("preferred %q, want %q", p, tc.preferred)
			}
			for _, path := range ran {
				if path != "/fake/bin/sdl-freerdp" && path != "/fake/bin/xfreerdp" {
					t.Errorf("ran %s, a suffixed or absent client", path)
				}
			}
		})
	}
}

// Two names that resolve to the same binary run it once per discovery.
func TestInstalledClients_RunsEachBinaryOnce(t *testing.T) {
	runs := 0
	lookPath := func(file string) (string, error) {
		if file == "sdl-freerdp" || file == "xfreerdp" {
			return "/fake/bin/freerdp", nil
		}
		return "", exec.ErrNotFound
	}
	got := InstalledClients(lookPath, func(string) (int, bool) { runs++; return 3, true })
	if len(got) != 2 || runs != 1 {
		t.Fatalf("installed %v after %d runs, want both after 1", got, runs)
	}
}

// Names that are links to one binary run it once, as Homebrew-style
// layouts can make them.
func TestInstalledClients_RunsALinkedBinaryOnce(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "freerdp")
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sdl-freerdp", "xfreerdp"} {
		if err := os.Symlink(bin, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	lookPath := func(file string) (string, error) {
		if file == "sdl-freerdp" || file == "xfreerdp" {
			return filepath.Join(dir, file), nil
		}
		return "", exec.ErrNotFound
	}
	runs := 0
	got := InstalledClients(lookPath, func(string) (int, bool) { runs++; return 3, true })
	if len(got) != 2 || runs != 1 {
		t.Fatalf("installed %v after %d runs, want both after 1", got, runs)
	}
}

// Every known client's exit codes are read as FreeRDP's.
func TestKnownClients_AreFreeRDPs(t *testing.T) {
	for _, c := range KnownClients {
		if !IsFreeRDP(c.Name) || c.About == "" {
			t.Errorf("%s: IsFreeRDP %v, about %q", c.Name, IsFreeRDP(c.Name), c.About)
		}
		sdl, x11 := IsSDLClient(c.Name), IsX11Client(c.Name)
		if sdl == x11 {
			t.Errorf("%s: IsSDLClient %v, IsX11Client %v", c.Name, sdl, x11)
		}
	}
	if IsSDLClient("/opt/homebrew/bin/sdl-freerdp") != true || IsX11Client("xfreerdp3") != true {
		t.Fatal("basename classification")
	}
	if IsSDLClient("xfreerdp") || IsX11Client("sdl-freerdp3") || IsSDLClient("myrdp") {
		t.Fatal("unrelated or crossed client classified as SDL/X11")
	}
}

func TestOutcome_PreConnectFailed(t *testing.T) {
	for _, tc := range []struct {
		o    Outcome
		want bool
	}{
		{Outcome{Client: "sdl-freerdp3", ExitCode: 136, Duration: time.Second}, true},
		{Outcome{Client: "xfreerdp3", ExitCode: 136}, true},
		{Outcome{Client: "sdl-freerdp3", ExitCode: 131}, false},
		{Outcome{Client: "myrdp", ExitCode: 136}, false},
		{Outcome{Client: "sdl-freerdp3", ExitCode: 136, Signaled: true}, false},
	} {
		if got := tc.o.PreConnectFailed(); got != tc.want {
			t.Errorf("%+v: %v, want %v", tc.o, got, tc.want)
		}
	}
}
