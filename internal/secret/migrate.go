package secret

import (
	"context"
	"errors"

	"github.com/gaius-codius/wicket/internal/config"
)

// MigrateLegacy moves a password stored under the pre-UUID attribute set to
// the profile_id identity. It is a no-op when the new identity already has an
// item or when no legacy item exists.
//
// dropLegacy controls whether the old item is deleted after a successful
// copy. Callers must pass false while newly minted profile ids exist only in
// memory: deleting the legacy item then would orphan the secret under a UUID
// that the next load never sees again. Once ids are on disk, pass true.
func MigrateLegacy(ctx context.Context, s Store, configPath string, p config.Profile, dropLegacy bool) error {
	_, err := LookupMigrating(ctx, s, configPath, p, dropLegacy)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// LookupMigrating migrates a legacy item if needed, then returns the secret
// under the current identity. One Lookup of the new identity is enough when
// it already has an item.
func LookupMigrating(ctx context.Context, s Store, configPath string, p config.Profile, dropLegacy bool) (LookupResult, error) {
	if p.ID == "" {
		return LookupResult{}, ErrNotFound
	}
	newID := IdentityFor(configPath, p)
	res, err := s.Lookup(ctx, newID)
	switch {
	case err == nil:
		if dropLegacy {
			_ = s.Delete(ctx, LegacyIdentity(configPath, p))
		}
		return res, nil
	case !errors.Is(err, ErrNotFound):
		return LookupResult{}, err
	}
	legacy := LegacyIdentity(configPath, p)
	res, err = s.Lookup(ctx, legacy)
	if errors.Is(err, ErrNotFound) {
		return LookupResult{}, ErrNotFound
	}
	if err != nil {
		return LookupResult{}, err
	}
	pw := res.Password
	defer pw.Clear()
	if err := s.Upsert(ctx, newID, pw); err != nil {
		return LookupResult{}, err
	}
	if dropLegacy {
		_ = s.Delete(ctx, legacy)
	}
	return s.Lookup(ctx, newID)
}

// PresenceMigrating reports whether a password is stored under the current
// identity or a still-unmigrated legacy item. It never reads the secret and
// never migrates, so it is safe on the list's selection path.
func PresenceMigrating(ctx context.Context, s Store, configPath string, p config.Profile) (Presence, error) {
	if p.ID == "" {
		return NotSaved, nil
	}
	pr, err := s.Presence(ctx, IdentityFor(configPath, p))
	if err != nil {
		return NotSaved, err
	}
	if pr == Saved {
		return Saved, nil
	}
	return s.Presence(ctx, LegacyIdentity(configPath, p))
}

// DeleteMigrating removes the current identity and any leftover legacy item
// for p, so a delete does not leave a name-keyed orphan behind.
func DeleteMigrating(ctx context.Context, s Store, configPath string, p config.Profile) error {
	id := IdentityFor(configPath, p)
	err := s.Delete(ctx, id)
	legacyErr := s.Delete(ctx, LegacyIdentity(configPath, p))
	if err == nil || errors.Is(err, ErrNotFound) {
		if legacyErr == nil || errors.Is(legacyErr, ErrNotFound) {
			if errors.Is(err, ErrNotFound) && errors.Is(legacyErr, ErrNotFound) {
				return ErrNotFound
			}
			return nil
		}
		if errors.Is(err, ErrNotFound) {
			return legacyErr
		}
		return nil
	}
	return err
}
