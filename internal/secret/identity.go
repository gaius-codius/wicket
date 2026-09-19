package secret

import "github.com/gaius-codius/wicket/internal/config"

const serviceName = "wicket"

// Identity is the libsecret attribute set (REQ-025).
type Identity struct {
	Service string
	Config  string
	Profile string
	Host    string
	User    string
	Domain  string
}

func IdentityFor(configPath string, p config.Profile) Identity {
	return Identity{
		Service: serviceName,
		Config:  configPath,
		Profile: p.Name,
		Host:    p.Host,
		User:    p.User,
		Domain:  p.Domain,
	}
}

func (id Identity) Attrs() map[string]string {
	return map[string]string{
		"service": id.Service,
		"config":  id.Config,
		"profile": id.Profile,
		"host":    id.Host,
		"user":    id.User,
		"domain":  id.Domain,
	}
}

func (id Identity) Label() string { return serviceName }
