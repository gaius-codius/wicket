package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	appName      = "wicket"
	configFile   = "config.toml"
	stateFile    = "state.toml"
	envConfig    = "WICKET_CONFIG"
	envState     = "WICKET_STATE"
	envXDGConfig = "XDG_CONFIG_HOME"
	envXDGState  = "XDG_STATE_HOME"
)

// Paths holds canonical config and state file locations (REQ-003, REQ-018, REQ-025).
type Paths struct {
	Config string
	State  string
}

// ResolveFromEnv uses the process environment, home directory, and working directory.
func ResolveFromEnv() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("home directory: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Paths{}, fmt.Errorf("working directory: %w", err)
	}
	return Resolve(os.Getenv, home, cwd)
}

// Resolve computes canonical config and state paths.
// Relative WICKET_CONFIG / WICKET_STATE values are resolved against cwd.
func Resolve(getenv func(string) string, home, cwd string) (Paths, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg, err := resolveOne(getenv(envConfig), getenv(envXDGConfig), home, cwd, "config", configFile)
	if err != nil {
		return Paths{}, err
	}
	st, err := resolveOne(getenv(envState), getenv(envXDGState), home, cwd, "state", stateFile)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Config: cfg, State: st}, nil
}

func resolveOne(override, xdg, home, cwd, kind, file string) (string, error) {
	var raw string
	switch {
	case override != "":
		raw = override
	case xdg != "":
		raw = filepath.Join(xdg, appName, file)
	case home != "":
		if kind == "config" {
			raw = filepath.Join(home, ".config", appName, file)
		} else {
			raw = filepath.Join(home, ".local", "state", appName, file)
		}
	default:
		return "", fmt.Errorf("cannot resolve %s path: no override, XDG, or home", kind)
	}
	return Canonical(raw, cwd)
}

// Canonical is Abs + Clean, plus EvalSymlinks when the path exists.
func Canonical(path, cwd string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if !filepath.IsAbs(path) {
		if cwd == "" {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			cwd = wd
		}
		path = filepath.Join(cwd, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if _, err := os.Lstat(abs); err != nil {
		return abs, nil
	}
	eval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, nil
	}
	return filepath.Clean(eval), nil
}
