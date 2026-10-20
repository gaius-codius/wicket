package rdp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/gaius-codius/wicket/internal/testutil"
)

// The certificate tests play FreeRDP's check of a server certificate with
// the fake client (FAKERDP_CERT): an entry in a store under the test's own
// XDG_CONFIG_HOME, and a question on stdin about any certificate the store
// does not hold. What they prove is Wicket's part: the client is given the
// terminal to ask on, its answer reaches it, Wicket never touches the store,
// and the terminal comes back afterwards.

const (
	oldCert = "old-certificate"
	newCert = "new-certificate"
	// trustPrompt is the fake client's question, FreeRDP's own words.
	trustPrompt = "Do you trust the above certificate? (Y/T/N)"
)

// certStore makes an empty store for host h under a fresh XDG_CONFIG_HOME,
// and returns the config home and the path of h's entry.
func certStore(t *testing.T) (home, entry string) {
	t.Helper()
	home = t.TempDir()
	dir := filepath.Join(home, "freerdp", "server")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(dir, "h_3389.pem")
}

func writeEntry(t *testing.T, entry, cert string) {
	t.Helper()
	if err := os.WriteFile(entry, []byte(cert), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readEntry(t *testing.T, entry string) string {
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

// A client started without a terminal cannot be asked, so a certificate the
// store does not hold is refused, as FreeRDP refuses it, and the store is
// left as it was.
func TestStart_WithoutTerminalCertificateIsRefused(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	home, entry := certStore(t)
	writeEntry(t, entry, oldCert)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("FAKERDP_CERT", newCert)
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)

	l := &Launcher{Stdout: io.Discard, Stderr: io.Discard, OwnSignals: true}
	sess, err := l.Start(testPlan(t), lineCred("pw"))
	if err != nil {
		t.Fatal(err)
	}
	o := sess.Wait()
	if o.ExitCode != 143 || o.Reason() != "TLS connection failed" {
		t.Fatalf("outcome %+v, reason %q", o, o.Reason())
	}
	r := testutil.ReadRecord(t, rec)
	if r.StdinTTY || r.CertAnswer != "EOF" {
		t.Fatalf("stdin tty %v, answer %q: the client should have read end of input", r.StdinTTY, r.CertAnswer)
	}
	if got := readEntry(t, entry); got != oldCert {
		t.Fatalf("store entry %q, want it unchanged", got)
	}
}

// The helper side of the terminal tests: run as a child of the test, in a
// session of its own whose controlling terminal is the pty on its stdin. It
// starts the fake client with that terminal, and reports how the client
// exited and whether the terminal came back.
func TestTerminalHelper(t *testing.T) {
	home := os.Getenv("WICKET_TEST_TERMINAL_HELPER")
	if home == "" {
		t.Skip("run by the terminal tests")
	}
	// The sandbox gave this process a config home of its own; the store is
	// the parent test's.
	t.Setenv("XDG_CONFIG_HOME", home)
	if !OwnsTerminal(os.Stdin) {
		t.Fatal("the helper does not own its terminal")
	}
	l := &Launcher{Terminal: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, OwnSignals: true}
	sess, err := l.Start(testPlan(t), lineCred("pw-terminal"))
	if err != nil {
		t.Fatal(err)
	}
	o := sess.Wait()
	group, err := foregroundGroup(os.Stdin)
	fmt.Printf("\nRESULT exit=%d restored=%v\n", o.ExitCode, err == nil && group == unix.Getpgrp())
}

// runOnTerminal runs TestTerminalHelper on a fresh pty, answers the client's
// question with answer when it asks, and returns the helper's exit code for
// the client, whether the terminal came back, and whether it was asked.
func runOnTerminal(t *testing.T, home, answer string) (code int, restored, asked bool) {
	t.Helper()
	return runOnTerminalWith(t, home, answer, false, 20*time.Second)
}

// runOnTerminalWith is runOnTerminal, with ctrlZ sending the terminal's
// suspend character before the answer, and a limit on the whole run: a
// client that stopped would otherwise hold the pty open for ever.
func runOnTerminalWith(t *testing.T, home, answer string, ctrlZ bool, limit time.Duration) (code int, restored, asked bool) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	t.Cleanup(cancel)
	// Closing the master ends the read below, and hangs up a client left
	// stopped on the terminal.
	go func() { <-ctx.Done(); _ = master.Close() }()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTerminalHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "WICKET_TEST_TERMINAL_HELPER="+home)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	// Ctty is the child's own descriptor for its stdin.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()

	var out strings.Builder
	result := ""
	rd := bufio.NewReader(master)
	var line []byte
	for {
		b, err := rd.ReadByte()
		if err != nil {
			// The pty reports EIO once the helper and the client have gone.
			break
		}
		out.WriteByte(b)
		line = append(line, b)
		if strings.HasSuffix(string(line), trustPrompt) {
			asked = true
			if ctrlZ {
				if _, err := master.Write([]byte{0x1a}); err != nil {
					t.Fatal(err)
				}
				time.Sleep(200 * time.Millisecond)
			}
			if _, err := master.Write([]byte(answer + "\n")); err != nil {
				t.Fatal(err)
			}
		}
		if b == '\n' {
			if s := strings.TrimSpace(string(line)); strings.HasPrefix(s, "RESULT ") {
				result = s
			}
			line = line[:0]
		}
	}
	_ = cmd.Wait()
	if _, err := fmt.Sscanf(result, "RESULT exit=%d restored=%t", &code, &restored); err != nil {
		t.Fatalf("no result from the helper (%v); it wrote:\n%s", err, out.String())
	}
	return code, restored, asked
}

// With the terminal, the client asks about a certificate the store does not
// hold, and the user's answer decides: y trusts it and the client saves it
// for this server alone; n refuses it and leaves the old entry; a store that
// cannot be written refuses it too, with the old entry intact. Whatever the
// answer, Wicket's own group has the terminal back once the client is done.
func TestStart_TerminalCertificatePrompt(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	for _, tc := range []struct {
		name     string
		saved    string // the entry before; "" for none
		answer   string
		readOnly bool
		code     int
		after    string
	}{
		{name: "changed, accepted", saved: oldCert, answer: "y", code: 0, after: newCert},
		{name: "changed, cancelled", saved: oldCert, answer: "n", code: 143, after: oldCert},
		{name: "first seen, accepted", answer: "y", code: 0, after: newCert},
		{name: "first seen, cancelled", answer: "n", code: 143, after: ""},
		// The store cases check the fake's FreeRDP contract; what Wicket
		// owes is the terminal side: the client owns it, the answer reaches
		// it, and the terminal comes back.
		{name: "changed, store not writable", saved: oldCert, answer: "y", readOnly: true, code: 143, after: oldCert},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.readOnly && os.Geteuid() == 0 {
				t.Skip("root ignores file modes")
			}
			home, entry := certStore(t)
			// Another server's entry, which no answer may touch.
			other := filepath.Join(filepath.Dir(entry), "other_3389.pem")
			writeEntry(t, other, oldCert)
			if tc.saved != "" {
				writeEntry(t, entry, tc.saved)
			}
			if tc.readOnly {
				if err := os.Chmod(entry, 0o400); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("FAKERDP_CERT", newCert)
			rec := filepath.Join(t.TempDir(), "rec.json")
			t.Setenv("FAKERDP_RECORD", rec)

			code, restored, asked := runOnTerminal(t, home, tc.answer)
			if !asked || code != tc.code || !restored {
				t.Fatalf("asked %v, client exit %d (want %d), terminal given back %v", asked, code, tc.code, restored)
			}
			if got := readEntry(t, entry); got != tc.after {
				t.Fatalf("store entry %q, want %q", got, tc.after)
			}
			if got := readEntry(t, other); got != oldCert {
				t.Fatalf("another server's entry became %q", got)
			}
			checkTerminalClient(t, testutil.ReadRecord(t, rec), tc.answer)
		})
	}
}

// checkTerminalClient checks that the client owned the terminal it asked on,
// read answer from it, and had its password from the arguments pipe.
func checkTerminalClient(t *testing.T, r testutil.FakeRecord, answer string) {
	t.Helper()
	if !r.StdinTTY || !r.Foreground {
		t.Fatalf("client stdin tty %v, foreground %v: it must own the terminal to ask", r.StdinTTY, r.Foreground)
	}
	if !strings.EqualFold(r.CertAnswer, answer) {
		t.Fatalf("client read %q, want %q", r.CertAnswer, answer)
	}
	if !r.HasPassword || r.Password != "pw-terminal" {
		t.Fatalf("password %q (%v), want it on the arguments pipe", r.Password, r.HasPassword)
	}
}

// A certificate the store already holds is trusted without a question.
func TestStart_TerminalKnownCertificateIsNotAsked(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	home, entry := certStore(t)
	writeEntry(t, entry, newCert)
	t.Setenv("FAKERDP_CERT", newCert)
	code, restored, asked := runOnTerminal(t, home, "n")
	if asked || code != 0 || !restored {
		t.Fatalf("asked %v, exit %d, restored %v", asked, code, restored)
	}
}

// A file that is not a terminal Wicket owns, such as the null device or a
// pipe, cannot be handed to the client.
func TestOwnsTerminal_NotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if OwnsTerminal(r) {
		t.Fatal("a pipe is not a terminal")
	}
	_, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer func() { _ = slave.Close() }()
	// A terminal, but not this process's controlling one.
	if OwnsTerminal(slave) {
		t.Fatal("a terminal Wicket does not control is not its own")
	}
	l := &Launcher{Terminal: slave, Stdout: io.Discard, Stderr: io.Discard, OwnSignals: true}
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	if _, err := l.Start(testPlan(t), lineCred("pw")); err == nil {
		t.Fatal("started the client on a terminal Wicket cannot hand over")
	}
}

// Ctrl+Z at the certificate question does not stop the client: it is started
// with SIGTSTP ignored, as Wicket is waiting on it and nothing would resume
// it. The answer after it still reaches the client, which finishes.
func TestStart_TerminalCtrlZDoesNotStopTheClient(t *testing.T) {
	testutil.PrependPATH(t, testutil.FakeRDPDir(t))
	home, entry := certStore(t)
	writeEntry(t, entry, oldCert)
	t.Setenv("FAKERDP_CERT", newCert)
	rec := filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("FAKERDP_RECORD", rec)

	code, restored, asked := runOnTerminalWith(t, home, "y", true, 10*time.Second)
	if !asked || code != 0 || !restored {
		t.Fatalf("asked %v, client exit %d, terminal given back %v: the client did not finish after Ctrl+Z", asked, code, restored)
	}
	if got := readEntry(t, entry); got != newCert {
		t.Fatalf("store entry %q, want %q", got, newCert)
	}
}

// Only FreeRDP's X11 clients, which ask on stdin, end with the TLS failure
// code over a certificate they could not ask about.
func TestOutcome_MaybeCertificate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		client string
		code   int
		want   bool
	}{
		{"xfreerdp3", 143, true},
		{"xfreerdp", 143, true},
		{"/usr/bin/xfreerdp3", 143, true},
		{"sdl-freerdp3", 143, false},
		{"sdl-freerdp", 143, false},
		{"myrdp", 143, false},
		{"xfreerdp3", 131, false},
		{"xfreerdp3", 0, false},
	} {
		if got := (Outcome{Client: tc.client, ExitCode: tc.code}).MaybeCertificate(); got != tc.want {
			t.Errorf("%s exit %d: MaybeCertificate = %v, want %v", tc.client, tc.code, got, tc.want)
		}
	}
}
