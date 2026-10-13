package config

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAddProfiles_OneWrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	c, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	a := DefaultProfile()
	a.Name, a.Host, a.User = "a", "h1", "u1"
	b := DefaultProfile()
	b.Name, b.Host, b.User = "b", "h2", "u2"
	added, skipped, _, err := c.AddProfiles([]Profile{a, b}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || len(skipped) != 0 {
		t.Fatalf("added=%v skipped=%v", added, skipped)
	}
	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Profiles()) != 2 {
		t.Fatalf("profiles %d", len(c2.Profiles()))
	}
}

func TestAddProfiles_CollisionSkipAndRename(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dup := DefaultProfile()
	dup.Name, dup.Host, dup.User = "work", "h2", "u2"
	other := DefaultProfile()
	other.Name, other.Host, other.User = "lab", "h3", "u3"

	added, skipped, _, err := c.AddProfiles([]Profile{dup, other}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Name != "lab" {
		t.Fatalf("added %+v", added)
	}
	if len(skipped) != 1 || skipped[0].Reason != "name already used" {
		t.Fatalf("skipped %+v", skipped)
	}

	added, skipped, _, err = c.AddProfiles([]Profile{dup}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Name != "work-2" || len(skipped) != 0 {
		t.Fatalf("rename added=%+v skipped=%+v", added, skipped)
	}
	if _, ok := c.Profile("work-2"); !ok {
		t.Fatal("work-2 missing")
	}
}

func TestAddProfiles_InvalidSkippedNoWrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	c, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := DefaultProfile()
	bad.Name, bad.Host, bad.User = "bad", "bad host", "u"
	added, skipped, _, err := c.AddProfiles([]Profile{bad}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(skipped) != 1 {
		t.Fatalf("added=%v skipped=%v", added, skipped)
	}
	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Profiles()) != 0 {
		t.Fatalf("wrote invalid profile: %+v", c2.Profiles())
	}
}

func TestNextFreeName(t *testing.T) {
	t.Parallel()
	taken := map[string]bool{"work": true, "work-2": true}
	if got := nextFreeName("work", taken); got != "work-3" {
		t.Fatalf("got %q", got)
	}
	if got := nextFreeName("lab", taken); got != "lab" {
		t.Fatalf("got %q", got)
	}
}

// PlanProfiles is what a dry run shows; it must match what AddProfiles writes.
func TestPlanProfiles_MatchesAddProfiles(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name, host string) Profile {
		p := DefaultProfile()
		p.Name, p.Host, p.User = name, host, "u"
		return p
	}
	in := []Profile{mk("work", "h2"), mk("work", "h3"), mk("bad", "bad host"), mk("lab", "h4")}
	in[1].Shares = []Share{{Path: "/tmp/a\nb"}, {Path: "/tmp/ok"}}
	planned, planSkipped, planDropped := PlanProfiles(c.Profiles(), in, true)
	added, skipped, dropped, err := c.AddProfiles(in, true)
	if err != nil {
		t.Fatal(err)
	}
	// AddProfiles mints fresh ids; PlanProfiles does not. Compare the rest.
	for i := range planned {
		planned[i].ID = ""
	}
	for i := range added {
		if added[i].ID == "" {
			t.Fatalf("added[%d] missing id", i)
		}
		added[i].ID = ""
	}
	if fmt.Sprint(planned) != fmt.Sprint(added) || fmt.Sprint(planSkipped) != fmt.Sprint(skipped) {
		t.Fatalf("plan %v %v, add %v %v", planned, planSkipped, added, skipped)
	}
	if fmt.Sprint(planDropped) != fmt.Sprint(dropped) || len(dropped) != 1 || dropped[0].Profile != "work-3" {
		t.Fatalf("plan dropped %v, add dropped %v", planDropped, dropped)
	}
	var names []string
	for _, p := range added {
		names = append(names, p.Name)
	}
	if fmt.Sprint(names) != "[work-2 work-3 lab]" || len(skipped) != 1 || skipped[0].Name != "bad" {
		t.Fatalf("added %v skipped %v", names, skipped)
	}
}

func TestAddProfiles_KeepsMissingShareDirDropsBadShare(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	c, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := DefaultProfile()
	p.Name, p.Host, p.User = "nas", "h", "u"
	p.Shares = []Share{
		{Path: "/no/such/wicket/import/share"},
		{Path: "relative/nope"},
		{Path: dir, Name: "My Docs"},
	}
	added, skipped, dropped, err := c.AddProfiles([]Profile{p}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || len(skipped) != 0 {
		t.Fatalf("added=%+v skipped=%+v", added, skipped)
	}
	want := []DroppedShare{{Profile: "nas", Path: "relative/nope", Reason: "path must be absolute or ~/…"}}
	if !reflect.DeepEqual(dropped, want) {
		t.Fatalf("dropped %+v, want %+v", dropped, want)
	}
	got := added[0].Shares
	if len(got) != 2 || got[0].Path != "/no/such/wicket/import/share" {
		t.Fatalf("shares %+v", got)
	}
	if got[1].Path != dir || got[1].Name != "My_Docs" {
		t.Fatalf("named share %+v", got[1])
	}
	if err := ValidateProfile(added[0]); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProfileInUse(added[0]); err == nil {
		t.Fatal("missing dir should still fail in-use validation")
	}
}

// A share the plan drops is reported with its reason, under the name the
// profile is imported as, so the summary does not claim it came across. A
// profile that is skipped outright reports none: its skip says enough.
func TestPlanProfiles_ReportsDroppedShares(t *testing.T) {
	t.Parallel()
	mk := func(name, host string, shares ...Share) Profile {
		p := DefaultProfile()
		p.Name, p.Host, p.User = name, host, "u"
		p.Shares = shares
		return p
	}
	existing := []Profile{mk("work", "h")}
	in := []Profile{
		mk("work", "h2",
			Share{Path: "/tmp/docs"},
			Share{Path: "/tmp/a\nb"},
			Share{Path: "/srv/docs"},                // drive "docs" again
			Share{Path: "/tmp/docs/", Name: "Docs"}, // the kept share again
		),
		mk("bad", "bad host", Share{Path: "relative"}),
		mk("lab", "h3", Share{Path: "/tmp/lab"}),
	}
	accepted, skipped, dropped := PlanProfiles(existing, in, true)
	if len(accepted) != 2 || len(skipped) != 1 {
		t.Fatalf("accepted %+v skipped %+v", accepted, skipped)
	}
	if got := accepted[0].Shares; !reflect.DeepEqual(got, []Share{{Path: "/tmp/docs"}}) {
		t.Fatalf("kept shares %+v", got)
	}
	want := []DroppedShare{
		{Profile: "work-2", Path: "/tmp/a\nb", Reason: "path must not contain control characters"},
		{Profile: "work-2", Path: "/srv/docs", Reason: `duplicate share name "docs"`},
	}
	if !reflect.DeepEqual(dropped, want) {
		t.Fatalf("dropped %+v, want %+v", dropped, want)
	}
}
