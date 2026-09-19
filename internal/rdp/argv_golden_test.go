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
			},
		},
		{
			file: "xfreerdp3.argv",
			p: config.Profile{
				Name: "lab", Host: "h", User: "u",
				Client: "xfreerdp3", Size: "1920x1080", Fullscreen: true,
				DynamicResolution: true, Scale: 140,
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
