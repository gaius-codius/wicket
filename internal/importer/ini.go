package importer

import (
	"bufio"
	"bytes"
	"strings"
)

// parseINISection returns key/value pairs from the named section of a
// GKeyFile-style INI document. Password-family keys are detected but their
// values are never stored: the returned map omits them, and sawPassword is
// set when any such key was present.
func parseINISection(data []byte, section string) (vals map[string]string, sawPassword bool, err error) {
	vals = make(map[string]string)
	section = strings.ToLower(section)
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
		if key == "" {
			continue
		}
		if isPasswordKey(key) {
			sawPassword = true
			continue
		}
		vals[key] = strings.TrimSpace(value)
	}
	return vals, sawPassword, sc.Err()
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
