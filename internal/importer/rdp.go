package importer

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gaius-codius/wicket/internal/config"
)

// ParseRDPFiles reads each path as a Microsoft .rdp file.
func ParseRDPFiles(paths []string) (ok []config.Profile, skipped []Skip, err error) {
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("no .rdp files given")
	}
	nRead := 0
	for _, path := range paths {
		p, skip, perr := ParseRDPFile(path)
		if perr != nil {
			skipped = append(skipped, Skip{Name: filepath.Base(path), Reason: perr.Error()})
			continue
		}
		nRead++
		if skip.Reason != "" {
			skipped = append(skipped, skip)
			continue
		}
		ok = append(ok, p)
	}
	if nRead == 0 {
		return nil, skipped, fmt.Errorf("no .rdp files readable")
	}
	return ok, skipped, nil
}

// ParseRDPFile reads one Microsoft .rdp connection file.
func ParseRDPFile(path string) (config.Profile, Skip, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config.Profile{}, Skip{}, err
	}
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return ParseRDP(data, name)
}

// ParseRDP maps a Microsoft .rdp document to a wicket profile.
// The password 51:b: blob is never stored.
func ParseRDP(data []byte, name string) (config.Profile, Skip, error) {
	vals, err := parseRDP(data)
	if err != nil {
		return config.Profile{}, Skip{}, err
	}
	p := config.DefaultProfile()
	p.Name = name
	p.Host = strings.TrimSpace(vals["full address"])
	user := strings.TrimSpace(vals["username"])
	domain := strings.TrimSpace(vals["domain"])
	if user != "" {
		if dom, bare, ok := splitDomainUser(user); ok {
			p.User = bare
			if domain == "" {
				domain = dom
			}
		} else {
			p.User = user
		}
	}
	p.Domain = domain
	if sm, ok := vals["screen mode id"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(sm)); err == nil && n == 2 {
			p.Fullscreen = true
		}
	}
	w := strings.TrimSpace(vals["desktopwidth"])
	h := strings.TrimSpace(vals["desktopheight"])
	if w != "" && h != "" {
		if ww, errW := strconv.Atoi(w); errW == nil {
			if hh, errH := strconv.Atoi(h); errH == nil && ww >= 1 && hh >= 1 {
				p.Size = fmt.Sprintf("%dx%d", ww, hh)
			}
		}
	}
	return p, Skip{}, nil
}

func parseRDP(data []byte) (map[string]string, error) {
	vals := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		// key:type:value — type is i, s, b, …
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		typ := strings.ToLower(strings.TrimSpace(parts[1]))
		value := parts[2]
		if key == "password" || strings.HasPrefix(key, "password ") {
			continue
		}
		if typ == "b" && strings.Contains(key, "password") {
			continue
		}
		vals[key] = value
	}
	return vals, sc.Err()
}
