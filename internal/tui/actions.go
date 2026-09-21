package tui

import (
	"bytes"
	"context"
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
	// LookPath finds a client on PATH for the form's client choices, a new
	// profile's default and the retry view's hints. nil means the launcher's
	// runner, which is exec.LookPath unless a test says otherwise.
	LookPath func(string) (string, error)

	// active is the session running now, if any. It is kept here rather
	// than only in the model so that Wicket can stop it on the way out,
	// after Bubble Tea has returned whatever model it last had.
	mu     sync.Mutex
	active *Session
	// base is the parent of every keyring operation's context, cancelled
	// when Wicket exits so that nothing is left waiting on the keyring.
	base     context.Context
	stopBase context.CancelFunc
}

// keyringContext returns a context for one keyring operation, bounded by
// limit and cancelled early if Wicket exits.
func (a *App) keyringContext(limit time.Duration) (context.Context, context.CancelFunc) {
	a.mu.Lock()
	if a.base == nil {
		a.base, a.stopBase = context.WithCancel(context.Background())
	}
	base := a.base
	a.mu.Unlock()
	return context.WithTimeout(base, limit)
}

// CancelKeyring gives up on every keyring operation still running. Wicket
// calls it on the way out.
func (a *App) CancelKeyring() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopBase != nil {
		a.stopBase()
	}
}

// savePlan is a validated save, carried out in steps: the keyring work runs
// off the TUI's update loop, since a keyring can take minutes to answer, and
// the config write runs on it, since the list reads the config as it draws.
type savePlan struct {
	oldName string
	// old is the profile as it was, or nil for a new one.
	old    *config.Profile
	p      config.Profile
	intent PasswordIntent
	oldID  secret.Identity
	newID  secret.Identity
	// moved is set when the save changes the profile's keyring identity: its
	// name, host, user or domain.
	moved bool
	// accountChanged is the part of moved a user may not expect to carry a
	// password: a new host, user or domain rather than just a new name.
	accountChanged bool
}

// carries reports whether the stored password follows the profile to its new
// identity. It does whenever the identity changes and the user neither typed
// a replacement nor asked to forget it: a blank password field keeps what is
// stored, as the form says, even across a new host, user or domain. Dropping
// it there, as Wicket once did, deleted the password without a word.
func (s savePlan) carries() bool { return s.moved && !s.intent.set() && !s.intent.forget() }

// finishes reports whether anything is left for the keyring once the config
// is written: the old entry to remove, or a typed password to store.
func (s savePlan) finishes() bool {
	return s.intent.set() || (s.old != nil && (s.moved || s.intent.forget()))
}

// carry is what carrySecret did before the config moved.
type carry struct {
	// copied is set when the password now also exists under the new
	// identity, and the old entry can go.
	copied bool
	// unknown is set when the keyring could not be read, so Wicket cannot
	// tell whether there was a password to carry. The old entry is then
	// left alone: deleting it would lose a password that was never copied.
	unknown bool
	// attempted is set once the copy has been sent to the keyring, whether
	// or not it was confirmed: a write the caller gave up on may still have
	// landed.
	attempted bool
}

// planSave validates a save without touching the config or the keyring.
func (a *App) planSave(oldName string, newP config.Profile, intent PasswordIntent) (savePlan, error) {
	// Surrounding whitespace is trimmed rather than rejected. A paste was
	// already trimmed on its way into the field, so typing the same trailing
	// space was the only way to see "must not have leading or trailing
	// whitespace" -- the validator still guards a hand-edited config file.
	trim(&newP.Name, &newP.Host, &newP.User, &newP.Domain, &newP.Client, &newP.Size)
	if err := config.ValidateProfile(newP); err != nil {
		return savePlan{}, err
	}
	if intent.set() && intent.Password.Empty() {
		return savePlan{}, &config.FieldError{Field: "password", Msg: "cannot store a blank password"}
	}
	if a.Cfg.NameTaken(newP.Name, oldName) {
		return savePlan{}, &config.FieldError{Field: "name", Msg: "already used"}
	}
	plan := savePlan{oldName: oldName, p: newP, intent: intent, newID: secret.IdentityFor(a.Cfg.Path(), newP)}
	if oldName != "" {
		if p, ok := a.Cfg.Profile(oldName); ok {
			plan.old = &p
			plan.oldID = secret.IdentityFor(a.Cfg.Path(), p)
			plan.accountChanged = p.Host != newP.Host || p.User != newP.User || p.Domain != newP.Domain
			plan.moved = plan.accountChanged || p.Name != newP.Name
		}
	}
	return plan, nil
}

// carrySecret copies the stored password to the profile's new identity before
// the config moves, so there is never a moment when the profile on disk has
// no password to find. The old entry stays until finishSave, after the config
// is written: if anything fails first, the password is still where it was.
func (a *App) carrySecret(ctx context.Context, plan savePlan) (carry, error) {
	res, err := a.Secrets.Lookup(ctx, plan.oldID)
	switch {
	case err == nil:
		pw := res.Password
		defer pw.Clear()
		if err := a.Secrets.Upsert(ctx, plan.newID, pw); err != nil {
			return carry{attempted: true}, err
		}
		return carry{copied: true, attempted: true}, nil
	case errors.Is(err, secret.ErrNotFound):
		return carry{}, nil
	case errors.Is(err, secret.ErrUnavailable):
		// A keyring Wicket cannot reach must not block a save. The profile
		// may have no password at all, and on a machine with no Secret
		// Service there would otherwise be no way to edit anything.
		return carry{unknown: true}, nil
	default:
		return carry{}, err
	}
}

// commitSave writes the config and moves the last-used time with a rename.
func (a *App) commitSave(plan savePlan) (warnings []string, err error) {
	if err := a.Cfg.Upsert(plan.p, plan.oldName); err != nil {
		return nil, err
	}
	if plan.old != nil && plan.old.Name != plan.p.Name && a.State != nil {
		if err := a.State.Rename(plan.old.Name, plan.p.Name); err != nil {
			warnings = append(warnings, "last_used: "+err.Error())
		}
	}
	return warnings, nil
}

// undoCarry removes a copy carrySecret made, when the config write that was
// to follow it failed.
func (a *App) undoCarry(ctx context.Context, plan savePlan) []string {
	if err := a.Secrets.Delete(ctx, plan.newID); err != nil && !errors.Is(err, secret.ErrNotFound) {
		return []string{"a copy of its password was left in the keyring"}
	}
	return nil
}

// finishSave does the keyring work that follows the config write. A typed
// password is stored before the old entry is removed, and the old entry is
// removed only once it has been: a failure between the two leaves a password
// behind rather than none.
func (a *App) finishSave(ctx context.Context, plan savePlan, c carry) (warnings []string) {
	if plan.intent.set() {
		if err := a.Secrets.Upsert(ctx, plan.newID, plan.intent.Password); err != nil {
			warnings = append(warnings, "could not save password: "+keyringProblem(err))
			if plan.old != nil && plan.moved {
				// The old password is the only one there is. It used to be
				// deleted anyway, and a keyring that refused the new one
				// lost both.
				warnings = append(warnings, "the old one was kept under its old details")
			}
			return warnings
		}
	}
	if plan.old == nil || !(plan.moved || plan.intent.forget()) {
		return warnings
	}
	if c.unknown {
		// The password, if there was one, was never copied, so the old
		// entry is all there is.
		return append(warnings, leftoverWarning(secret.ErrUnavailable))
	}
	if err := a.Secrets.Delete(ctx, plan.oldID); err != nil && !errors.Is(err, secret.ErrNotFound) {
		warnings = append(warnings, leftoverWarning(err))
	}
	return warnings
}

// SaveProfile writes newP over the profile called oldName, or adds it when
// oldName is empty, and does what intent says with its stored password. It
// runs the steps the TUI runs one at a time, in the same order.
func (a *App) SaveProfile(ctx context.Context, oldName string, newP config.Profile, intent PasswordIntent) (warnings []string, err error) {
	plan, err := a.planSave(oldName, newP, intent)
	if err != nil {
		return nil, err
	}
	var c carry
	if plan.carries() {
		if c, err = a.carrySecret(ctx, plan); err != nil {
			return nil, carryFailed(err)
		}
	}
	warnings, err = a.commitSave(plan)
	if err != nil {
		if c.copied {
			warnings = append(warnings, a.undoCarry(ctx, plan)...)
		}
		return warnings, err
	}
	return append(warnings, a.finishSave(ctx, plan, c)...), nil
}

// carryFailed explains a save refused because its password could not be
// moved. Nothing was written, and the password is still stored as it was.
func carryFailed(err error) error {
	return fmt.Errorf("could not move the saved password (%s), so nothing was saved; type a new password or switch on forget password to save without it", keyringProblem(err))
}

// removeProfile deletes name from the config and the last-used state, and
// returns the keyring identity its password was stored under, for
// forgetSecret to remove off the update loop.
func (a *App) removeProfile(name string) (id secret.Identity, warnings []string, err error) {
	p, ok := a.Cfg.Profile(name)
	if !ok {
		return id, nil, fmt.Errorf("profile %q not found", name)
	}
	id = secret.IdentityFor(a.Cfg.Path(), p)
	if err := a.Cfg.Remove(name); err != nil {
		return id, nil, err
	}
	if a.State != nil {
		if err := a.State.Forget(name); err != nil {
			warnings = append(warnings, "last_used: "+err.Error())
		}
	}
	return id, warnings, nil
}

// forgetSecret removes a deleted profile's password.
func (a *App) forgetSecret(ctx context.Context, id secret.Identity) []string {
	if err := a.Secrets.Delete(ctx, id); err != nil && !errors.Is(err, secret.ErrNotFound) {
		return []string{leftoverWarning(err)}
	}
	return nil
}

func (a *App) DeleteProfile(ctx context.Context, name string) (warnings []string, err error) {
	id, warnings, err := a.removeProfile(name)
	if err != nil {
		return nil, err
	}
	return append(warnings, a.forgetSecret(ctx, id)...), nil
}

// leftoverWarning describes a secret that could not be removed. A keyring that
// is not there at all is worth saying plainly: the old warning claimed a
// leftover secret for every profile deleted on a machine with no Secret
// Service, including the ones that never had a password.
func leftoverWarning(err error) string {
	if errors.Is(err, secret.ErrUnavailable) {
		return keyringProblem(err) + "; any saved password was left in place"
	}
	return "a leftover secret may remain"
}

// keyringProblem says in a few words why a keyring call failed. The error
// itself can be three lines of D-Bus detail -- a socket path, a method name --
// that the user can do nothing with and that wraps the status line; what they
// can act on is whether the keyring was missing, slow, or refused.
func keyringProblem(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "stopped waiting for the keyring"
	case errors.Is(err, context.DeadlineExceeded):
		return "the keyring did not answer"
	case errors.Is(err, secret.ErrPromptDismissed):
		return "the keyring prompt was dismissed"
	case errors.Is(err, secret.ErrPromptTimeout):
		return "nobody answered the keyring prompt"
	case errors.Is(err, secret.ErrUnavailable):
		return "keyring unavailable"
	case errors.Is(err, secret.ErrNotFound):
		return "no password saved"
	}
	// Anything else is unexpected, and the error is all there is to go on,
	// so it is kept, on one line and briefly.
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return "keyring error: " + truncate(sanitize(msg), 60)
}

type credResult struct {
	Cred      rdp.Credential
	NeedModal bool
	Multiple  bool
	Err       error
}

func (a *App) ResolveCredential(ctx context.Context, p config.Profile, typed *secret.Password) credResult {
	if typed != nil {
		return credResult{Cred: *typed}
	}
	id := secret.IdentityFor(a.Cfg.Path(), p)
	res, err := a.Secrets.Lookup(ctx, id)
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
	// The session is published before anything else can go wrong or wait,
	// so that StopSession finds the client from the moment it exists. The
	// last-used time is not recorded here: RecordUse takes a lock another
	// Wicket may hold, and the TUI calls it off its update loop.
	s := &Session{rdp: rs, output: out}
	a.mu.Lock()
	a.active = s
	a.mu.Unlock()
	return s, nil
}

// RecordUse records that p's client has started, as it always was once it
// had: a client that never ran was not used. It can block for as long as
// another process holds the state lock.
func (a *App) RecordUse(name string) error {
	if a.State == nil {
		return nil
	}
	return a.State.Record(name)
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
	res := ConnectResult{Class: cl, Outcome: out, Output: s.output.Bytes()}
	switch cl {
	case rdp.ClassStartError:
		res.Status = "failed to start"
		res.IsError = true
	case rdp.ClassShortSession:
		res.Status = "session ended quickly"
	case rdp.ClassFailed:
		// Wicket cannot see whether FreeRDP ever connected, only how it
		// exited; a failure is reported as one however long it took.
		res.Status = "FreeRDP exited with an error"
		if r := out.Reason(); r != "" {
			res.Status = "FreeRDP failed: " + r
		}
	default:
		res.Status = "session ended"
		if r := out.Reason(); r != "" {
			res.Status += ": " + r
		}
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
// session failed: the last line naming a FreeRDP error code (ERRCONNECT_ or
// ERRINFO_), failing that the last error, failing that the last line. It is
// cleaned of escape sequences and control characters, since it is client
// output drawn on Wicket's screen. A line that contains the password, which
// a client could only have echoed, is never a candidate. It is looked for
// both before and after cleaning, and cleaned as the line was: a password
// with an escape sequence in it would otherwise be drawn, stripped of it, on
// a line the raw check had passed and the cleaned check could not match.
func clientNote(out []byte, cred rdp.Credential) string {
	pw, _ := cred.(secret.Password)
	var last, lastErr, lastCode string
	for _, ln := range bytes.Split(out, []byte("\n")) {
		raw := string(ln)
		if terminalNoise(raw) {
			continue
		}
		clean := cleanOutput(raw)
		if pw.OccursIn(raw, cleanOutput) || pw.OccursIn(clean, cleanOutput) {
			continue
		}
		s := strings.TrimSpace(logPrefix.ReplaceAllString(strings.TrimSpace(clean), ""))
		if s == "" {
			continue
		}
		last = s
		if strings.Contains(s, "ERRCONNECT_") || strings.Contains(s, "ERRINFO_") {
			lastCode = s
		}
		if strings.Contains(raw, "ERROR") || strings.Contains(s, "ERRCONNECT") {
			lastErr = s
		}
	}
	switch {
	case lastCode != "":
		return lastCode
	case lastErr != "":
		return lastErr
	}
	return last
}

// terminalNoise reports a line FreeRDP logs about the terminal rather than
// the connection. Its password reader tries to switch off echo on stdin, and
// stdin is Wicket's pipe, so every run logs "tcsetattr(TCSANOW) failed with
// Inappropriate ioctl for device" as an ERROR, once more on the way out,
// after the real error: picked as the last error, it stood in for why every
// real connection had failed.
func terminalNoise(line string) bool {
	return strings.Contains(line, "com.freerdp.utils.passphrase") ||
		strings.Contains(line, "tcsetattr") || strings.Contains(line, "tcgetattr")
}

// cleanOutput removes the escape sequences and control characters from a
// line of client output.
func cleanOutput(s string) string {
	return sanitize(ansiSequence.ReplaceAllString(s, ""))
}

// lookPath finds file on PATH the way the launcher would.
func (a *App) lookPath(file string) (string, error) {
	if a.LookPath != nil {
		return a.LookPath(file)
	}
	if a.Launcher != nil && a.Launcher.Runner != nil {
		return a.Launcher.Runner.LookPath(file)
	}
	return rdp.OSRunner{}.LookPath(file)
}

// InstalledClients lists the known FreeRDP clients on PATH, most preferred
// first. It searches PATH, so callers ask once, not on every frame.
func (a *App) InstalledClients() []rdp.KnownClient {
	return rdp.InstalledClients(a.lookPath)
}

// Installed reports whether client can be found on PATH.
func (a *App) Installed(client string) bool {
	_, err := a.lookPath(client)
	return err == nil
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

func (a *App) StoreSecret(ctx context.Context, p config.Profile, pw secret.Password) error {
	return a.Secrets.Upsert(ctx, secret.IdentityFor(a.Cfg.Path(), p), pw)
}
