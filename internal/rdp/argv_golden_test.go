package rdp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
)

func TestBuildPlan_Goldens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file string
		p    config.Profile
	}{
		{
			file: "sdl-freerdp3.argv",
			p: config.Profile{
				Name: "work", Host: "192.168.1.20", User: "jdoe", Domain: "CORP",
				Client: "sdl-freerdp3", Size: "100%", DynamicResolution: true, Scale: 100,
				Clipboard: true,
			},
		},
		{
			file: "xfreerdp3.argv",
			p: config.Profile{
				Name: "lab", Host: "h", User: "u",
				Client: "xfreerdp3", Size: "1920x1080", Fullscreen: true,
				DynamicResolution: true, Scale: 140, Clipboard: true,
			},
		},
		// The sharing settings, each away from its default. Both clients
		// take the same syntax for them.
		{
			file: "sdl-freerdp3-sharing.argv",
			p: config.Profile{
				Name: "work", Host: "h", User: "u", Client: "sdl-freerdp3",
				DynamicResolution: true, Scale: 100,
				Multimon: true, Clipboard: false, ShareHome: true,
			},
		},
		{
			file: "xfreerdp3-sharing.argv",
			p: config.Profile{
				Name: "lab", Host: "h", User: "u", Client: "xfreerdp3",
				Fullscreen: true, Scale: 100,
				Multimon: true, Clipboard: false, ShareHome: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			plan, err := BuildPlan(tc.p)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(append([]string{plan.Client}, plan.Args...), "\n") + "\n"
			path := filepath.Join("testdata", tc.file)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("got:\n%s\nwant:\n%s", got, want)
			}
			if strings.Contains(got, "/p:") || strings.Contains(got, "cert:ignore") {
				t.Fatal("forbidden token")
			}
		})
	}
}

func TestBuildPlan_FolderShares(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := filepath.Join(dir, "Documents")
	b := filepath.Join(dir, "work stuff")
	if err := os.Mkdir(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(b, 0o755); err != nil {
		t.Fatal(err)
	}
	p := config.Profile{
		Name: "work", Host: "h", User: "u", Client: "sdl-freerdp3",
		DynamicResolution: true, Scale: 100, Clipboard: true,
		ShareHome: true,
		Shares: []config.Share{
			{Path: a},
			{Path: b, Name: "projects"},
		},
	}
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(plan.Args, "\n")
	if !strings.Contains(args, "+home-drive") {
		t.Fatalf("missing home-drive:\n%s", args)
	}
	wantDocs := "/drive:Documents," + a
	wantProj := "/drive:projects," + b
	if !strings.Contains(args, wantDocs) || !strings.Contains(args, wantProj) {
		t.Fatalf("got:\n%s\nwant %s and %s", args, wantDocs, wantProj)
	}
	joined := strings.Join(plan.Args, " ")
	i := strings.Index(joined, "+home-drive")
	j := strings.Index(joined, wantDocs)
	k := strings.Index(joined, wantProj)
	l := strings.Index(joined, "/from-stdin:force")
	if !(i < j && j < k && k < l) {
		t.Fatalf("order: %v", plan.Args)
	}
}
