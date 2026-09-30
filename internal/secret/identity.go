package secret

import "github.com/gaius-codius/wicket/internal/config"

const serviceName = "wicket"

// Identity is the libsecret attribute set (REQ-025).
//
// Current identities search by service, config path and profile_id (issue
// #24). Display name, host, user and domain are not search attrs: they
// change, and including them let a recreated copy name inherit an orphaned
// secret.
//
// When ProfileID is empty and the Legacy fields are set, Attrs returns the
// pre-UUID attribute set so Lookup/Delete can still find items written
// before the migration.
type Identity struct {
	Service   string
	Config    string
	ProfileID string

	// Legacy fields are set only for LegacyIdentity searches.
	legacy                      bool
	Profile, Host, User, Domain string
}

// IdentityFor builds the keyring identity for p. p.ID must be non-empty
// (call config.EnsureID first); an empty ProfileID matches nothing useful.
func IdentityFor(configPath string, p config.Profile) Identity {
	return Identity{
		Service:   serviceName,
		Config:    configPath,
		ProfileID: p.ID,
	}
}

// LegacyIdentity builds the pre-UUID identity used to find items that still
// carry profile/host/user/domain attributes.
func LegacyIdentity(configPath string, p config.Profile) Identity {
	return Identity{
		Service: serviceName,
		Config:  configPath,
		legacy:  true,
		Profile: p.Name,
		Host:    p.Host,
		User:    p.User,
		Domain:  p.Domain,
	}
}

// Attrs are the libsecret search attributes.
func (id Identity) Attrs() map[string]string {
	if id.legacy || id.ProfileID == "" && id.Profile != "" {
		return map[string]string{
			"service": id.Service,
			"config":  id.Config,
			"profile": id.Profile,
			"host":    id.Host,
			"user":    id.User,
			"domain":  id.Domain,
		}
	}
	return map[string]string{
		"service":    id.Service,
		"config":     id.Config,
		"profile_id": id.ProfileID,
	}
}

// Label is a human-facing keyring item label. It deliberately omits the
// profile display name so a rename does not imply a different keyring item.
func (id Identity) Label() string { return serviceName }
