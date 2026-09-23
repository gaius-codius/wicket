package config

import (
	"errors"
	"os"
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
	if got, _ := c2.Profile(p.Name); got != p {
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
