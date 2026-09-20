package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
	"golang.org/x/term"
)

var (
	openStore    = func() secret.Store { return secret.NewDBus() }
	isTerminal   = func(fd int) bool { return term.IsTerminal(fd) }
	readPassword = func(fd int) ([]byte, error) { return term.ReadPassword(fd) }
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
	p, ok := cfg.Profile(name)
	if !ok {
		fmt.Fprintf(stderr, "unknown profile %q\n", name)
		return 2
	}
	plan, err := rdp.BuildPlan(p)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	launcher := newLauncher(stdout, stderr)
	if _, err := execLookPath(plan.Client); err != nil {
		fmt.Fprintf(stderr, "rdp client %q not found on PATH\n", plan.Client)
		return 2
	}

	store := openStore()
	id := secret.IdentityFor(cfg.Path(), p)
	cred, err := resolveCLICredential(store, id, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
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
	out := sess.Wait()
	if out.StartErr != nil {
		fmt.Fprintln(stderr, out.StartErr)
		return 2
	}
	return out.ExitStatus()
}

func execLookPath(client string) (string, error) {
	return rdp.OSRunner{}.LookPath(client)
}

func resolveCLICredential(store secret.Store, id secret.Identity, stderr io.Writer) (rdp.Credential, error) {
	res, err := store.Lookup(id)
	if err == nil {
		if res.Multiple {
			fmt.Fprintln(stderr, "warning: multiple secrets matched; using the most recently modified")
		}
		return res.Password, nil
	}
	if !errors.Is(err, secret.ErrNotFound) && !errors.Is(err, secret.ErrUnavailable) {
		return nil, err
	}
	in := stdinFile()
	if !isTerminal(int(in.Fd())) {
		return nil, fmt.Errorf("no stored password for this profile; save one from the TUI first")
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
	fmt.Fprint(stderr, "Save password for this profile? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	ans := strings.TrimSpace(strings.ToLower(line))
	if ans == "y" || ans == "yes" {
		if err := store.Upsert(id, pw); err != nil {
			fmt.Fprintln(stderr, "warning: could not save password:", err)
		}
	}
	return pw, nil
}
