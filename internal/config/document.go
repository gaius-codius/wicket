package config

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"time"

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

	// src is the file as read, which a save patches rather than replaces.
	src []byte
	// origProfiles are the profiles as read, and origIdx says which of
	// them each entry of profiles started as, or -1 for one added since.
	// Together they tell the patch what a save changed.
	origProfiles []map[string]any
	origIdx      []int
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
	d.src = data
	d.origProfiles = slices.Clone(d.profiles)
	d.origIdx = make([]int, len(d.profiles))
	for i := range d.origIdx {
		d.origIdx[i] = i
	}
	return d, nil
}

// setProfile replaces the profile at idx.
func (d *document) setProfile(idx int, table map[string]any) { d.profiles[idx] = table }

// addProfile appends a profile.
func (d *document) addProfile(table map[string]any) {
	d.profiles = append(d.profiles, table)
	d.origIdx = append(d.origIdx, -1)
}

// removeProfile deletes the profile at idx.
func (d *document) removeProfile(idx int) {
	d.profiles = slices.Delete(d.profiles, idx, idx+1)
	d.origIdx = slices.Delete(d.origIdx, idx, idx+1)
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
	p := DefaultProfile()
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
	if err := assignBool(m, "multimon", &p.Multimon); err != nil {
		return Profile{}, err
	}
	if err := assignBool(m, "clipboard", &p.Clipboard); err != nil {
		return Profile{}, err
	}
	if err := assignBool(m, "share_home", &p.ShareHome); err != nil {
		return Profile{}, err
	}
	// Multimon is full screen across every monitor, so a hand-edited one
	// without fullscreen loads with it, and the form shows what will run.
	if p.Multimon {
		p.Fullscreen = true
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
	// Optional keys left empty are left out, rather than written as
	// `size = ""`: an empty value means the same as no key, and a hand-edited
	// file reads better without them. Unknown keys are still kept.
	for key, v := range map[string]string{"domain": p.Domain, "size": p.Size} {
		if v == "" {
			delete(out, key)
		} else {
			out[key] = v
		}
	}
	// A new profile spells these out, so a hand-editor finds them. An
	// existing one that leaves one out, at its default, keeps leaving it out:
	// a save should not grow a hand-written profile by lines that change
	// nothing.
	adding := len(table) == 0
	for _, kv := range []struct {
		key      string
		val, def any
	}{
		{"client", p.Client, DefaultClient},
		{"fullscreen", p.Fullscreen, DefaultFullscreen},
		{"dynamic_resolution", p.DynamicResolution, DefaultDynamicResolution},
		{"scale", int64(p.Scale), int64(DefaultScale)},
	} {
		if _, had := out[kv.key]; had || adding || kv.val != kv.def {
			out[kv.key] = kv.val
		}
	}
	// Settings added after v0.1 are written only when they differ from
	// their default, so saving a profile that never touched them leaves the
	// file as it was.
	for key, v := range map[string]struct{ val, def bool }{
		"multimon":   {p.Multimon, false},
		"clipboard":  {p.Clipboard, DefaultClipboard},
		"share_home": {p.ShareHome, false},
	} {
		if v.val == v.def {
			delete(out, key)
		} else {
			out[key] = v.val
		}
	}
	return out
}

func (d *document) encode() ([]byte, error) {
	out := map[string]any{}
	for k, v := range d.extras {
		out[k] = fixLocalTimes(v)
	}
	if d.general == nil {
		out[keyGeneral] = map[string]any{}
	} else {
		out[keyGeneral] = fixLocalTimes(d.general)
	}
	if len(d.profiles) > 0 {
		out[keyProfiles] = fixLocalTimes(d.profiles)
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

// localTOML is a decoded TOML local date, time, or datetime. BurntSushi's
// encoder runs these through time.UTC, which shifts the wall clock by the
// zone offset baked into date-local / time-local / datetime-local (see
// BurntSushi/toml internal.LocalDate). MarshalTOML keeps the wall clock.
type localTOML time.Time

func (t localTOML) MarshalTOML() ([]byte, error) {
	tt := time.Time(t)
	var s string
	switch tt.Location().String() {
	case "date-local":
		s = tt.Format("2006-01-02")
	case "time-local":
		s = tt.Format("15:04:05.999999999")
	case "datetime-local":
		s = tt.Format("2006-01-02T15:04:05.999999999")
	default:
		s = tt.Format(time.RFC3339Nano)
	}
	return []byte(s), nil
}

// fixLocalTimes wraps decoded local date/time values so encode does not
// shift them. Values with an explicit offset are left alone.
func fixLocalTimes(v any) any {
	switch t := v.(type) {
	case time.Time:
		switch t.Location().String() {
		case "date-local", "time-local", "datetime-local":
			return localTOML(t)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = fixLocalTimes(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(t))
		for i, m := range t {
			out[i] = fixLocalTimes(m).(map[string]any)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = fixLocalTimes(item)
		}
		return out
	default:
		return v
	}
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
