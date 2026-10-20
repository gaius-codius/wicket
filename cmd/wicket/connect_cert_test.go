package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/testutil"
)

// The fake client plays FreeRDP meeting a server certificate it has not been
// told to trust (FAKERDP_CERT): it checks a store under XDG_CONFIG_HOME and
// asks on stdin about anything the store does not hold. These tests prove
// wicket connect's part: on its own terminal the client is given it to ask
// on, and anywhere else the refusal is explained.

const (
	cliOldCert     = "old-certificate"
	cliNewCert     = "new-certificate"
	cliTrustPrompt = "Do you trust the above certificate? (Y/T/N)"
)

// x11TOML is validTOML with FreeRDP's X11 client, the one whose TLS failure
// may be a certificate it could not ask about; the SDL client asks in a
// window of its own.
func x11TOML() string {
	return strings.Replace(validTOML(), `"sdl-freerdp3"`, `"xfreerdp3"`, 1)
}

// certConnectEnv points wicket at a config holding body, a state file and a
// certificate store under home.
func certConnectEnv(t *testing.T, body, home string) {
	t.Helper()
	t.Setenv("WICKET_CONFIG", writeConnectConfig(t, body))
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "state.toml"))
	t.Setenv("XDG_CONFIG_HOME", home)
}

// certConnectSetup writes a config with the profile "work" (client sdl
// unless body says otherwise) on host h, a keyring holding its password, and
// a certificate store under a fresh XDG_CONFIG_HOME whose entry for h holds
// saved ("" for none). It returns the entry's path.
func certConnectSetup(t *testing.T, body, saved string) string {
	t.Helper()
	home := t.TempDir()
	certConnectEnv(t, body, home)
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	entry := filepath.Join(home, "freerdp", "server", "h_3389.pem")
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		t.Fatal(err)
	}
	if saved != "" {
		if err := os.WriteFile(entry, []byte(saved), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKERDP_CERT", cliNewCert)
	return entry
}

// useStoredPassword makes the keyring hold pw for the profile "work".
func useStoredPassword(t *testing.T, pw string) {
	t.Helper()
	cfg, err := config.Open(os.Getenv("WICKET_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cfg.Profile("work")
	mem := secret.NewMemory()
	sp, err := secret.NewPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Upsert(bg, secret.IdentityFor(cfg.Path(), p), sp); err != nil {
		t.Fatal(err)
	}
	old := openStore
	openStore = func() secret.Store { return mem }
	t.Cleanup(func() { openStore = old })
}

func readCert(t *testing.T, entry string) string {
	t.Helper()
	b, err := os.ReadFile(entry)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Off a terminal the client cannot ask, so it refuses the certificate, and
// wicket connect says what may have happened and where it can be settled.
// The store is left alone.
func TestConnect_CertificateRefusedOffTerminal(t *testing.T) {
	entry := certConnectSetup(t, x11TOML(), cliOldCert)
	useStoredPassword(t, "pw")
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 143 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	for _, want := range []string{"TLS connection failed", "certificate is new or has changed", "wicket connect work", "in the foreground of a terminal", "with its output on the terminal"} {
		if !strings.Contains(got.stderr, want) {
			t.Fatalf("stderr %q, want it to say %q", got.stderr, want)
		}
	}
	if c := readCert(t, entry); c != cliOldCert {
		t.Fatalf("store entry %q, want it unchanged", c)
	}
}

// The SDL client asks in a window of its own, so its 143 says nothing about
// stdin or a terminal: no hint.
func TestConnect_SDLClientNoCertificateHint(t *testing.T) {
	certConnectSetup(t, validTOML(), cliOldCert)
	useStoredPassword(t, "pw")
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 143 || strings.Contains(got.stderr, "certificate") || strings.Contains(got.stderr, "TLS") {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
}

// Another failure says nothing about certificates.
func TestConnect_OtherFailureNoCertificateHint(t *testing.T) {
	certConnectSetup(t, x11TOML(), cliNewCert) // the certificate is trusted
	useStoredPassword(t, "pw")
	t.Setenv("FAKERDP_EXIT", "131")
	got := runCLI(t, []string{"connect", "work"})
	if got.code != 131 || strings.Contains(got.stderr, "certificate") {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
}

// The helper side of the terminal tests: wicket connect run in a session of
// its own, whose controlling terminal is the pty on its stdin. The parent
// has put the fake client on PATH and set FAKERDP_*; the helper makes the
// config, state and keyring. When WICKET_TEST_CONNECT_STDOUT names a file,
// that file is wicket's output instead of the terminal.
func TestConnectTerminalHelper(t *testing.T) {
	home := os.Getenv("WICKET_TEST_CONNECT_TERMINAL")
	if home == "" {
		t.Skip("run by TestConnect_CertificateOnTerminal")
	}
	certConnectEnv(t, x11TOML(), home)
	useStoredPassword(t, "pw-terminal")
	stdout := os.Stdout
	if path := os.Getenv("WICKET_TEST_CONNECT_STDOUT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		stdout = f
	}
	code := run([]string{"connect", "work"}, stdout, os.Stderr, func() error { return nil })
	fmt.Printf("\nRESULT exit=%d\n", code)
}

// On its own terminal, wicket connect hands it to the client, which shows
// the certificate and asks: y trusts it and the client saves it for this
// server, and the session goes ahead; n refuses it and the old entry stays.
// Nothing is said about certificates that the client did not already ask.
func TestConnect_CertificateOnTerminal(t *testing.T) {
	for _, tc := range []struct {
		answer string
		code   int
		after  string
	}{
		{"y", 0, cliNewCert},
		{"n", 143, cliOldCert},
	} {
		t.Run(tc.answer, func(t *testing.T) {
			home := t.TempDir()
			entry := filepath.Join(home, "freerdp", "server", "h_3389.pem")
			if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(entry, []byte(cliOldCert), 0o600); err != nil {
				t.Fatal(err)
			}
			rec := filepath.Join(t.TempDir(), "rec.json")
			t.Setenv("FAKERDP_RECORD", rec)
			t.Setenv("FAKERDP_CERT", cliNewCert)
			testutil.PrependPATH(t, testutil.FakeRDPDir(t))
			code, out := connectOnTerminal(t, home, tc.answer)
			if code != tc.code {
				t.Fatalf("exit %d, want %d; output:\n%s", code, tc.code, out)
			}
			if strings.Contains(out, "certificate is new or has changed") {
				t.Fatalf("explained a refusal the client asked about:\n%s", out)
			}
			if c := readCert(t, entry); c != tc.after {
				t.Fatalf("store entry %q, want %q", c, tc.after)
			}
			r := testutil.ReadRecord(t, rec)
			if !r.StdinTTY || !r.Foreground || r.CertAnswer != tc.answer {
				t.Fatalf("client tty %v, foreground %v, answer %q", r.StdinTTY, r.Foreground, r.CertAnswer)
			}
			if !r.HasPassword || r.Password != "pw-terminal" {
				t.Fatalf("password %q (present %v)", r.Password, r.HasPassword)
			}
		})
	}
}

// With its output redirected away from the terminal, wicket connect does not
// hand the terminal over: a question written to a file would leave the user
// facing a terminal waiting for an answer to nothing. The client refuses the
// certificate without asking, and the hint goes to stderr, on the terminal.
func TestConnect_OutputRedirectedKeepsTheTerminal(t *testing.T) {
	home := t.TempDir()
	entry := filepath.Join(home, "freerdp", "server", "h_3389.pem")
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(cliOldCert), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)
	t.Setenv("FAKERDP_CERT", cliNewCert)
	t.Setenv("WICKET_TEST_CONNECT_STDOUT", filepath.Join(t.TempDir(), "out.txt"))
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	code, out := connectOnTerminal(t, home, "y")
	if code != 143 {
		t.Fatalf("exit %d, want 143; output:\n%s", code, out)
	}
	if strings.Contains(out, cliTrustPrompt) {
		t.Fatalf("the client asked on the terminal:\n%s", out)
	}
	for _, want := range []string{"TLS connection failed", "wicket connect work", "foreground of a terminal"} {
		if !strings.Contains(out, want) {
			t.Fatalf("terminal output lacks %q:\n%s", want, out)
		}
	}
	r := testutil.ReadRecord(t, rec)
	if r.StdinTTY || r.CertAnswer != "EOF" {
		t.Fatalf("client tty %v, answer %q: it must not have had the terminal", r.StdinTTY, r.CertAnswer)
	}
	if c := readCert(t, entry); c != cliOldCert {
		t.Fatalf("store entry %q, want it unchanged", c)
	}
}

// connectOnTerminal runs TestConnectTerminalHelper on a fresh pty, answers
// the client's question, and returns wicket connect's exit code and all it
// and the client wrote.
func connectOnTerminal(t *testing.T, home, answer string) (int, string) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConnectTerminalHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "WICKET_TEST_CONNECT_TERMINAL="+home)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var out strings.Builder
	var line []byte
	rd := bufio.NewReader(master)
	for {
		b, err := rd.ReadByte()
		if err != nil {
			break
		}
		out.WriteByte(b)
		line = append(line, b)
		if strings.HasSuffix(string(line), cliTrustPrompt) {
			if _, err := master.Write([]byte(answer + "\n")); err != nil {
				t.Fatal(err)
			}
		}
		if b == '\n' {
			line = line[:0]
		}
	}
	_ = cmd.Wait()
	code := -1
	for ln := range strings.SplitSeq(out.String(), "\n") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(ln), "RESULT exit="); ok {
			_, _ = fmt.Sscanf(s, "%d", &code)
		}
	}
	if code < 0 {
		t.Fatalf("no result from the helper; it wrote:\n%s", out.String())
	}
	return code, out.String()
}
