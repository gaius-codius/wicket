package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config written before the sharing settings existed opens with FreeRDP's
// behaviour, clipboard on and the rest off, and a save that does not touch
// them writes none of their keys.
func TestSharing_OldConfigKeepsItsShape(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Profile("work")
	if !p.Clipboard || p.Multimon || p.ShareHome {
		t.Fatalf("defaults: clipboard %v multimon %v share_home %v", p.Clipboard, p.Multimon, p.ShareHome)
	}
	p.User = "u2"
	if err := c.Upsert(p, "work"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	for _, key := range []string{"multimon", "clipboard", "share_home"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("default %s written:\n%s", key, raw)
		}
	}
}

func TestSharing_RoundTrip(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, "[general]\n")
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := validProfile()
	p.Fullscreen, p.Multimon, p.Clipboard, p.ShareHome = true, true, false, true
	if err := c.Upsert(p, ""); err != nil {
		t.Fatal(err)
	}
	raw := decodeRaw(t, path)
	prof := profileTable(t, raw, p.Name)
	for key, want := range map[string]bool{"multimon": true, "clipboard": false, "share_home": true} {
		assertType(t, prof[key], want)
	}
	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := c2.Profile(p.Name); !got.Equal(p) {
		t.Fatalf("got %+v want %+v", got, p)
	}

	// Back to the defaults, the keys go again.
	p.Multimon, p.Clipboard, p.ShareHome = false, true, false
	if err := c2.Upsert(p, p.Name); err != nil {
		t.Fatal(err)
	}
	prof = profileTable(t, decodeRaw(t, path), p.Name)
	for _, key := range []string{"multimon", "clipboard", "share_home"} {
		if _, ok := prof[key]; ok {
			t.Errorf("default %s still written", key)
		}
	}
}

// All monitors means full screen on each, so a hand-edited multimon
// without fullscreen loads as fullscreen, which is what it will do.
func TestSharing_MultimonImpliesFullscreen(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
multimon = true
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := c.Profile("work"); !p.Multimon || !p.Fullscreen {
		t.Fatalf("multimon %v fullscreen %v", p.Multimon, p.Fullscreen)
	}
}

func TestSharing_RejectsNonBooleans(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"multimon", "clipboard", "share_home"} {
		path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
`+key+` = "yes"
`)
		_, err := Open(path)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != key {
			t.Errorf("%s = \"yes\": err %v", key, err)
		}
	}
}

func TestShares_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTOML(t, "[general]\n")
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := validProfile()
	p.Shares = []Share{
		{Path: dir},
		{Path: dir, Name: "docs"},
	}
	// Two shares with the same path need distinct names.
	p.Shares[0].Name = "data"
	if err := c.Upsert(p, ""); err != nil {
		t.Fatal(err)
	}
	raw := decodeRaw(t, path)
	prof := profileTable(t, raw, p.Name)
	shares, ok := prof["shares"].([]map[string]any)
	if !ok {
		// BurntSushi may decode as []any of maps.
		arr, ok := prof["shares"].([]any)
		if !ok {
			t.Fatalf("shares type %T", prof["shares"])
		}
		shares = make([]map[string]any, len(arr))
		for i, item := range arr {
			shares[i] = item.(map[string]any)
		}
	}
	if len(shares) != 2 {
		t.Fatalf("shares: %#v", shares)
	}
	assertType(t, shares[0]["name"], "data")
	assertType(t, shares[0]["path"], dir)
	assertType(t, shares[1]["name"], "docs")
	assertType(t, shares[1]["path"], dir)

	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := c2.Profile(p.Name)
	if !got.Equal(p) {
		t.Fatalf("got %+v want %+v", got, p)
	}

	p.Shares = nil
	if err := c2.Upsert(p, p.Name); err != nil {
		t.Fatal(err)
	}
	prof = profileTable(t, decodeRaw(t, path), p.Name)
	if _, ok := prof["shares"]; ok {
		t.Fatalf("empty shares still written: %#v", prof["shares"])
	}
}

func TestShares_OldConfigOmitsKey(t *testing.T) {
	t.Parallel()
	path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Profile("work")
	if len(p.Shares) != 0 {
		t.Fatalf("shares %v", p.Shares)
	}
	p.User = "u2"
	if err := c.Upsert(p, "work"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "shares") {
		t.Errorf("shares written:\n%s", raw)
	}
}

func TestShares_RejectsBadEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cases := []struct {
		name string
		p    func() Profile
	}{
		{"relative", func() Profile {
			p := validProfile()
			p.Shares = []Share{{Path: "relative/path"}}
			return p
		}},
		{"missing dir", func() Profile {
			p := validProfile()
			p.Shares = []Share{{Path: filepath.Join(dir, "nope")}}
			return p
		}},
		{"duplicate name", func() Profile {
			p := validProfile()
			p.Shares = []Share{{Path: dir, Name: "a"}, {Path: dir, Name: "a"}}
			return p
		}},
		{"home clash", func() Profile {
			p := validProfile()
			p.ShareHome = true
			p.Shares = []Share{{Path: dir, Name: "home"}}
			return p
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateProfileInUse(tc.p())
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != "shares" {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestShares_LoadSkipsMissingDir(t *testing.T) {
	t.Parallel()
	// A share whose folder has gone must still load; only save/connect refuse.
	path := writeTOML(t, `
[general]
[[profiles]]
name = "work"
host = "h"
user = "u"
shares = [{ path = "/no/such/wicket/share/dir" }]
`)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Profile("work")
	if len(p.Shares) != 1 || p.Shares[0].Path != "/no/such/wicket/share/dir" {
		t.Fatalf("%+v", p.Shares)
	}
	if err := ValidateProfile(p); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProfileInUse(p); err == nil {
		t.Fatal("expected missing dir to fail in-use validation")
	}
}

func TestShareNameFromPath(t *testing.T) {
	t.Parallel()
	if got := ShareNameFromPath("/home/u/My Documents"); got != "My_Documents" {
		t.Fatalf("got %q", got)
	}
	if got := ShareNameFromPath("/"); got != "share" {
		t.Fatalf("root: %q", got)
	}
	if got := ShareNameFromPath("/home/u/Документы"); got != "share" {
		t.Fatalf("non-ASCII: %q", got)
	}
}

func TestProfile_CloneSharesAreIndependent(t *testing.T) {
	t.Parallel()
	p := validProfile()
	p.Shares = []Share{{Path: "/a", Name: "a"}}
	c := p.Clone()
	c.Shares[0].Path = "/b"
	if p.Shares[0].Path != "/a" {
		t.Fatalf("clone aliased shares: %q", p.Shares[0].Path)
	}
}

func TestConfig_ProfileClonesShares(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTOML(t, "[general]\n")
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := validProfile()
	p.Shares = []Share{{Path: dir, Name: "docs"}}
	if err := c.Upsert(p, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := c.Profile("work")
	got.Shares[0].Path = filepath.Join(dir, "other")
	again, _ := c.Profile("work")
	if again.Shares[0].Path != dir {
		t.Fatalf("Profile aliased shares: %+v", again.Shares)
	}
}
