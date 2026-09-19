package secret

import (
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
)

func TestIdentityFor_AllAttributes(t *testing.T) {
	t.Parallel()
	p := config.Profile{Name: "work", Host: "h", User: "u", Domain: "D"}
	id := IdentityFor("/tmp/a.toml", p)
	a := id.Attrs()
	if a["service"] != "wicket" || a["config"] != "/tmp/a.toml" || a["profile"] != "work" ||
		a["host"] != "h" || a["user"] != "u" || a["domain"] != "D" {
		t.Fatalf("%v", a)
	}
	id2 := IdentityFor("/tmp/b.toml", p)
	if id == id2 {
		t.Fatal("config path must isolate identities")
	}
}
