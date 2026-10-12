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
	p.User, p.Domain = splitAccount(vals["username"], vals["domain"])
	p.Fullscreen = remminaFullscreen(vals["viewmode"])
	p.Size = remminaSize(vals["resolution_mode"], vals["resolution_width"], vals["resolution_height"])
	if off, ok := keyFileBool(vals["disableclipboard"]); ok {
		p.Clipboard = !off
	}
	p.Shares = remminaShares(vals["sharefolder"], vals["drive"])
	return p, Skip{}, nil
}

// keyFileBool reads a Remmina flag, which it writes as 0 or 1; GKeyFile's
// true and false are read too. ok is false when the key is missing or holds
// anything else, so the profile keeps Wicket's default.
func keyFileBool(v string) (b, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true":
		return true, true
	case "0", "false":
		return false, true
	}
	return false, false
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

// dropUPNDomain keeps a UPN user (alice@corp.com) and drops a domain set
// beside it: the UPN already names the domain, and the pair together is one
// Wicket refuses, so the file was skipped. Only a name on both sides of the
// "@" is a UPN; "alice@" beside a domain is left to be skipped as before,
// rather than lose the domain it needs.
func dropUPNDomain(user, domain string) (string, string) {
	name, suffix, ok := strings.Cut(user, "@")
	if domain != "" && ok && name != "" && suffix != "" && !strings.Contains(user, `\`) {
		return user, ""
	}
	return user, domain
}

// splitAccount is the user and domain a file names. DOMAIN\user is split,
// and a domain key set beside it wins over the one in the user, as the
// Windows client reads the pair.
func splitAccount(user, domain string) (string, string) {
	user = strings.TrimSpace(user)
	domain = strings.TrimSpace(domain)
	if dom, bare, ok := splitDomainUser(user); ok {
		user = bare
		if domain == "" {
			domain = dom
		}
	}
	return dropUPNDomain(user, domain)
}

func splitDomainUser(user string) (domain, bare string, ok bool) {
	if i := strings.IndexByte(user, '\\'); i > 0 && i < len(user)-1 {
		return user[:i], user[i+1:], true
	}
	return "", user, false
}

// shareSet collects the shares an import keeps, by the rules both formats
// share. A path must be absolute or ~/…; an empty, relative or malformed one
// is skipped. So is the whole home folder or the root: a file sent to the
// user could otherwise hand a server every file they have, and Wicket offers
// home only as share_home, which an import does not turn on. Whether the
// folder exists is left to the time it is used.
type shareSet struct {
	list []config.Share
	seen map[string]bool
}

func (s *shareSet) add(name, path string) {
	path = strings.TrimSpace(path)
	name = strings.TrimSpace(name)
	if !isSharePath(path) || strings.ContainsRune(path, ',') || wholeHomeOrRoot(path) {
		return
	}
	key := strings.ToLower(path) + "\x00" + strings.ToLower(name)
	if s.seen[key] {
		return
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[key] = true
	s.list = append(s.list, config.Share{Path: path, Name: name})
}

// wholeHomeOrRoot reports whether path, once expanded, is "/" or the home
// folder itself. A path that cannot be expanded counts as one, so it is
// skipped rather than guessed at.
func wholeHomeOrRoot(path string) bool {
	expanded, err := config.ExpandPath(path)
	if err != nil {
		return true
	}
	if expanded == "/" {
		return true
	}
	home, err := os.UserHomeDir()
	return err == nil && expanded == filepath.Clean(home)
}

func isSharePath(path string) bool {
	return strings.HasPrefix(path, "/") || path == "~" || strings.HasPrefix(path, "~/")
}

// remminaShares maps Remmina's sharefolder (one path) and drive
// (name,path;name,path or a bare path) into wicket shares.
func remminaShares(sharefolder, drive string) []config.Share {
	var shares shareSet
	shares.add("", sharefolder)
	for _, part := range strings.Split(drive, ";") {
		part = strings.TrimSpace(part)
		// FreeRDP / Remmina: name,path — or a bare path starting with /.
		if isSharePath(part) {
			shares.add("", part)
			continue
		}
		name, path, ok := strings.Cut(part, ",")
		if !ok {
			continue
		}
		shares.add(name, path)
	}
	return shares.list
}
