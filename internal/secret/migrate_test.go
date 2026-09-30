package secret

import (
	"errors"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
)

func TestMigrateLegacy_MovesOldAttrsToProfileID(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	p := config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u", Domain: "D"}
	pw, _ := NewPassword("s3cret")
	if err := m.Upsert(bg, LegacyIdentity("/tmp/a.toml", p), pw); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(bg, m, "/tmp/a.toml", p, true); err != nil {
		t.Fatal(err)
	}
	got, err := m.Lookup(bg, IdentityFor("/tmp/a.toml", p))
	if err != nil || !got.Password.OccursIn("s3cret") {
		t.Fatalf("migrated lookup: %v", err)
	}
	if _, err := m.Lookup(bg, LegacyIdentity("/tmp/a.toml", p)); !errors.Is(err, ErrNotFound) {
		t.Fatal("legacy item should be gone after dropLegacy")
	}
}

func TestMigrateLegacy_KeepsLegacyWhenIDsNotPersisted(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	p := config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"}
	pw, _ := NewPassword("s3cret")
	_ = m.Upsert(bg, LegacyIdentity("/tmp/a.toml", p), pw)
	if err := MigrateLegacy(bg, m, "/tmp/a.toml", p, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Lookup(bg, IdentityFor("/tmp/a.toml", p)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Lookup(bg, LegacyIdentity("/tmp/a.toml", p)); err != nil {
		t.Fatal("legacy must remain until ids are on disk")
	}
}

func TestPresenceMigrating_SeesLegacyWithoutReadingSecret(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	p := config.Profile{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: "work", Host: "h", User: "u"}
	pw, _ := NewPassword("s3cret")
	_ = m.Upsert(bg, LegacyIdentity("/tmp/a.toml", p), pw)
	pr, err := PresenceMigrating(bg, m, "/tmp/a.toml", p)
	if err != nil || pr != Saved {
		t.Fatalf("presence = %v %v", pr, err)
	}
}
