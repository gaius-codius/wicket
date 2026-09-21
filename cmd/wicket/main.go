package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/gaius-codius/wicket/internal/rdp"
	"github.com/gaius-codius/wicket/internal/secret"
	"github.com/gaius-codius/wicket/internal/tui"
)

const helpText = `Usage: wicket [command]

Wicket is a terminal UI for saved FreeRDP connections.

With no command, wicket starts the TUI.

Commands:
  connect <profile>   Connect to a named profile without the TUI
                      (requires a stored password when stdin is not a terminal)

Options:
  -h, --help          Show this help
  -v, --version       Show the version
`

// version is set at build time with -ldflags "-X main.version=0.1.0". A build
// installed with "go install" has no ldflags, so the module version recorded
// in the binary is used instead; only a build straight from a working tree
// falls through to "dev".
var (
	version = "dev"
	commit  = ""
)

func versionString() string {
	v := version
	if v == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
	}
	v = strings.TrimPrefix(v, "v")
	if commit == "" {
		return "wicket " + v
	}
	return "wicket " + v + " (" + commit + ")"
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, startTUI))
}

func startTUI() error {
	return tui.Run(tui.Options{
		Store:    secret.NewDBus(),
		Launcher: &rdp.Launcher{Stdout: os.Stdout, Stderr: os.Stderr},
	})
}

func run(args []string, stdout, stderr io.Writer, runTUI func() error) int {
	if len(args) == 0 {
		if err := runTUI(); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, helpText)
		return 0
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, versionString())
		return 0
	case "connect":
		return runConnect(args[1:], stdout, stderr)
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(stderr, "unknown flag: %s\n", args[0])
		} else {
			fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		}
		return 2
	}
}
