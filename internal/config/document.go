package config

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	keyGeneral  = "general"
	keyProfiles = "profiles"
)

var secretKeyNames = map[string]struct{}{
	"password": {},
	"pass":     {},
	"secret":   {},
	"passwd":   {},
}

type document struct {
	general  map[string]any
	profiles []map[string]any
	extras   map[string]any
	warnings []string
}

func parseDocument(data []byte) (*document, error) {
	raw := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if _, err := toml.Decode(string(data), &raw); err != nil {
			return nil, fmt.Errorf("invalid TOML: %w", err)
		}
	}
	d := &document{
		general: map[string]any{},
		extras:  map[string]any{},
	}
	if g, ok := raw[keyGeneral]; ok {
		m, err := asTable(g, keyGeneral)
		if err != nil {
			return nil, err
		}
		d.general = m
		delete(raw, keyGeneral)
	}
	if p, ok := raw[keyProfiles]; ok {
		tables, err := asTableArray(p, keyProfiles)
		if err != nil {
			return nil, err
		}
		d.profiles = tables
		delete(raw, keyProfiles)
	}
	d.extras = raw
	d.stripSecrets()
	return d, nil
}

func (d *document) stripSecrets() {
	d.general = stripSecretKeys(d.general, keyGeneral, &d.warnings)
	for i, p := range d.profiles {
		d.profiles[i] = stripSecretKeys(p, fmt.Sprintf("profiles[%d]", i), &d.warnings)
	}
	d.extras = stripSecretKeys(d.extras, "", &d.warnings)
}

func stripSecretKeys(v any, prefix string, warnings *[]string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if _, secret := secretKeyNames[strings.ToLower(k)]; secret {
			*warnings = append(*warnings, "stripped key "+path)
			continue
		}
		out[k] = stripSecretValue(val, path, warnings)
	}
	return out
}

func stripSecretValue(v any, prefix string, warnings *[]string) any {
	switch t := v.(type) {
	case map[string]any:
		return stripSecretKeys(t, prefix, warnings)
	case []map[string]any:
		out := make([]map[string]any, len(t))
		for i, m := range t {
			out[i] = stripSecretKeys(m, fmt.Sprintf("%s[%d]", prefix, i), warnings)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			p := fmt.Sprintf("%s[%d]", prefix, i)
			if m, ok := item.(map[string]any); ok {
				out[i] = stripSecretKeys(m, p, warnings)
			} else {
				out[i] = stripSecretValue(item, p, warnings)
			}
		}
		return out
	default:
		return v
	}
}

func (d *document) typedProfiles() ([]Profile, error) {
	out := make([]Profile, 0, len(d.profiles))
	seen := map[string]int{}
	for i, table := range d.profiles {
		p, err := profileFromTable(table)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fmtIndex(i), err)
		}
		if prev, ok := seen[p.Name]; ok {
			return nil, fmt.Errorf("duplicate profile name %q (profiles[%d] and profiles[%d])", p.Name, prev, i)
		}
		seen[p.Name] = i
		out = append(out, p)
	}
	return out, nil
}

func profileFromTable(m map[string]any) (Profile, error) {
	p := defaultProfile()
	if err := assignString(m, "name", &p.Name, false); err != nil {
		return Profile{}, err
	}
	if err := assignString(m, "host", &p.Host, true); err != nil {
		return Profile{}, err
	}
	if err := assignString(m, "user", &p.User, true); err != nil {
		return Profile{}, err
	}
	if err := assignString(m, "domain", &p.Domain, true); err != nil {
		return Profile{}, err
	}
	if _, ok := m["client"]; ok {
		if err := assignString(m, "client", &p.Client, false); err != nil {
			return Profile{}, err
		}
		if p.Client == "" {
			p.Client = DefaultClient
		}
	}
	if err := assignString(m, "size", &p.Size, true); err != nil {
		return Profile{}, err
	}
	if err := assignBool(m, "fullscreen", &p.Fullscreen); err != nil {
		return Profile{}, err
	}
	if err := assignBool(m, "dynamic_resolution", &p.DynamicResolution); err != nil {
		return Profile{}, err
	}
	if err := assignInt(m, "scale", &p.Scale); err != nil {
		return Profile{}, err
	}
	if err := ValidateProfile(p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func assignString(m map[string]any, key string, dst *string, trim bool) error {
	v, ok := m[key]
	if !ok {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return &FieldError{Field: key, Msg: "must be a string"}
	}
	if trim {
		s = strings.TrimSpace(s)
	}
	*dst = s
	return nil
}

func assignBool(m map[string]any, key string, dst *bool) error {
	v, ok := m[key]
	if !ok {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		return &FieldError{Field: key, Msg: "must be a boolean"}
	}
	*dst = b
	return nil
}

func assignInt(m map[string]any, key string, dst *int) error {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch n := v.(type) {
	case int:
		*dst = n
	case int64:
		*dst = int(n)
	case uint64:
		*dst = int(n)
	default:
		return &FieldError{Field: key, Msg: "must be an integer"}
	}
	return nil
}

func applyProfile(table map[string]any, p Profile) map[string]any {
	out := cloneMap(table)
	out["name"] = p.Name
	out["host"] = p.Host
	out["user"] = p.User
	out["domain"] = p.Domain
	out["client"] = p.Client
	out["size"] = p.Size
	out["fullscreen"] = p.Fullscreen
	out["dynamic_resolution"] = p.DynamicResolution
	out["scale"] = int64(p.Scale)
	return out
}

func (d *document) encode() ([]byte, error) {
	out := map[string]any{}
	for k, v := range d.extras {
		out[k] = v
	}
	if d.general == nil {
		out[keyGeneral] = map[string]any{}
	} else {
		out[keyGeneral] = d.general
	}
	if len(d.profiles) > 0 {
		out[keyProfiles] = d.profiles
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if !bytes.Contains(b, []byte("["+keyGeneral+"]")) && !bytes.Contains(b, []byte("["+keyGeneral+".")) {
		// The encoder normally emits [general] even when it is empty. Should
		// that ever change, append the header rather than prepend it: a table
		// header at the top would swallow every bare key the encoder wrote
		// before the first table, which is where the preserved extras live.
		if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
			b = append(b, '\n')
		}
		b = append(b, []byte("["+keyGeneral+"]\n")...)
	}
	return b, nil
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func asTable(v any, name string) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a table", name)
	}
	return m, nil
}

func asTableArray(v any, name string) ([]map[string]any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []map[string]any:
		return t, nil
	case []any:
		out := make([]map[string]any, 0, len(t))
		for i, item := range t {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s[%d] must be a table", name, i)
			}
			out = append(out, m)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be an array of tables", name)
	}
}
