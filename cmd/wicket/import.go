package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"

	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/importer"
	"github.com/gaius-codius/wicket/internal/rdp"
)

const importUsage = `usage: wicket import remmina [DIR]
       wicket import rdp FILE...

Import RDP connections into wicket profiles (no export).

Flags:
  --dry-run   parse and report without writing
  --rename    on name collision, append -2, -3… instead of skipping
  --          end of flags; later arguments are paths even if they start with -

Exits 1 when entries were found but none was imported.
`

func runImport(args []string, stdout, stderr io.Writer) int {
	dryRun, rename, rest, err := parseImportFlags(args)
	if err != nil {
		if errors.Is(err, errImportHelp) {
			fmt.Fprint(stdout, importUsage)
			return 0
		}
		fmt.Fprintln(stderr, err)
		fmt.Fprint(stderr, importUsage)
		return 2
	}
	if len(rest) == 0 {
		fmt.Fprint(stderr, importUsage)
		return 2
	}
	switch rest[0] {
	case "remmina":
		dir := importer.DefaultRemminaDir()
		if len(rest) > 1 {
			dir = rest[1]
		}
		if len(rest) > 2 {
			fmt.Fprintln(stderr, "usage: wicket import remmina [DIR]")
			return 2
		}
		if dir == "" {
			fmt.Fprintln(stderr, "cannot resolve Remmina directory: no home")
			return 2
		}
		return doImport(stdout, stderr, dryRun, rename, func() ([]config.Profile, []importer.Skip, error) {
			return importer.ParseRemminaDir(dir)
		})
	case "rdp":
		files := rest[1:]
		if len(files) == 0 {
			fmt.Fprintln(stderr, "usage: wicket import rdp FILE...")
			return 2
		}
		return doImport(stdout, stderr, dryRun, rename, func() ([]config.Profile, []importer.Skip, error) {
			return importer.ParseRDPFiles(files)
		})
	default:
		fmt.Fprintf(stderr, "unknown import source %q\n", rest[0])
		fmt.Fprint(stderr, importUsage)
		return 2
	}
}

var errImportHelp = fmt.Errorf("help")

func parseImportFlags(args []string) (dryRun, rename bool, rest []string, err error) {
	for i, a := range args {
		switch {
		case a == "--":
			return dryRun, rename, append(rest, args[i+1:]...), nil
		case a == "--dry-run":
			dryRun = true
		case a == "--rename":
			rename = true
		case a == "-h" || a == "--help":
			return false, false, nil, errImportHelp
		case strings.HasPrefix(a, "-"):
			return false, false, nil, fmt.Errorf("unknown flag: %s", a)
		default:
			rest = append(rest, a)
		}
	}
	return dryRun, rename, rest, nil
}

func doImport(stdout, stderr io.Writer, dryRun, rename bool, parse func() ([]config.Profile, []importer.Skip, error)) int {
	profiles, parseSkipped, err := parse()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	client := rdp.PreferredClient(rdp.InstalledClients(exec.LookPath))
	for i := range profiles {
		profiles[i].Client = client
	}

	paths, err := config.ResolveFromEnv()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	var (
		imported []config.Profile
		skipped  []importer.Skip
	)
	if dryRun {
		existing, err := existingProfiles(paths.Config, stderr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		imported, skipped = config.PlanProfiles(existing, profiles, rename)
	} else {
		cfg, err := config.OpenOrCreate(paths.Config)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		for _, w := range cfg.Warnings() {
			fmt.Fprintln(stderr, "warning:", w)
		}
		imported, skipped, err = cfg.AddProfiles(profiles, rename)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	skipped = append(parseSkipped, skipped...)
	importer.WriteSummary(stdout, imported, skipped)
	if len(imported) == 0 && len(skipped) > 0 {
		return 1
	}
	return 0
}

func existingProfiles(path string, stderr io.Writer) ([]config.Profile, error) {
	cfg, err := config.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, w := range cfg.Warnings() {
		fmt.Fprintln(stderr, "warning:", w)
	}
	return cfg.Profiles(), nil
}
