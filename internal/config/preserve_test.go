package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestPreserveUnknownKeysAndTypes(t *testing.T) {
	t.Parallel()
	src := `
[general]
keep = 1.5
flag = true
when = 2026-09-18T14:00:00+10:00
tags = ["a", "b"]
count = 7

[other]
nested_int = 3
nested_float = 2.25

[[other_tables]]
id = 1
name = "x"

[[profiles]]
name = "work"
host = "h"
user = "old"
mystery = true
ratio = 1.25
items = [1, 2, 3]
when = 2026-01-02T03:04:05Z
`
	path := writeTOML(t, src)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Profile("work")
	p.User = "new"
	if err := c.Upsert(p, "work"); err != nil {
		t.Fatal(err)
	}

	raw := decodeRaw(t, path)
	general := raw["general"].(map[string]any)
	assertType(t, general["keep"], 1.5)
	assertType(t, general["flag"], true)
	assertType(t, general["count"], int64(7))
	if _, ok := general["when"].(time.Time); !ok {
		t.Fatalf("general.when type %T", general["when"])
	}
	tags, ok := general["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "a" {
		t.Fatalf("tags %+v", general["tags"])
	}

	other := raw["other"].(map[string]any)
	assertType(t, other["nested_int"], int64(3))
	assertType(t, other["nested_float"], 2.25)

	ots, ok := raw["other_tables"].([]map[string]any)
	if !ok {
		if arr, ok := raw["other_tables"].([]any); ok && len(arr) == 1 {
			ots = []map[string]any{arr[0].(map[string]any)}
		} else {
			t.Fatalf("other_tables type %T", raw["other_tables"])
		}
	}
	if ots[0]["id"] != int64(1) || ots[0]["name"] != "x" {
		t.Fatalf("other_tables %+v", ots)
	}

	prof := profileTable(t, raw, "work")
	if prof["user"] != "new" {
		t.Fatalf("user %v", prof["user"])
	}
	assertType(t, prof["mystery"], true)
	assertType(t, prof["ratio"], 1.25)
	items, ok := prof["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("items %+v", prof["items"])
	}
	if _, ok := prof["when"].(time.Time); !ok {
		t.Fatalf("profile.when type %T", prof["when"])
	}
}

func TestStripPasswordFamilyKeys(t *testing.T) {
	t.Parallel()
	src := `
[general]
Password = "nope"
keep = true

[[profiles]]
name = "work"
host = "h"
user = "u"
pass = "x"
SECRET = "y"
passwd = "z"
password = "w"
`
	path := writeTOML(t, src)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings()) == 0 {
		t.Fatal("want strip warnings")
	}
	p, _ := c.Profile("work")
	p.User = "u2"
	if err := c.Upsert(p, "work"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(body))
	for _, k := range []string{"password", "pass ", "secret", "passwd"} {
		if strings.Contains(lower, k+" ") || strings.Contains(lower, k+"=") {
			t.Fatalf("rewrote secret key in:\n%s", body)
		}
	}
	if strings.Contains(string(body), "nope") || strings.Contains(string(body), `"x"`) {
		t.Fatalf("secret value survived:\n%s", body)
	}
}

func decodeRaw(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{}
	if _, err := toml.Decode(string(b), &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func profileTable(t *testing.T, raw map[string]any, name string) map[string]any {
	t.Helper()
	switch ps := raw["profiles"].(type) {
	case []map[string]any:
		for _, p := range ps {
			if p["name"] == name {
				return p
			}
		}
	case []any:
		for _, item := range ps {
			p := item.(map[string]any)
			if p["name"] == name {
				return p
			}
		}
	default:
		t.Fatalf("profiles type %T", raw["profiles"])
	}
	t.Fatalf("profile %q missing", name)
	return nil
}

func assertType(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v (%T), want %#v (%T)", got, got, want, want)
	}
}
