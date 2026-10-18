// Package exporter writes wicket profiles in formats other RDP clients open.
package exporter

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gaius-codius/wicket/internal/config"
)

// Omission is a setting of a profile that RDP leaves out of the file, and
// why, so the export can say so rather than drop it silently.
type Omission struct {
	Setting string
	Reason  string
}

// RDP is p as the text of a Microsoft .rdp file, with the settings it could
// not write. It writes only what Windows Remote Desktop and FreeRDP both read
// and wicket's importer reads back: the host, the account, the display, the
// clipboard and the shared folders. A profile holds no password, and RDP
// writes neither the profile's id nor its client: the id names its keyring
// entry, which another machine does not have.
func RDP(p config.Profile) (string, []Omission) {
	var (
		b       strings.Builder
		omitted []Omission
	)
	line := func(key, typ, value string) {
		b.WriteString(key + ":" + typ + ":" + value + "\n")
	}
	flag := func(on bool) string {
		if on {
			return "1"
		}
		return "0"
	}

	line("full address", "s", strings.TrimSpace(p.Host))
	line("username", "s", p.User)
	if p.Domain != "" {
		line("domain", "s", p.Domain)
	}
	// Multimon is full screen across every monitor; a loaded config already
	// has Fullscreen on beside it, but the file must not rely on that.
	fullscreen := p.Fullscreen || p.Multimon
	if fullscreen {
		line("screen mode id", "i", "2")
	} else {
		line("screen mode id", "i", "1")
	}
	if w, h, ok := dimensions(p.Size); ok {
		line("desktopwidth", "i", strconv.Itoa(w))
		line("desktopheight", "i", strconv.Itoa(h))
	} else if s := strings.TrimSpace(p.Size); s != "" {
		omitted = append(omitted, Omission{"size " + s, ".rdp sets a size only in pixels"})
	}
	line("use multimon", "i", flag(p.Multimon))
	line("redirectclipboard", "i", flag(p.Clipboard))

	drives, dropped := drives(p.Shares)
	omitted = append(omitted, dropped...)
	if drives != "" {
		line("drivestoredirect", "s", drives)
	}

	if p.ShareHome {
		omitted = append(omitted, Omission{"share_home", "wicket does not import a shared home folder from a file"})
	}
	if p.Scale != config.DefaultScale {
		omitted = append(omitted, Omission{"scale " + strconv.Itoa(p.Scale), "not exported"})
	}
	if !p.DynamicResolution {
		omitted = append(omitted, Omission{"dynamic resolution off", "not exported"})
	}
	return b.String(), omitted
}

// dimensions reads a size of WxH pixels. A size in percent, or none, is not
// one .rdp can hold.
func dimensions(size string) (w, h int, ok bool) {
	ws, hs, ok := strings.Cut(strings.ToLower(strings.TrimSpace(size)), "x")
	if !ok {
		return 0, 0, false
	}
	w, errW := strconv.Atoi(ws)
	h, errH := strconv.Atoi(hs)
	if errW != nil || errH != nil || w < 1 || h < 1 {
		return 0, 0, false
	}
	return w, h, true
}

// drives is the drivestoredirect value for shares: a ';' list of path or
// path(name), the form FreeRDP reads without guessing which part is the
// folder. FreeRDP expands neither ~ nor anything else, so a ~/… path is
// written out in full. The list has no escaping, so a path with ';', '(' or
// ')' in it cannot be written and is left out.
func drives(shares []config.Share) (string, []Omission) {
	var (
		entries []string
		omitted []Omission
	)
	for _, s := range shares {
		path, err := config.ExpandPath(s.Path)
		if err != nil {
			omitted = append(omitted, Omission{"share " + strconv.Quote(s.Path), err.Error()})
			continue
		}
		if strings.ContainsAny(path, ";()") {
			omitted = append(omitted, Omission{"share " + strconv.Quote(s.Path), ".rdp cannot hold a path with ';', '(' or ')'"})
			continue
		}
		if name := strings.TrimSpace(s.Name); name != "" {
			path += "(" + name + ")"
		}
		entries = append(entries, path)
	}
	return strings.Join(entries, ";"), omitted
}

// Encode is text as the Windows client saves an .rdp file: UTF-16LE with a
// byte-order mark and CRLF line ends. FreeRDP reads the same.
func Encode(text string) []byte {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r\n")
	units := utf16.Encode([]rune(text))
	out := make([]byte, 2, 2+2*len(units))
	out[0], out[1] = 0xFF, 0xFE
	for _, u := range units {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

// maxFileName bounds a file name in bytes, below the 255 most file systems
// allow, with room for ".rdp" and a "-N" a clash adds.
const maxFileName = 200

// FileName is a safe file name for a profile called name, ending ".rdp".
// Anything but letters, digits, spaces and -_.+@ becomes '_', so a name
// cannot climb out of the folder, name a device on Windows, hide the file
// behind a leading dot or end in a dot or space Windows drops. Importing the
// file names the profile after it, without ".rdp".
func FileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r > 0x7F && (unicode.IsLetter(r) || unicode.IsDigit(r)),
			strings.ContainsRune(" -_.+@", r):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > maxFileName {
		s = s[:maxFileName]
		// Cut back to a whole character.
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	s = strings.TrimLeft(s, ". -")
	s = strings.TrimRight(s, ". ")
	if s == "" {
		s = "profile"
	}
	if windowsDevice(s) {
		s = "_" + s
	}
	return s + ".rdp"
}

// windowsDevice reports whether Windows reads a file called s as a device,
// whatever its extension: CON, PRN, AUX, NUL, COM1–9 or LPT1–9.
func windowsDevice(s string) bool {
	base, _, _ := strings.Cut(s, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

// Unique is FileName(name), with -2, -3… before ".rdp" when taken already
// holds it. taken is keyed in lower case, since macOS and Windows file
// systems fold case, and the result is added to it.
func Unique(name string, taken map[string]bool) string {
	file := FileName(name)
	stem := strings.TrimSuffix(file, ".rdp")
	for n := 2; taken[strings.ToLower(file)]; n++ {
		file = fmt.Sprintf("%s-%d.rdp", stem, n)
	}
	taken[strings.ToLower(file)] = true
	return file
}
