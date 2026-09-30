package secret

import (
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
)

func TestIdentityFor_UUIDAttributes(t *testing.T) {
	t.Parallel()
	p := config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u", Domain: "D"}
	id := IdentityFor("/tmp/a.toml", p)
	a := id.Attrs()
	if a["service"] != "wicket" || a["config"] != "/tmp/a.toml" || a["profile_id"] != p.ID {
		t.Fatalf("%v", a)
	}
	if _, ok := a["profile"]; ok {
		t.Fatal("display name must not be a search attr")
	}
	if _, ok := a["host"]; ok || a["user"] != "" {
		t.Fatalf("account fields must not be search attrs: %v", a)
	}
	id2 := IdentityFor("/tmp/b.toml", p)
	if id.Config == id2.Config {
		t.Fatal("config path must isolate identities")
	}
	// Same name/host/user/domain but different id → different identity.
	other := p
	other.ID = "bbbbbbbb-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	if IdentityFor("/tmp/a.toml", p) == IdentityFor("/tmp/a.toml", other) {
		t.Fatal("distinct profile ids must not share a keyring identity")
	}
	if id.Label() != "wicket" {
		t.Fatalf("Label = %q", id.Label())
	}
}

func TestLegacyIdentity_OldAttributes(t *testing.T) {
	t.Parallel()
	p := config.Profile{Name: "work", Host: "h", User: "u", Domain: "D"}
	a := LegacyIdentity("/tmp/a.toml", p).Attrs()
	if a["service"] != "wicket" || a["config"] != "/tmp/a.toml" || a["profile"] != "work" ||
		a["host"] != "h" || a["user"] != "u" || a["domain"] != "D" {
		t.Fatalf("%v", a)
	}
	if _, ok := a["profile_id"]; ok {
		t.Fatal("legacy attrs must not include profile_id")
	}
}
