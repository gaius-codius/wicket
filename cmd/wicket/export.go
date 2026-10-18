package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/exporter"
)

const exportUsage = `usage: wicket export rdp PROFILE... [--out DIR] [--force]
       wicket export rdp --all [--out DIR] [--force]

Write profiles to .rdp files that Windows Remote Desktop and FreeRDP open,
one per profile, named after it. Passwords are never written.

Flags:
  --all       export every profile
  --out DIR   write into DIR, made if missing (default: the current folder)
  --force     replace files that already exist; without it they are kept
  --          end of flags; later arguments are profile names

Exits 1 when a profile was not written.
`

type exportFlags struct {
	all, force bool
	out        string
}

var errExportHelp = errors.New("help")

func parseExportFlags(args []string) (flags exportFlags, rest []string, err error) {
	flags.out = "."
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return flags, append(rest, args[i+1:]...), nil
		case a == "--all":
			flags.all = true
		case a == "--force":
			flags.force = true
		case a == "--out":
			// A folder that starts with '-' is given as --out=DIR, so a
			// forgotten folder does not swallow the next flag.
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "-") {
				return flags, nil, errors.New("--out needs a folder")
			}
			i++
			flags.out = args[i]
		case strings.HasPrefix(a, "--out="):
			flags.out = strings.TrimPrefix(a, "--out=")
		case a == "-h" || a == "--help":
			return flags, nil, errExportHelp
		case strings.HasPrefix(a, "-"):
			return flags, nil, fmt.Errorf("unknown flag: %s", a)
		default:
			rest = append(rest, a)
		}
	}
	if flags.out == "" {
		return flags, nil, errors.New("--out needs a folder")
	}
	return flags, rest, nil
}

func runExport(args []string, stdout, stderr io.Writer) int {
	flags, rest, err := parseExportFlags(args)
	if err != nil {
		if errors.Is(err, errExportHelp) {
			fmt.Fprint(stdout, exportUsage)
			return 0
		}
		fmt.Fprintln(stderr, err)
		fmt.Fprint(stderr, exportUsage)
		return 2
	}
	if len(rest) == 0 {
		fmt.Fprint(stderr, exportUsage)
		return 2
	}
	if rest[0] != "rdp" {
		fmt.Fprintf(stderr, "unknown export format %q\n", rest[0])
		fmt.Fprint(stderr, exportUsage)
		return 2
	}
	names := rest[1:]
	if flags.all == (len(names) > 0) {
		fmt.Fprintln(stderr, "name the profiles to export, or give --all")
		fmt.Fprint(stderr, exportUsage)
		return 2
	}

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
	profiles, ok := exportProfiles(cfg, names, flags.all, stderr)
	if !ok {
		return 2
	}
	if len(profiles) == 0 {
		fmt.Fprintln(stderr, "no profiles to export")
		return 1
	}
	if err := os.MkdirAll(flags.out, 0o700); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	failed := 0
	taken := map[string]bool{}
	for _, p := range profiles {
		text, omitted := exporter.RDP(p)
		path := filepath.Join(flags.out, exporter.Unique(p.Name, taken))
		if err := exporter.WriteFile(path, exporter.Encode(text), flags.force); err != nil {
			failed++
			if errors.Is(err, fs.ErrExist) {
				fmt.Fprintf(stderr, "kept %s: it exists already (--force replaces it)\n", path)
			} else {
				fmt.Fprintf(stderr, "profile %q not written: %v\n", p.Name, err)
			}
			continue
		}
		fmt.Fprintf(stdout, "wrote %s (profile %q)\n", path, p.Name)
		for _, o := range omitted {
			fmt.Fprintf(stdout, "  left out %s: %s\n", o.Setting, o.Reason)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// exportProfiles is the profiles to export: all of them, or the ones names
// lists, each once, in the order given. An unknown name stops the export
// before any file is written, so a typo does not leave half of it done.
func exportProfiles(cfg *config.Config, names []string, all bool, stderr io.Writer) ([]config.Profile, bool) {
	if all {
		return cfg.Profiles(), true
	}
	var (
		profiles []config.Profile
		seen     = map[string]bool{}
		ok       = true
	)
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		p, found := cfg.Profile(name)
		if !found {
			fmt.Fprintf(stderr, "unknown profile %q\n", name)
			ok = false
			continue
		}
		profiles = append(profiles, p)
	}
	return profiles, ok
}
