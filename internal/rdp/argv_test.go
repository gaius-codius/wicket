package rdp

import (
	"slices"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
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
		"/from-stdin:force",
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
		"/from-stdin:force",
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
