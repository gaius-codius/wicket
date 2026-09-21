package tui

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

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

func trim(fields ...*string) {
	for _, f := range fields {
		*f = strings.TrimSpace(*f)
	}
}

// App is the Bubble Tea-free use-case layer.
type App struct {
	Cfg      *config.Config
	Secrets  secret.Store
	State    *config.StateStore
	Launcher *rdp.Launcher
	Clock    rdp.Clock

	// active is the session running now, if any. It is kept here rather
	// than only in the model so that Wicket can stop it on the way out,
	// after Bubble Tea has returned whatever model it last had.
	mu     sync.Mutex
	active *Session
}

func (a *App) SaveProfile(oldName string, newP config.Profile, intent PasswordIntent) (warnings []string, err error) {
	// Surrounding whitespace is trimmed rather than rejected. A paste was
	// already trimmed on its way into the field, so typing the same trailing
	// space was the only way to see "must not have leading or trailing
	// whitespace" -- the validator still guards a hand-edited config file.
	trim(&newP.Name, &newP.Host, &newP.User, &newP.Domain, &newP.Client, &newP.Size)
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

	// A rename leaves the identity otherwise unchanged, so an existing secret
	// is carried to the new name before the config moves and the old entry is
	// deleted below. A typed replacement needs none of that: it is written
	// after the config, like every other save.
	copiedNew := false
	if renamed && !identityChanged && !intent.set() && !intent.forget() {
		res, lerr := a.Secrets.Lookup(oldID)
		switch {
		case lerr == nil:
			if err := a.Secrets.Upsert(newID, res.Password); err != nil {
				return nil, err
			}
			copiedNew = true
		case errors.Is(lerr, secret.ErrNotFound):
			// Nothing to carry over.
		case errors.Is(lerr, secret.ErrUnavailable):
			// A keyring Wicket cannot reach must not block a rename. The
			// profile may have no password at all, and on a machine with no
			// Secret Service there would otherwise be no way to rename
			// anything. The delete below reports what was left behind.
		default:
			return nil, lerr
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
			warnings = append(warnings, leftoverWarning(err))
		}
	}

	if intent.set() {
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
		warnings = append(warnings, leftoverWarning(err))
	}
	return warnings, nil
}

// leftoverWarning describes a secret that could not be removed. A keyring that
// is not there at all is worth saying plainly: the old warning claimed a
// leftover secret for every profile deleted on a machine with no Secret
// Service, including the ones that never had a password.
func leftoverWarning(err error) string {
	if errors.Is(err, secret.ErrUnavailable) {
		return "keyring unavailable; any saved password was left in place"
	}
	return "a leftover secret may remain"
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
	// Output is the end of what the client logged, raw. It is never drawn
	// as it is: clientNote cleans it first.
	Output []byte
}

// sessionWaitDelay bounds how long a session's end waits on output from
// helpers the client left behind; see rdp.Launcher.WaitDelay.
const sessionWaitDelay = 2 * time.Second

// Session is a client started from the TUI and not yet collected.
type Session struct {
	rdp    *rdp.Session
	output *rdp.Tail
	// warning is a problem from the start that did not stop it, reported
	// with the result.
	warning string
}

// Start launches the client for p and returns once it is running, so the
// TUI can draw the session while it lasts. The terminal stays with Wicket:
// the client's output goes to a bounded buffer rather than to a screen
// Bubble Tea is drawing, and its password to a private pipe as always.
func (a *App) Start(p config.Profile, cred rdp.Credential) (*Session, error) {
	plan, err := rdp.BuildPlan(p)
	if err != nil {
		return nil, err
	}
	l := rdp.Launcher{}
	if a.Launcher != nil {
		l = *a.Launcher
	}
	out := rdp.NewTail(rdp.OutputLimit)
	l.Stdout, l.Stderr = out, out
	// The TUI owns Ctrl+C and SIGINT while a session runs, and stops the
	// client through the session itself.
	l.OwnSignals = true
	l.WaitDelay = sessionWaitDelay
	rs, err := l.Start(plan, cred)
	if err != nil {
		return nil, err
	}
	s := &Session{rdp: rs, output: out}
	// The last-used time is recorded once the client has started, as it
	// always was: a client that never ran was not used.
	if a.State != nil {
		if err := a.State.Record(p.Name); err != nil {
			s.warning = "last_used: " + err.Error()
		}
	}
	a.mu.Lock()
	a.active = s
	a.mu.Unlock()
	return s, nil
}

// Wait blocks until s has ended and says how.
func (a *App) Wait(s *Session) ConnectResult {
	out := s.rdp.Wait()
	a.mu.Lock()
	if a.active == s {
		a.active = nil
	}
	a.mu.Unlock()
	cl := rdp.Classify(out)
	res := ConnectResult{Class: cl, Outcome: out, Warning: s.warning, Output: s.output.Bytes()}
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

// startFailed is the result of a client that never ran.
func startFailed(err error) ConnectResult {
	return ConnectResult{Class: rdp.ClassStartError, Status: err.Error(), IsError: true}
}

// StopSession stops a session that is still running and waits for it to
// end, escalating to SIGKILL after grace. Wicket calls it whenever it exits,
// so a client is never left running without the window that started it.
func (a *App) StopSession(grace time.Duration) {
	a.mu.Lock()
	s := a.active
	a.mu.Unlock()
	if s != nil {
		s.rdp.Terminate(grace)
	}
}

var (
	// ansiSequence matches the escape sequences a client may colour its
	// log with.
	ansiSequence = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[@-Z\\-_])`)
	// logPrefix matches the bracketed time, thread, level and function
	// FreeRDP puts in front of every line, up to the " - " before the
	// message, which would otherwise take the room the message needs.
	logPrefix = regexp.MustCompile(`^.*?\]\s*-\s+(\[[^\]]*\]:\s*)?`)
)

// clientNote picks the line of the client's output most likely to say why a
// session failed: the last error, or failing that the last line. It is
// cleaned of escape sequences and control characters, since it is client
// output drawn on Wicket's screen, and dropped altogether if it contains the
// password, which a client could only have echoed.
func clientNote(out []byte, cred rdp.Credential) string {
	var last, lastErr string
	for _, ln := range bytes.Split(out, []byte("\n")) {
		s := sanitize(ansiSequence.ReplaceAllString(string(ln), ""))
		s = strings.TrimSpace(logPrefix.ReplaceAllString(strings.TrimSpace(s), ""))
		if s == "" {
			continue
		}
		last = s
		if strings.Contains(string(ln), "ERROR") || strings.Contains(s, "ERRCONNECT") {
			lastErr = s
		}
	}
	note := last
	if lastErr != "" {
		note = lastErr
	}
	if pw, ok := cred.(secret.Password); ok && pw.OccursIn(note) {
		return ""
	}
	return note
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
