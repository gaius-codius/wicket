package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
)

// PasswordAction says what SaveProfile should do with the profile's stored
// password. The three outcomes are exclusive by construction: "replace it" and
// "delete it" cannot both be asked for.
type PasswordAction int

const (
	// PasswordKeep leaves the keyring untouched, beyond following a rename.
	PasswordKeep PasswordAction = iota
	// PasswordSet replaces the stored password with Password.
	PasswordSet
	// PasswordForget deletes the stored password.
	PasswordForget
)

type PasswordIntent struct {
	Action   PasswordAction
	Password secret.Password
}

func (i PasswordIntent) set() bool    { return i.Action == PasswordSet }
func (i PasswordIntent) forget() bool { return i.Action == PasswordForget }

type TerminalController interface {
	Release() error
	Restore() error
}

type nopTerm struct{}

func (nopTerm) Release() error { return nil }
func (nopTerm) Restore() error { return nil }

// App is the Bubble Tea-free use-case layer.
type App struct {
	Cfg      *config.Config
	Secrets  secret.Store
	State    *config.StateStore
	Launcher *rdp.Launcher
	Term     TerminalController
	Clock    rdp.Clock
}

func (a *App) term() TerminalController {
	if a.Term != nil {
		return a.Term
	}
	return nopTerm{}
}

func (a *App) SaveProfile(oldName string, newP config.Profile, intent PasswordIntent) (warnings []string, err error) {
	newP.Size = strings.TrimSpace(newP.Size)
	if err := config.ValidateProfile(newP); err != nil {
		return nil, err
	}
	if intent.set() && intent.Password.Empty() {
		return nil, &config.FieldError{Field: "password", Msg: "cannot store a blank password"}
	}
	if a.Cfg.NameTaken(newP.Name, oldName) {
		return nil, &config.FieldError{Field: "name", Msg: "already used"}
	}
	var old *config.Profile
	if oldName != "" {
		if p, ok := a.Cfg.Profile(oldName); ok {
			cp := p
			old = &cp
		}
	}
	renamed := old != nil && old.Name != newP.Name
	identityChanged := old != nil && (old.Host != newP.Host || old.User != newP.User || old.Domain != newP.Domain)
	var oldID secret.Identity
	if old != nil {
		oldID = secret.IdentityFor(a.Cfg.Path(), *old)
	}
	newID := secret.IdentityFor(a.Cfg.Path(), newP)

	copiedNew := false
	if renamed && !identityChanged {
		if intent.set() {
			if err := a.Secrets.Upsert(newID, intent.Password); err != nil {
				return nil, err
			}
			copiedNew = true
		} else if !intent.forget() {
			res, lerr := a.Secrets.Lookup(oldID)
			if lerr == nil {
				if err := a.Secrets.Upsert(newID, res.Password); err != nil {
					return nil, err
				}
				copiedNew = true
			} else if !errors.Is(lerr, secret.ErrNotFound) {
				return nil, lerr
			}
		}
	}

	if err := a.Cfg.Upsert(newP, oldName); err != nil {
		if copiedNew {
			if derr := a.Secrets.Delete(newID); derr != nil && !errors.Is(derr, secret.ErrNotFound) {
				warnings = append(warnings, "orphan secret at "+newP.Name)
			}
		}
		return warnings, err
	}

	if renamed && a.State != nil {
		if err := a.State.Rename(old.Name, newP.Name); err != nil {
			warnings = append(warnings, "last_used: "+err.Error())
		}
	}

	if old != nil && (renamed || identityChanged || intent.forget()) {
		if err := a.Secrets.Delete(oldID); err != nil && !errors.Is(err, secret.ErrNotFound) {
			warnings = append(warnings, "a leftover secret may remain")
		}
	}

	if intent.set() && !copiedNew {
		if err := a.Secrets.Upsert(newID, intent.Password); err != nil {
			warnings = append(warnings, "could not save password: "+err.Error())
		}
	}
	return warnings, nil
}

func (a *App) DeleteProfile(name string) (warnings []string, err error) {
	p, ok := a.Cfg.Profile(name)
	if !ok {
		return nil, fmt.Errorf("profile %q not found", name)
	}
	id := secret.IdentityFor(a.Cfg.Path(), p)
	if err := a.Cfg.Remove(name); err != nil {
		return nil, err
	}
	if a.State != nil {
		if err := a.State.Forget(name); err != nil {
			warnings = append(warnings, "last_used: "+err.Error())
		}
	}
	if err := a.Secrets.Delete(id); err != nil && !errors.Is(err, secret.ErrNotFound) {
		warnings = append(warnings, "a leftover secret may remain")
	}
	return warnings, nil
}

type credResult struct {
	Cred      rdp.Credential
	NeedModal bool
	Multiple  bool
	Err       error
}

func (a *App) ResolveCredential(p config.Profile, typed *secret.Password) credResult {
	if typed != nil {
		return credResult{Cred: *typed}
	}
	id := secret.IdentityFor(a.Cfg.Path(), p)
	res, err := a.Secrets.Lookup(id)
	if err == nil {
		return credResult{Cred: res.Password, Multiple: res.Multiple}
	}
	if errors.Is(err, secret.ErrNotFound) {
		return credResult{NeedModal: true}
	}
	return credResult{NeedModal: true, Err: err}
}

type ConnectResult struct {
	Class   rdp.Class
	Outcome rdp.Outcome
	Warning string
	Status  string
	IsError bool
}

func (a *App) Connect(p config.Profile, cred rdp.Credential) ConnectResult {
	if err := a.term().Release(); err != nil {
		return ConnectResult{Class: rdp.ClassStartError, Status: err.Error(), IsError: true}
	}
	defer func() { _ = a.term().Restore() }()
	plan, err := rdp.BuildPlan(p)
	if err != nil {
		return ConnectResult{Class: rdp.ClassStartError, Status: err.Error(), IsError: true}
	}
	if a.Launcher == nil {
		a.Launcher = &rdp.Launcher{}
	}
	sess, err := a.Launcher.Start(plan, cred)
	if err != nil {
		msg := err.Error()
		return ConnectResult{Class: rdp.ClassStartError, Status: msg, IsError: true}
	}
	var warn string
	if a.State != nil {
		if err := a.State.Record(p.Name); err != nil {
			warn = "last_used: " + err.Error()
		}
	}
	out := sess.Wait()
	cl := rdp.Classify(out)
	res := ConnectResult{Class: cl, Outcome: out, Warning: warn}
	switch cl {
	case rdp.ClassStartError:
		res.Status = "failed to start"
		res.IsError = true
	case rdp.ClassShortSession:
		res.Status = "session ended quickly"
	default:
		res.Status = "session ended"
	}
	return res
}

// ProbeClient reports a missing or illegal client basename before any password prompt.
func (a *App) ProbeClient(p config.Profile) error {
	plan, err := rdp.BuildPlan(p)
	if err != nil {
		return err
	}
	l := a.Launcher
	if l == nil {
		l = &rdp.Launcher{}
	}
	runner := l.Runner
	if runner == nil {
		runner = rdp.OSRunner{}
	}
	if _, err := runner.LookPath(plan.Client); err != nil {
		return fmt.Errorf("%w: %s", rdp.ErrClientNotFound, plan.Client)
	}
	return nil
}

func (a *App) StoreSecret(p config.Profile, pw secret.Password) error {
	return a.Secrets.Upsert(secret.IdentityFor(a.Cfg.Path(), p), pw)
}
