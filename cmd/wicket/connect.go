package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
)

var (
	openStore     = secret.Default
	lookupProfile = func(cfg *config.Config, name string) (config.Profile, bool) {
		return cfg.Profile(name)
	}
	isTerminal   = term.IsTerminal
	readPassword = term.ReadPassword
	stdinFile    = func() *os.File { return os.Stdin }
	newLauncher  = func(stdout, stderr io.Writer) *rdp.Launcher {
		return &rdp.Launcher{Stdout: stdout, Stderr: stderr}
	}
)

func runConnect(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: wicket connect <profile>")
		return 2
	}
	name := args[0]
	paths, err := config.ResolveFromEnv()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cfg, err := config.Open(paths.Config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	for _, w := range cfg.Warnings() {
		fmt.Fprintln(stderr, "warning:", w)
	}
	p, ok := lookupProfile(cfg, name)
	if !ok {
		fmt.Fprintf(stderr, "unknown profile %q\n", name)
		return 2
	}
	if p.User == "" {
		// An import can leave a profile with no user. Say so in Wicket's
		// words, rather than BuildPlan's "user: must not be empty", and
		// never hand FreeRDP an empty /u: to sign in as nobody.
		fmt.Fprintf(stderr, "profile %q has no user: open it in wicket and add one before connecting\n", name)
		return 2
	}
	plan, err := rdp.BuildPlan(p)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	launcher := newLauncher(stdout, stderr)
	// The same lookup Start makes, so a test's runner decides both.
	if _, err := launcher.LookupClient(plan.Client); err != nil {
		fmt.Fprintf(stderr, "rdp client %q not found on PATH\n", plan.Client)
		return 2
	}

	store := openStore()
	if p.ID == "" {
		// Open backfills missing ids; an empty id here means the loaded
		// profile is corrupt or a test injected one without an id.
		fmt.Fprintf(stderr, "profile %q has no id after load; config may be corrupt\n", name)
		return 2
	}
	cred, err := resolveCLICredential(store, cfg, p, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	// On a terminal Wicket owns, the client gets it, so FreeRDP can show a
	// server certificate it has not been told to trust, new or changed, and
	// ask the user about it. Elsewhere it cannot ask, and refuses one.
	if in := stdinFile(); rdp.OwnsTerminal(in) {
		launcher.Terminal = in
	}
	sess, err := launcher.Start(plan, cred)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	st, sterr := config.OpenState(paths.State, nil)
	if sterr != nil {
		fmt.Fprintln(stderr, "warning: last_used:", sterr)
	} else if err := st.Record(name); err != nil {
		fmt.Fprintln(stderr, "warning: last_used:", err)
	}
	// A SIGTERM or SIGHUP from here on stops the client's whole process
	// group and ends Wait; see rdp.Launcher.OwnSignals.
	out := sess.Wait()
	if out.StartErr != nil {
		fmt.Fprintln(stderr, out.StartErr)
		return 2
	}
	if out.MaybeCertificate() && launcher.Terminal == nil {
		fmt.Fprintf(stderr, "%s: TLS connection failed. If the server's certificate is new or has changed, run `wicket connect %s` in a terminal: FreeRDP shows the certificate there and asks before trusting it.\n", plan.Client, name)
	}
	return out.ExitStatus()
}

// resolveCLICredential finds the password for id, asking on a terminal when
// the keyring has none or cannot be reached; only a missing password offers
// to save the typed one. Each keyring call is bounded by secret.OpTimeout,
// which leaves room to answer an unlock prompt; Ctrl+C or SIGTERM end the
// wait sooner, since nothing is running yet that could be left behind.
func resolveCLICredential(store secret.Store, cfg *config.Config, p config.Profile, stderr io.Writer) (rdp.Credential, error) {
	id := secret.IdentityFor(cfg.Path(), p)
	ctx, cancel := context.WithTimeout(context.Background(), secret.OpTimeout)
	res, err := store.Lookup(ctx, id)
	cancel()
	if err == nil {
		if res.Multiple {
			fmt.Fprintln(stderr, "warning: multiple secrets matched; using the most recently modified")
		}
		return res.Password, nil
	}
	// A saved password that cannot be used was read fine: it is neither
	// missing nor behind an unreachable keyring, and a prompt would hide
	// what is wrong with it.
	if errors.Is(err, secret.ErrTooLong) || errors.Is(err, secret.ErrCRLF) {
		return nil, fmt.Errorf("the saved password cannot be used (%w); save a new one from the TUI", err)
	}
	// An unreachable keyring is not a missing password: telling the user to
	// save one from the TUI sends them somewhere that cannot save it either.
	unavailable := errors.Is(err, secret.ErrUnavailable)
	if !unavailable && !errors.Is(err, secret.ErrNotFound) {
		return nil, err
	}
	in := stdinFile()
	if !isTerminal(int(in.Fd())) {
		if unavailable {
			return nil, fmt.Errorf("cannot read the password from the keyring: %w", err)
		}
		return nil, fmt.Errorf("no stored password for this profile; save one from the TUI first")
	}
	switch {
	case errors.Is(err, secret.ErrLocked):
		// A password is stored, behind a keyring the user would not unlock.
		// Saving the typed one would meet the same lock.
		fmt.Fprintf(stderr, "warning: %v; the password will not be saved\n", err)
	case unavailable:
		fmt.Fprintf(stderr, "warning: cannot reach the keyring (%v); the password will not be saved\n", err)
	}
	fmt.Fprint(stderr, "Password: ")
	b, err := readPassword(int(in.Fd()))
	// The terminal hands back a mutable buffer. The string inside Password
	// cannot be wiped, but this copy can, so do not leave a second one around.
	defer clear(b)
	fmt.Fprintln(stderr)
	if err != nil {
		return nil, err
	}
	pw, err := secret.NewPassword(string(b))
	if err != nil {
		return nil, err
	}
	if pw.Empty() {
		return nil, fmt.Errorf("password required")
	}
	if unavailable {
		return pw, nil
	}
	fmt.Fprint(stderr, "Save password for this profile? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	ans := strings.TrimSpace(strings.ToLower(line))
	if ans == "y" || ans == "yes" {
		ctx, cancel := context.WithTimeout(context.Background(), secret.OpTimeout)
		defer cancel()
		if err := store.Upsert(ctx, id, pw); err != nil {
			fmt.Fprintln(stderr, "warning: could not save password:", saveProblem(err))
		}
	}
	return pw, nil
}

// saveProblem says why a save failed when the user answered, or ignored, a
// keyring prompt. The error itself goes on to say "secret service
// unavailable", which is not what happened: the keyring was there and stayed
// locked. Any other failure is shown as it is.
func saveProblem(err error) string {
	dismissed := errors.Is(err, secret.ErrPromptDismissed)
	timedOut := errors.Is(err, secret.ErrPromptTimeout)
	switch {
	case errors.Is(err, secret.ErrLocked) && dismissed:
		return "the keyring is locked; its unlock prompt was dismissed"
	case errors.Is(err, secret.ErrLocked) && timedOut:
		return "the keyring is locked; nobody answered its unlock prompt"
	case errors.Is(err, secret.ErrLocked):
		return "the keyring is locked"
	case dismissed:
		return "the keyring prompt was dismissed"
	case timedOut:
		return "nobody answered the keyring prompt"
	}
	return err.Error()
}
