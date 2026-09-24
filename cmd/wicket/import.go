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
	for _, a := range args {
		switch {
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
	skipped = append(skipped, parseSkipped...)

	if dryRun {
		existing, werr := existingProfiles(paths.Config, stderr)
		if werr != nil {
			fmt.Fprintln(stderr, werr)
			return 2
		}
		imported, skipped = planImport(existing, profiles, rename, skipped)
	} else {
		cfg, err := config.OpenOrCreate(paths.Config)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		for _, w := range cfg.Warnings() {
			fmt.Fprintln(stderr, "warning:", w)
		}
		added, addSkipped, err := cfg.AddProfiles(profiles, rename)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		imported = added
		for _, s := range addSkipped {
			skipped = append(skipped, importer.Skip{Name: s.Name, Reason: s.Reason})
		}
	}
	importer.WriteSummary(stdout, imported, skipped)
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

// planImport mirrors AddProfiles collision and validation rules without writing.
func planImport(existing []config.Profile, profiles []config.Profile, rename bool, skipped []importer.Skip) ([]config.Profile, []importer.Skip) {
	taken := make(map[string]bool, len(existing)+len(profiles))
	for _, p := range existing {
		taken[p.Name] = true
	}
	var imported []config.Profile
	for _, p := range profiles {
		if err := config.ValidateProfileInUse(p); err != nil {
			name := p.Name
			if name == "" {
				name = p.Host
			}
			if name == "" {
				name = "(unnamed)"
			}
			skipped = append(skipped, importer.Skip{Name: name, Reason: err.Error()})
			continue
		}
		name := p.Name
		if taken[name] {
			if !rename {
				skipped = append(skipped, importer.Skip{Name: name, Reason: "name already used"})
				continue
			}
			name = nextImportName(name, taken)
			p.Name = name
		}
		taken[name] = true
		imported = append(imported, p)
	}
	return imported, skipped
}

func nextImportName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s-%d", name, n)
		if !taken[cand] {
			return cand
		}
	}
}
