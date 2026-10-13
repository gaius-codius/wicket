package importer

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/gaius-codius/wicket/internal/config"
)

// ParseRDPFiles reads each path as a Microsoft .rdp file.
func ParseRDPFiles(paths []string) (ok []config.Import, skipped []Skip, err error) {
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
func ParseRDPFile(path string) (config.Import, Skip, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config.Import{}, Skip{}, err
	}
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return ParseRDP(data, name)
}

// ParseRDP maps a Microsoft .rdp document to a wicket profile.
// The password 51:b: blob is never stored.
func ParseRDP(data []byte, name string) (config.Import, Skip, error) {
	vals, err := parseRDP(data)
	if err != nil {
		return config.Import{}, Skip{}, err
	}
	p := config.DefaultProfile()
	p.Name = name
	p.Host = withPort(strings.TrimSpace(vals["full address"]), vals["server port"])
	p.User, p.Domain = splitAccount(vals["username"], vals["domain"])
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
	// A file that leaves the clipboard out gets Wicket's default, as it
	// would get FreeRDP's.
	if on, ok := rdpClipboard(vals["redirectclipboard"]); ok {
		p.Clipboard = on
	}
	var refused []config.DroppedShare
	p.Shares, refused = rdpDrives(vals["drivestoredirect"])
	return config.Import{Profile: p, Refused: refused}, Skip{}, nil
}

// rdpClipboard reads redirectclipboard: 0 is off and any other number on,
// as FreeRDP reads it. ok is false when the value is missing or not a number.
func rdpClipboard(v string) (on, ok bool) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return false, false
	}
	return n != 0, true
}

// rdpDrives maps drivestoredirect onto shares, and the entries it refuses.
// It is a ';' list with no escaping, and FreeRDP reads each entry as a path,
// label(path) or path(label), or path(path) for two folders. The entries
// that are not one local folder are refused by the share path rules: "*" and
// DynamicDrives (every drive, and drives plugged in later), Windows drive
// letters, and "%", the whole home folder (see shareSet).
func rdpDrives(v string) ([]config.Share, []config.DroppedShare) {
	var shares shareSet
	for part := range strings.SplitSeq(v, ";") {
		part = strings.TrimSpace(part)
		switch {
		case part == "*" || strings.EqualFold(part, "DynamicDrives"):
			shares.refuse(part, refusedAllDrive)
			continue
		case part == "%":
			shares.refuse(part, refusedHome)
			continue
		}
		name, path := "", part
		if open := strings.IndexByte(part, '('); open >= 0 && strings.HasSuffix(part, ")") {
			outer := strings.TrimSpace(part[:open])
			inner := strings.TrimSpace(part[open+1 : len(part)-1])
			switch {
			case isSharePath(inner) && isSharePath(outer):
				// Two folders, not a label: FreeRDP shares both.
				shares.add("", outer)
				name, path = "", inner
			case isSharePath(inner):
				name, path = outer, inner
			case isSharePath(outer):
				name, path = inner, outer
			}
			// Otherwise neither is a folder, and the whole entry is
			// refused as a path.
		}
		shares.add(name, path)
	}
	return shares.list, shares.refused
}

// withPort adds the separate "server port" the Windows client writes to a
// host that names none. Without it the session went to 3389. A port in
// "full address" itself wins, and 3389 or a port that is not a number from 1
// to 65535 leaves the host as it is.
func withPort(host, port string) string {
	if strings.Count(host, ":") > 1 && !strings.HasPrefix(host, "[") {
		// A bare IPv6 address: its colons are not a port, and Wicket reads
		// an unbracketed one as a malformed host:port.
		host = "[" + host + "]"
	}
	port = strings.TrimSpace(port)
	if host == "" || port == "" || strings.Trim(port, "0123456789") != "" {
		return host
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || n == rdpDefaultPort {
		return host
	}
	port = strconv.Itoa(n)
	switch {
	case strings.HasPrefix(host, "["):
		if strings.Contains(host[strings.LastIndexByte(host, ']')+1:], ":") {
			return host
		}
		return host + ":" + port
	case strings.Contains(host, ":"):
		return host
	default:
		return host + ":" + port
	}
}

const rdpDefaultPort = 3389

// decodeRDP returns the file as UTF-8 text. The Windows client saves .rdp
// files as UTF-16LE with a byte-order mark; other tools write UTF-8, with or
// without one.
func decodeRDP(data []byte) []byte {
	var order binary.ByteOrder
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		order = binary.LittleEndian
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		order = binary.BigEndian
	default:
		return bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	}
	data = data[2:]
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = order.Uint16(data[2*i:])
	}
	return []byte(string(utf16.Decode(units)))
}

func parseRDP(data []byte) (map[string]string, error) {
	vals := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(decodeRDP(data)))
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
		value := parts[2]
		// Any password-family key is dropped here, the gateway's
		// (gatewaypassword) and the 51:b: blob included, so no value of one
		// can reach a profile.
		if strings.Contains(key, "password") {
			continue
		}
		vals[key] = value
	}
	return vals, sc.Err()
}
