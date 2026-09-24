package main

import (
	"errors"
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

With no command, wicket starts the TUI. Press ? there for its keys; s sorts
the list by most recent use.

Commands:
  connect <profile>   Connect to a named profile without the TUI
                      (requires a stored password when stdin is not a terminal)
  import remmina|rdp  Import RDP connections from Remmina or .rdp files
                      (import only; no export)

Options:
  -h, --help          Show this help
  -v, --version       Show the version

Environment:
  WICKET_CONFIG       Config file path
  WICKET_STATE        State file path
  WICKET_THEME        TUI theme: auto, wicket, wicket-dark, wicket-light,
                      omarchy or terminal (overrides [ui] theme)
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
		Store: secret.NewDBus(),
		// The TUI sends the client's output to a buffer of its own: the
		// terminal is Bubble Tea's for as long as a session runs.
		Launcher: &rdp.Launcher{},
		// The TUI asks the terminal for its background colour only when
		// there is a terminal to answer.
		StdoutIsTerminal: func() bool { return isTerminal(int(os.Stdout.Fd())) },
	})
}

func run(args []string, stdout, stderr io.Writer, runTUI func() error) int {
	if len(args) == 0 {
		if err := runTUI(); err != nil {
			// SIGTERM or SIGHUP ended the TUI: exit as wicket connect does,
			// without calling it an error.
			var stopped *tui.StoppedError
			if errors.As(err, &stopped) {
				return stopped.ExitStatus()
			}
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
	case "import":
		return runImport(args[1:], stdout, stderr)
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(stderr, "unknown flag: %s\n", args[0])
		} else {
			fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		}
		return 2
	}
}
