package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

type record struct {
	Argv    []string          `json:"argv"`
	Environ map[string]string `json:"environ"`
	Stdin   string            `json:"stdin"`
}

func main() {
	if os.Getenv("FAKERDP_ROLE") == "helper" {
		helper()
		return
	}
	if path := os.Getenv("FAKERDP_PID"); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	rec := record{
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

	delay := os.Getenv("FAKERDP_DELAY_STDIN")
	if delay != "" {
		if d, err := time.ParseDuration(delay); err == nil {
			time.Sleep(d)
		}
	}
	in, _ := io.ReadAll(os.Stdin)
	rec.Stdin = string(in)

	if path := os.Getenv("FAKERDP_RECORD"); path != "" {
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(path, b, 0o600)
	}

	// FAKERDP_OUTPUT is logged to stdout and stderr, the way FreeRDP logs.
	if s := os.Getenv("FAKERDP_OUTPUT"); s != "" {
		fmt.Fprintln(os.Stdout, "stdout: "+s)
		fmt.Fprintln(os.Stderr, "stderr: "+s)
	}
	// FAKERDP_ECHO_STDIN plays a client that repeats what it was given.
	if os.Getenv("FAKERDP_ECHO_STDIN") != "" {
		fmt.Fprint(os.Stdout, rec.Stdin)
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
	cmd := exec.Command(self)
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
	defer f.Close()
	fmt.Fprintf(f, "%s:%s\n", role, sig)
}
