package main

import (
	"encoding/json"
	"io"
	"os"
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
