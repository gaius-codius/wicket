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
	EnsureID(&p)
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
	EnsureID(&p)
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

// Each entry is refused with its own reason: these are the notes an import
// summary prints, and a row that only checked for some refusal would pass
// with the check it names gone. The folders exist, so the refusal is not
// that one is missing.
func TestShares_RejectsBadEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	comma := filepath.Join(dir, "a,b")
	if err := os.Mkdir(comma, 0o700); err != nil {
		t.Fatal(err)
	}
	withShare := func(s Share) func() Profile {
		return func() Profile {
			p := validProfile()
			p.Shares = []Share{s}
			return p
		}
	}
	cases := []struct {
		name string
		p    func() Profile
		msg  string
	}{
		{"empty path", withShare(Share{Path: " "}), "path must not be empty"},
		{"comma in path", withShare(Share{Path: comma}), "path must not contain ','"},
		{"control in path", withShare(Share{Path: dir + "\x01"}), "path must not contain control characters"},
		{"slash in name", withShare(Share{Path: dir, Name: "a/b"}), "name must not contain ',' or '/'"},
		{"comma in name", withShare(Share{Path: dir, Name: "a,b"}), "name must not contain ',' or '/'"},
		{"control in name", withShare(Share{Path: dir, Name: "a\x01"}), "name must not contain control characters"},
		{"name out of set", withShare(Share{Path: dir, Name: "a b"}), "name must be letters, digits, '_' or '-'"},
		{"relative", withShare(Share{Path: "relative/path"}), "path must be absolute or ~/…"},
		{"missing dir", withShare(Share{Path: filepath.Join(dir, "nope")}), "is not a directory"},
		{"duplicate name", func() Profile {
			p := validProfile()
			p.Shares = []Share{{Path: dir, Name: "a"}, {Path: dir, Name: "a"}}
			return p
		}, `duplicate share name "a"`},
		{"home clash", func() Profile {
			p := validProfile()
			p.ShareHome = true
			p.Shares = []Share{{Path: dir, Name: "home"}}
			return p
		}, `name "home" clashes with share_home`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateProfileInUse(tc.p())
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != "shares" || !strings.Contains(fe.Msg, tc.msg) {
				t.Fatalf("err %v, want %q", err, tc.msg)
			}
		})
	}
}

// A share under ~ is refused with the reason when there is no home folder to
// expand it with. Not parallel: it unsets HOME.
func TestShares_RejectsTildeWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	p := validProfile()
	p.Shares = []Share{{Path: "~/docs"}}
	err := ValidateProfile(p)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "shares" || !strings.Contains(fe.Msg, "$HOME is not defined") {
		t.Fatalf("err %v", err)
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

// A save or a connect reads its shares once and hands on what it found, so
// the steps after it do not read them again. What it hands on covers only
// the shares it read.
func TestCheckShares_CoversOnlyTheSharesItRead(t *testing.T) {
	t.Parallel()
	dir, other := t.TempDir(), t.TempDir()
	p := Profile{
		Name: "work", Host: "h", User: "u", Client: "sdl-freerdp3", Scale: 100,
		Shares: []Share{{Path: dir}},
	}
	checked, err := CheckShares(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	// A link put in place after the check is not seen by the steps that
	// trust it, and is by any that read the shares again.
	if err := os.Symlink("/", filepath.Join(dir, "z:")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCheckedProfile(p, checked); err != nil {
		t.Fatalf("checked shares were read again: %v", err)
	}
	if err := ValidateProfileInUse(p); err == nil {
		t.Fatal("unchecked validation passed a share with a link to /")
	}
	if err := ValidateCheckedProfile(p, CheckedShares{}); err == nil {
		t.Fatal("the zero CheckedShares covered a profile with shares")
	}
	// A profile whose shares differ from the ones read is read in full.
	moved := p
	moved.Shares = []Share{{Path: other}, {Path: dir, Name: "d"}}
	if checked.Covers(moved) {
		t.Fatal("covers shares it never read")
	}
	if err := ValidateCheckedProfile(moved, checked); err == nil {
		t.Fatal("a share the check never read was trusted")
	}
	// The rest of the profile is still checked.
	noUser := p
	noUser.User = ""
	if err := ValidateCheckedProfile(noUser, checked); !errors.Is(err, ErrNoUser) {
		t.Fatalf("err = %v, want ErrNoUser", err)
	}
}
