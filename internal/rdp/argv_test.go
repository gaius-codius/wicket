package rdp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/importer"
)

func TestBuildPlan_DefaultOrder(t *testing.T) {
	t.Parallel()
	p := config.Profile{
		Name:              "work",
		Host:              "192.168.1.20",
		User:              "jdoe",
		Domain:            "CORP",
		Client:            "sdl-freerdp3",
		Size:              "100%",
		DynamicResolution: true,
		Scale:             100,
		Clipboard:         true,
	}
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/v:192.168.1.20",
		"/u:jdoe",
		"/d:CORP",
		"/size:100%",
		"+dynamic-resolution",
	}
	if strings.Join(plan.Args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("args =\n%s\nwant\n%s", strings.Join(plan.Args, "\n"), strings.Join(want, "\n"))
	}
	joined := strings.Join(plan.Args, " ")
	if strings.Contains(joined, "/p:") || strings.Contains(joined, "cert:ignore") {
		t.Fatal(joined)
	}
}

func TestBuildPlan_Omits(t *testing.T) {
	t.Parallel()
	p := config.Profile{
		Name:              "lab",
		Host:              "h",
		User:              "u",
		Client:            "xfreerdp3",
		Fullscreen:        true,
		DynamicResolution: false,
		Scale:             180,
		Size:              "1920x1080",
		Clipboard:         true,
	}
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/v:h",
		"/u:u",
		"/size:1920x1080",
		"/f",
		"/scale:180",
	}
	if strings.Join(plan.Args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(plan.Args, "\n"))
	}
	for _, a := range plan.Args {
		if a == "+dynamic-resolution" || strings.HasPrefix(a, "/d:") {
			t.Fatalf("unexpected %q", a)
		}
	}
}

func TestBuildPlan_RejectsPathClient(t *testing.T) {
	t.Parallel()
	p := config.Profile{Name: "n", Host: "h", User: "u", Client: "/tmp/x", Scale: 100, DynamicResolution: true}
	if _, err := BuildPlan(p); err == nil {
		t.Fatal("want error")
	}
}

// Validation tolerates padding around size, so BuildPlan has to trim it
// rather than hand FreeRDP an argument with spaces inside the value.
func TestBuildPlan_TrimsSize(t *testing.T) {
	p := config.Profile{Name: "n", Host: "h", User: "u", Client: "sdl-freerdp3", Scale: 100, Size: "  1920x1080 "}
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "/size:1920x1080"
	if !slices.Contains(plan.Args, want) {
		t.Fatalf("args = %v, want %q", plan.Args, want)
	}
}

// A share that holds the whole home folder is refused at connect, as at
// import, though the config was written by hand: share_home is the way to
// share home.
func TestBuildPlan_RefusesHomeShare(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"~", home, filepath.Dir(home), "/"} {
		p := config.Profile{Name: "n", Host: "h", User: "u", Client: "sdl-freerdp3", Scale: 100, Shares: []config.Share{{Path: path, Name: "x"}}}
		if _, err := BuildPlan(p); err == nil || !strings.Contains(err.Error(), "share_home") {
			t.Errorf("%s: err = %v, want a refusal pointing at share_home", path, err)
		}
	}
}

// An import keeps a share whose folder does not exist yet. If it becomes a
// link to home, to /, or through /proc before the connect, or a link inside
// it does, the plan refuses it rather than hand it to FreeRDP.
func TestBuildPlan_RefusesShareLinkedAfterImport(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	targets := []string{home, "/", filepath.Dir(home)}
	if _, err := os.Stat("/proc/self/root"); err == nil {
		targets = append(targets, "/proc/self/root")
	}
	for _, target := range targets {
		for _, inside := range []bool{false, true} {
			share := filepath.Join(t.TempDir(), "wicket-share")
			imp, _, err := importer.ParseRDP([]byte("full address:s:h\nusername:s:u\ndrivestoredirect:s:"+share+"\n"), "x.rdp")
			if err != nil {
				t.Fatal(err)
			}
			if len(imp.Shares) != 1 {
				t.Fatalf("import kept %+v, refused %+v", imp.Shares, imp.Refused)
			}
			link := share
			if inside {
				if err := os.Mkdir(share, 0o700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(share, "z:")
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			p := imp.Profile
			p.Client = "sdl-freerdp3"
			if _, err := BuildPlan(p); err == nil || !strings.Contains(err.Error(), "home folder") && !strings.Contains(err.Error(), "/proc") {
				t.Errorf("link to %s (inside %v): err = %v", target, inside, err)
			}
		}
	}
}
