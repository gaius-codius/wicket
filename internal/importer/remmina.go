package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gaius-codius/wicket/internal/config"
)

// Remmina viewmode values that mean fullscreen (from Remmina's remmina_pref.h).
const (
	remminaFullscreenMode         = 2
	remminaScrolledFullscreenMode = 3
	remminaViewportFullscreenMode = 4
)

// remminaResCustom is Remmina's RES_USE_CUSTOM resolution_mode: only then
// are resolution_width and resolution_height the size to ask for. The other
// modes (client resolution, initial window size) keep stale values there.
const remminaResCustom = 0

// DefaultRemminaDir is Remmina's usual profile directory.
func DefaultRemminaDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "remmina")
}

// ParseRemminaDir reads every *.remmina file in dir (non-recursive).
func ParseRemminaDir(dir string) (ok []config.Profile, skipped []Skip, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var found bool
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".remmina") {
			continue
		}
		found = true
		path := filepath.Join(dir, e.Name())
		p, skip, perr := ParseRemminaFile(path)
		if perr != nil {
			skipped = append(skipped, Skip{Name: e.Name(), Reason: perr.Error()})
			continue
		}
		if skip.Reason != "" {
			skipped = append(skipped, skip)
			continue
		}
		ok = append(ok, p)
	}
	if !found {
		return nil, nil, fmt.Errorf("no .remmina files in %s", dir)
	}
	return ok, skipped, nil
}

// ParseRemminaFile reads one Remmina connection file.
func ParseRemminaFile(path string) (config.Profile, Skip, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config.Profile{}, Skip{}, err
	}
	return ParseRemmina(data, filepath.Base(path))
}

// ParseRemmina maps a Remmina INI document to a wicket profile.
// Only protocol=RDP is accepted. Password-family keys are never stored.
func ParseRemmina(data []byte, sourceName string) (config.Profile, Skip, error) {
	vals, err := parseINISection(data, "remmina")
	if err != nil {
		return config.Profile{}, Skip{}, err
	}
	if len(vals) == 0 {
		return config.Profile{}, Skip{Name: sourceName, Reason: "no [remmina] section"}, nil
	}
	proto := strings.ToUpper(strings.TrimSpace(vals["protocol"]))
	name := strings.TrimSpace(vals["name"])
	if name == "" {
		name = strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
	}
	if proto != "RDP" {
		reason := "not RDP"
		if proto != "" {
			reason = "not RDP (" + proto + ")"
		}
		return config.Profile{}, Skip{Name: name, Reason: reason}, nil
	}
	p := config.DefaultProfile()
	p.Name = name
	p.Host = strings.TrimSpace(vals["server"])
	p.User = strings.TrimSpace(vals["username"])
	p.Domain = strings.TrimSpace(vals["domain"])
	if p.User != "" && p.Domain == "" {
		if dom, user, ok := splitDomainUser(p.User); ok {
			p.Domain, p.User = dom, user
		}
	}
	p.Fullscreen = remminaFullscreen(vals["viewmode"])
	p.Size = remminaSize(vals["resolution_mode"], vals["resolution_width"], vals["resolution_height"])
	p.Shares = remminaShares(vals["sharefolder"], vals["drive"])
	return p, Skip{}, nil
}

func remminaFullscreen(viewmode string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(viewmode))
	if err != nil {
		return false
	}
	switch n {
	case remminaFullscreenMode, remminaScrolledFullscreenMode, remminaViewportFullscreenMode:
		return true
	default:
		return false
	}
}

// remminaSize is the custom size, when the profile asks for one. A file from
// before resolution_mode has only the width and height, which Remmina reads
// as custom when both are set.
func remminaSize(mode, w, h string) string {
	if m := strings.TrimSpace(mode); m != "" {
		if n, err := strconv.Atoi(m); err != nil || n != remminaResCustom {
			return ""
		}
	}
	ww, errW := strconv.Atoi(strings.TrimSpace(w))
	hh, errH := strconv.Atoi(strings.TrimSpace(h))
	if errW != nil || errH != nil || ww < 1 || hh < 1 {
		return ""
	}
	return fmt.Sprintf("%dx%d", ww, hh)
}

func splitDomainUser(user string) (domain, bare string, ok bool) {
	if i := strings.IndexByte(user, '\\'); i > 0 && i < len(user)-1 {
		return user[:i], user[i+1:], true
	}
	return "", user, false
}

// remminaShares maps Remmina's sharefolder (one path) and drive
// (name,path;name,path or a bare path) into wicket shares. Paths that are
// empty or not absolute / ~/… are skipped; existence is left to save time.
func remminaShares(sharefolder, drive string) []config.Share {
	var out []config.Share
	seen := map[string]bool{}
	add := func(name, path string) {
		path = strings.TrimSpace(path)
		name = strings.TrimSpace(name)
		if path == "" {
			return
		}
		if !strings.HasPrefix(path, "/") && path != "~" && !strings.HasPrefix(path, "~/") {
			return
		}
		key := strings.ToLower(path) + "\x00" + strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, config.Share{Path: path, Name: name})
	}
	if sf := strings.TrimSpace(sharefolder); sf != "" {
		add("", sf)
	}
	for _, part := range strings.Split(drive, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// FreeRDP / Remmina: name,path — or a bare path starting with /.
		if strings.HasPrefix(part, "/") || part == "~" || strings.HasPrefix(part, "~/") {
			add("", part)
			continue
		}
		name, path, ok := strings.Cut(part, ",")
		if !ok {
			continue
		}
		add(name, path)
	}
	return out
}
