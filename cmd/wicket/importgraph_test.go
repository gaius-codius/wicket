package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestImportGraph(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	cfg := &packages.Config{
		// Tests must be loaded too: without them the Charm ban below would
		// never see a _test.go file, and the whole rule could be bypassed by
		// putting the import in a test.
		Mode:  packages.NeedName | packages.NeedImports | packages.NeedFiles,
		Dir:   root,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "github.com/gaius-codius/wicket/...")
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("packages.Load reported errors")
	}

	internalPrefix := "github.com/gaius-codius/wicket/internal/"
	for _, pkg := range pkgs {
		if pkg.Name == "" {
			continue
		}
		path, isTest, skip := realPkgPath(pkg.ID, pkg.PkgPath)
		if skip {
			continue
		}
		if path == "github.com/gaius-codius/wicket/tools" || strings.HasPrefix(path, "github.com/gaius-codius/wicket/tools/") {
			continue
		}

		imports := make(map[string]bool, len(pkg.Imports))
		for imp := range pkg.Imports {
			imports[imp] = true
		}

		switch {
		case isTest:
			// Layering below governs the shipped binary. A test may reach for
			// another internal package (a fake, a fixture) without breaking it.
		case path == internalPrefix+"config" || strings.HasPrefix(path, internalPrefix+"config/"):
			for imp := range imports {
				if strings.HasPrefix(imp, internalPrefix) {
					t.Errorf("internal/config imports %s; must import no other internal package", imp)
				}
			}
		case path == internalPrefix+"theme" || strings.HasPrefix(path, internalPrefix+"theme/"):
			for imp := range imports {
				if strings.HasPrefix(imp, internalPrefix) {
					t.Errorf("internal/theme imports %s; must import no other internal package", imp)
				}
				if isLipgloss(imp) {
					t.Errorf("internal/theme imports Lipgloss (%s)", imp)
				}
			}
		case path == internalPrefix+"importer" || strings.HasPrefix(path, internalPrefix+"importer/"):
			for imp := range imports {
				switch {
				case imp == internalPrefix+"config" || strings.HasPrefix(imp, internalPrefix+"config/"):
					// parsers may use config.Profile and defaults only
				case strings.HasPrefix(imp, internalPrefix):
					t.Errorf("internal/importer imports %s; may import only config among internal packages", imp)
				case isCharm(imp):
					t.Errorf("internal/importer imports a Charm library (%s)", imp)
				}
			}
		case path == internalPrefix+"rdp" || strings.HasPrefix(path, internalPrefix+"rdp/"):
			for imp := range imports {
				switch {
				case imp == internalPrefix+"secret" || strings.HasPrefix(imp, internalPrefix+"secret/"):
					t.Errorf("internal/rdp imports secret (%s)", imp)
				case imp == internalPrefix+"theme" || strings.HasPrefix(imp, internalPrefix+"theme/"):
					t.Errorf("internal/rdp imports theme (%s)", imp)
				case imp == internalPrefix+"tui" || strings.HasPrefix(imp, internalPrefix+"tui/"):
					t.Errorf("internal/rdp imports tui (%s)", imp)
				case isCharm(imp):
					t.Errorf("internal/rdp imports a Charm library (%s)", imp)
				}
			}
		}

		// The Charm ban holds for tests as well, so a stray import in a
		// _test.go file cannot quietly widen the dependency.
		for imp := range imports {
			if !isCharm(imp) {
				continue
			}
			if !strings.HasPrefix(path, internalPrefix+"tui") {
				t.Errorf("%s imports %s; Bubble Tea, Bubbles and Lipgloss may appear only under internal/tui", pkg.PkgPath, imp)
			}
		}

		for _, file := range pkg.GoFiles {
			base := filepath.Base(file)
			if base != "actions.go" && base != "actions_test.go" {
				continue
			}
			if !strings.Contains(filepath.ToSlash(file), "/tui/") {
				continue
			}
			assertNoCharmImports(t, file)
		}
	}

	// The two files that must stay Charm-free even inside internal/tui are also
	// checked straight off disk, so the rule holds even if the loader stops
	// reporting one of them.
	tuiDir := filepath.Join(root, "internal", "tui")
	for _, name := range []string{"actions.go", "actions_test.go"} {
		assertNoCharmImports(t, filepath.Join(tuiDir, name))
	}
}

func assertNoCharmImports(t *testing.T, path string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, spec := range f.Imports {
		imp := strings.Trim(spec.Path.Value, `"`)
		if isCharm(imp) {
			t.Errorf("%s must not import Bubble Tea, Bubbles, or Lipgloss: %s", filepath.Base(path), imp)
		}
	}
}

// realPkgPath maps a package loaded with Tests set back to the package it
// belongs to. An in-package test variant keeps the original PkgPath and is
// only distinguishable by its ID ("p [p.test]"); an external test package is
// "p_test"; and each test binary adds a synthetic "p.test" main to skip.
func realPkgPath(id, path string) (pkg string, isTest bool, skip bool) {
	if strings.HasSuffix(path, ".test") {
		return "", false, true
	}
	isTest = strings.Contains(id, ".test]")
	if strings.HasSuffix(path, "_test") {
		path, isTest = strings.TrimSuffix(path, "_test"), true
	}
	return path, isTest, false
}

// isCharm reports whether path is any Charm TUI library. Bubbles counts: it is
// a Bubble Tea component library, and omitting it left a hole in this rule.
func isCharm(path string) bool {
	return isBubbleTea(path) || isLipgloss(path) || isBubbles(path)
}

func isBubbles(path string) bool {
	return strings.HasPrefix(path, "charm.land/bubbles") || strings.HasPrefix(path, "github.com/charmbracelet/bubbles")
}

func isBubbleTea(path string) bool {
	return strings.HasPrefix(path, "charm.land/bubbletea") || strings.HasPrefix(path, "github.com/charmbracelet/bubbletea")
}

func isLipgloss(path string) bool {
	return strings.HasPrefix(path, "charm.land/lipgloss") || strings.HasPrefix(path, "github.com/charmbracelet/lipgloss")
}

func TestImportGraphModuleLoads(t *testing.T) {
	// Ensure the tools file is not treated as missing by the loader.
	if _, err := os.Stat(filepath.Join(moduleRoot(t), "tools", "tools.go")); err != nil {
		t.Fatal(err)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}
