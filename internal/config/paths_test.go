package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve_DefaultPaths(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cwd := t.TempDir()
	getenv := func(string) string { return "" }
	p, err := Resolve(getenv, home, cwd)
	if err != nil {
		t.Fatal(err)
	}
	wantCfg := filepath.Join(home, ".config", "wicket", "config.toml")
	wantSt := filepath.Join(home, ".local", "state", "wicket", "state.toml")
	if p.Config != wantCfg {
		t.Fatalf("config = %q, want %q", p.Config, wantCfg)
	}
	if p.State != wantSt {
		t.Fatalf("state = %q, want %q", p.State, wantSt)
	}
}

func TestResolve_XDG(t *testing.T) {
	t.Parallel()
	xdgCfg := t.TempDir()
	xdgState := t.TempDir()
	getenv := func(k string) string {
		switch k {
		case envXDGConfig:
			return xdgCfg
		case envXDGState:
			return xdgState
		default:
			return ""
		}
	}
	p, err := Resolve(getenv, "/unused-home", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.Config != filepath.Join(xdgCfg, "wicket", "config.toml") {
		t.Fatalf("config = %q", p.Config)
	}
	if p.State != filepath.Join(xdgState, "wicket", "state.toml") {
		t.Fatalf("state = %q", p.State)
	}
}

func TestResolve_WicketConfigOverrideRelative(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	getenv := func(k string) string {
		if k == envConfig {
			return "rel/config.toml"
		}
		return ""
	}
	p, err := Resolve(getenv, t.TempDir(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.Join(cwd, "rel", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Config != want {
		t.Fatalf("config = %q, want %q", p.Config, want)
	}
}

func TestCanonical_Symlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(real, "config.toml")
	if err := os.WriteFile(target, []byte("[general]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(dir, "link")
	if err := os.Symlink(real, linkDir); err != nil {
		t.Fatal(err)
	}
	got, err := Canonical(filepath.Join(linkDir, "config.toml"), dir)
	if err != nil {
		t.Fatal(err)
	}
	eval, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != eval {
		t.Fatalf("canonical = %q, want %q", got, eval)
	}
}

func TestCanonical_MissingPathNoEval(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := filepath.Join(dir, "nope", "config.toml")
	got, err := Canonical(raw, dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolve_WicketConfigIsolation(t *testing.T) {
	t.Setenv("WICKET_CONFIG", filepath.Join(t.TempDir(), "isolated.toml"))
	t.Setenv("WICKET_STATE", filepath.Join(t.TempDir(), "isolated-state.toml"))
	p, err := ResolveFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if p.Config != os.Getenv("WICKET_CONFIG") && filepath.Base(p.Config) != "isolated.toml" {
		t.Fatalf("override not applied: %+v", p)
	}
	if filepath.Base(p.Config) != "isolated.toml" {
		t.Fatalf("config base = %q", filepath.Base(p.Config))
	}
	if filepath.Base(p.State) != "isolated-state.toml" {
		t.Fatalf("state base = %q", filepath.Base(p.State))
	}
}
