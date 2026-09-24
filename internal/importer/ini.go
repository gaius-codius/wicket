package importer

import (
	"bufio"
	"bytes"
	"strings"
)

// parseINISection returns key/value pairs from the named section of a
// GKeyFile-style INI document, with GKeyFile's escapes undone. Values of
// password-family keys are never stored: the returned map omits them.
func parseINISection(data []byte, section string) (map[string]string, error) {
	vals := make(map[string]string)
	in := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end <= 1 {
				in = false
				continue
			}
			in = strings.EqualFold(line[1:end], section)
			continue
		}
		if !in {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || isPasswordKey(key) {
			continue
		}
		vals[key] = unescapeKeyFile(strings.TrimSpace(value))
	}
	return vals, sc.Err()
}

// unescapeKeyFile undoes the escapes g_key_file_set_string writes: Remmina
// saves "CORP\alice" as "CORP\\alice". An unknown escape is kept as written.
func unescapeKeyFile(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func isPasswordKey(key string) bool {
	k := strings.ToLower(key)
	switch k {
	case "password", "pass", "passwd", "secret", "ssh_password", "ssh_passphrase",
		"gateway_password", "proxy_password":
		return true
	}
	return strings.Contains(k, "password") || strings.Contains(k, "passwd")
}
