package rdp

import (
	"os/exec"
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
	} {
		got := InstalledClients(onPath(tc.path...))
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

// Every known client's exit codes are read as FreeRDP's.
func TestKnownClients_AreFreeRDPs(t *testing.T) {
	for _, c := range KnownClients {
		if !IsFreeRDP(c.Name) || c.About == "" {
			t.Errorf("%s: IsFreeRDP %v, about %q", c.Name, IsFreeRDP(c.Name), c.About)
		}
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
