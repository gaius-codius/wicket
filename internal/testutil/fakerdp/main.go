package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// record is what the client was given. It mirrors testutil.FakeRecord.
type record struct {
	// RawArgv is the command line as the process table shows it.
	RawArgv []string `json:"raw_argv"`
	// Argv is the arguments as FreeRDP would read them: the command line,
	// or, under /args-from:fd:3, the program name and the lines read from
	// fd 3, less the /p: line.
	Argv        []string `json:"argv"`
	Password    string   `json:"password"`
	HasPassword bool     `json:"has_password"`
	// ArgsLines is every line read from fd 3, the /p: line included and in
	// the order they came.
	ArgsLines []string          `json:"args_lines"`
	Environ   map[string]string `json:"environ"`
	Stdin     string            `json:"stdin"`
	// StdinTTY says stdin was a terminal, and Foreground that the client's
	// process group was the terminal's foreground group.
	StdinTTY   bool `json:"stdin_tty"`
	Foreground bool `json:"foreground"`
	// CertAnswer is what the client read when it asked about a
	// certificate, or "" when it did not ask.
	CertAnswer string `json:"cert_answer"`
}

// argsFromFD is the argument under which FreeRDP reads the rest from fd 3.
const argsFromFD = "/args-from:fd:3"

func main() {
	if os.Getenv("FAKERDP_ROLE") == "helper" {
		helper()
		return
	}
	if path := os.Getenv("FAKERDP_PID"); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	rec := record{
		RawArgv: os.Args,
		Argv:    os.Args,
		Environ: map[string]string{},
	}
	for _, e := range os.Environ() {
		k, v, ok := splitEnv(e)
		if !ok {
			continue
		}
		rec.Environ[k] = v
	}

	// FAKERDP_DELAY_ARGS plays a client slow to read its arguments.
	if d, err := time.ParseDuration(os.Getenv("FAKERDP_DELAY_ARGS")); err == nil {
		time.Sleep(d)
	}
	if len(os.Args) == 2 && os.Args[1] == argsFromFD {
		readArgs(&rec)
	}
	rec.StdinTTY, rec.Foreground = terminalState()
	// FAKERDP_CERT plays FreeRDP meeting a server certificate it has not
	// been told to trust: see certPrompt. It reads its answer from stdin, so
	// stdin is read whole only otherwise.
	if presented := os.Getenv("FAKERDP_CERT"); presented != "" {
		if !certPrompt(&rec, presented) {
			writeRecord(rec)
			os.Exit(exitTLSConnectFailed)
		}
	} else if !rec.StdinTTY {
		in, _ := io.ReadAll(os.Stdin)
		rec.Stdin = string(in)
	}

	writeRecord(rec)

	// FAKERDP_OUTPUT is logged to stdout and stderr, the way FreeRDP logs.
	if s := os.Getenv("FAKERDP_OUTPUT"); s != "" {
		fmt.Fprintln(os.Stdout, "stdout: "+s)
		fmt.Fprintln(os.Stderr, "stderr: "+s)
	}
	// FAKERDP_ECHO_PASSWORD plays a client that repeats the password it
	// was given.
	if os.Getenv("FAKERDP_ECHO_PASSWORD") != "" {
		fmt.Fprintln(os.Stdout, rec.Password)
	}
	// FAKERDP_SPAWN starts a helper in the client's process group, the
	// way a client can leave something running behind it.
	if os.Getenv("FAKERDP_SPAWN") != "" {
		spawnHelper()
	}
	// FAKERDP_TRAP plays a client that ignores SIGINT: each signal is
	// logged to the file it names, and only SIGTERM ends the client.
	if path := os.Getenv("FAKERDP_TRAP"); path != "" {
		trap(path, "client")
	}

	if hold := os.Getenv("FAKERDP_HOLD_FILE"); hold != "" {
		for {
			if _, err := os.Stat(hold); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	if os.Getenv("FAKERDP_SIGINT_HOLD") != "" {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		os.Exit(0)
	}

	if d := os.Getenv("FAKERDP_SLEEP"); d != "" {
		if dur, err := time.ParseDuration(d); err == nil {
			time.Sleep(dur)
		}
	}

	code := 0
	if s := os.Getenv("FAKERDP_EXIT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			code = n
		}
	}
	os.Exit(code)
}

func writeRecord(rec record) {
	if path := os.Getenv("FAKERDP_RECORD"); path != "" {
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(path, b, 0o600)
	}
}

// readArgs reads the arguments from fd 3 as FreeRDP does: a line each,
// until the pipe closes or a line is empty.
func readArgs(rec *record) {
	f := os.NewFile(3, "args")
	if f == nil {
		os.Exit(exitInvalidArguments)
	}
	b, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		os.Exit(exitInvalidArguments)
	}
	rec.Argv = []string{os.Args[0]}
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break
		}
		rec.ArgsLines = append(rec.ArgsLines, line)
		if pw, ok := strings.CutPrefix(line, "/p:"); ok {
			rec.Password, rec.HasPassword = pw, true
			continue
		}
		rec.Argv = append(rec.Argv, line)
	}
}

// terminalState says whether stdin is a terminal, and whether this process's
// group is its foreground group.
func terminalState() (tty, foreground bool) {
	group, err := unix.IoctlGetInt(0, unix.TIOCGPGRP)
	if err != nil {
		return false, false
	}
	return true, group == unix.Getpgrp()
}

const (
	exitInvalidArguments = 128
	exitTLSConnectFailed = 143
)

// certPrompt plays FreeRDP's check of the server certificate presented, the
// text FAKERDP_CERT holds, against its store: the file named for the host
// and port under $XDG_CONFIG_HOME/freerdp/server. A certificate the store
// already holds is trusted. Any other is asked about on stdin, as FreeRDP's
// command-line client does: y trusts it and saves it, t trusts it for this
// run only, and anything else, end of input included, refuses it. A save
// that fails refuses it too, as FreeRDP does. It reports whether the
// certificate was trusted.
func certPrompt(rec *record, presented string) bool {
	entry := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "freerdp", "server", storeName(rec.Argv))
	if saved, err := os.ReadFile(entry); err == nil && string(saved) == presented {
		return true
	}
	fmt.Fprintf(os.Stdout, "Certificate details for %s:\n\tFingerprint: %s\n", entry, presented)
	fmt.Fprint(os.Stdout, "Do you trust the above certificate? (Y/T/N) ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	rec.CertAnswer = strings.TrimSpace(line)
	if rec.CertAnswer == "" {
		rec.CertAnswer = "EOF"
	}
	switch strings.ToLower(rec.CertAnswer) {
	case "y":
		if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
			return false
		}
		return os.WriteFile(entry, []byte(presented), 0o600) == nil
	case "t":
		return true
	}
	return false
}

// storeName is FreeRDP's file name for the /v: host's entry: host_port.pem,
// lowercased, the port 3389 unless the host names one.
func storeName(argv []string) string {
	host := ""
	for _, a := range argv {
		if v, ok := strings.CutPrefix(a, "/v:"); ok {
			host = v
		}
	}
	port := "3389"
	if h, p, ok := strings.Cut(host, ":"); ok {
		host, port = h, p
	}
	return strings.ToLower(host) + "_" + port + ".pem"
}

func splitEnv(e string) (string, string, bool) {
	for i := 0; i < len(e); i++ {
		if e[i] == '=' {
			return e[:i], e[i+1:], true
		}
	}
	return "", "", false
}

func spawnHelper() {
	self, err := os.Executable()
	if err != nil {
		os.Exit(3)
	}
	cmd := exec.CommandContext(context.Background(), self)
	cmd.Env = append(os.Environ(), "FAKERDP_ROLE=helper")
	// The helper shares the client's output, as a real one would, so it
	// can hold the pipe open after the client has gone.
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	// FAKERDP_SPAWN_DETACHED puts the helper in a process group of its own,
	// out of reach of anything aimed at the client's group.
	if os.Getenv("FAKERDP_SPAWN_DETACHED") != "" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		os.Exit(3)
	}
	// Wait for the helper to be listening, so a signal sent to the group
	// as soon as this client is running reaches a handler.
	path := os.Getenv("FAKERDP_HELPER_PID")
	for i := 0; path != "" && i < 250; i++ {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// helper stays in the client's process group, ignores SIGINT and exits on
// SIGTERM, logging each to FAKERDP_TRAP when it is set.
func helper() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	if path := os.Getenv("FAKERDP_HELPER_PID"); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	log := os.Getenv("FAKERDP_TRAP")
	for sig := range ch {
		logSignal(log, "helper", sig)
		if sig == syscall.SIGTERM && os.Getenv("FAKERDP_STUBBORN") == "" {
			os.Exit(0)
		}
	}
}

func trap(path, role string) {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	if ready := os.Getenv("FAKERDP_TRAP_READY"); ready != "" {
		_ = os.WriteFile(ready, []byte("x"), 0o600)
	}
	for sig := range ch {
		logSignal(path, role, sig)
		// FAKERDP_STUBBORN ignores SIGTERM too, leaving only SIGKILL.
		if sig == syscall.SIGTERM && os.Getenv("FAKERDP_STUBBORN") == "" {
			os.Exit(0)
		}
	}
}

func logSignal(path, role string, sig os.Signal) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	fmt.Fprintf(f, "%s:%s\n", role, sig)
}
