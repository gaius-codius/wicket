package config

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

// profileKeyOrder is the order Wicket writes a profile's keys in when it
// adds them. Keys a profile already has stay where they are.
var profileKeyOrder = []string{
	"name", "host", "user", "domain", "client", "size",
	"fullscreen", "dynamic_resolution", "scale",
	"multimon", "clipboard", "share_home",
}

// render is the file a save writes. Where it can, it is the file as read
// with only the changed values, keys and profiles edited, so comments and
// formatting a hand-editor left survive the save. patched reports whether
// that worked; when it did not, the file is re-encoded in full, as every
// save once was.
//
// A patch is only used when the decoder reads it as exactly the document a
// full re-encode would produce. The scanner and the splicing are then free
// to be wrong about a file they do not follow: the cost is the old
// behaviour, never a config that says something the user did not save.
func (d *document) render() (data []byte, patched bool, err error) {
	full, err := d.encode()
	if err != nil {
		return nil, false, err
	}
	p, err := d.safePatch()
	if err != nil || !sameDocument(p, full) {
		return full, false, nil
	}
	return p, true, nil
}

// safePatch is patch, with a panic in the hand-written scanner turned into
// the full rewrite rather than a failed save.
func (d *document) safePatch() (out []byte, err error) {
	defer func() {
		if recover() != nil {
			out, err = nil, errLayout
		}
	}()
	return d.patch()
}

// sameDocument reports whether a and b decode to the same values. A missing
// [general] and an empty one mean the same; the full encoder always writes
// the table.
func sameDocument(a, b []byte) bool {
	decode := func(data []byte) (map[string]any, bool) {
		raw := map[string]any{}
		if _, err := toml.Decode(string(data), &raw); err != nil {
			return nil, false
		}
		if _, ok := raw[keyGeneral]; !ok {
			raw[keyGeneral] = map[string]any{}
		}
		return raw, true
	}
	ra, ok := decode(a)
	if !ok {
		return false
	}
	rb, ok := decode(b)
	return ok && reflect.DeepEqual(ra, rb)
}

// edit replaces src[start:end] with text; start == end inserts.
type edit struct {
	start, end int
	text       string
}

// patch applies the document's changes to the bytes it was read from.
func (d *document) patch() ([]byte, error) {
	stmts, err := scanStatements(d.src)
	if err != nil {
		return nil, err
	}
	nl := "\n"
	if bytes.Contains(d.src, []byte("\r\n")) {
		nl = "\r\n"
	}

	var heads []int
	for i, st := range stmts {
		if st.kind == stmtArrayTable && slices.Equal(st.path, []string{keyProfiles}) {
			heads = append(heads, i)
		}
	}
	// Profiles written any other way, such as profiles = [{...}], are left
	// to the full rewrite.
	if len(heads) != len(d.origProfiles) {
		return nil, errLayout
	}

	var edits []edit
	gone := make([]bool, len(stmts))
	kept := map[int]bool{}
	for _, j := range d.origIdx {
		if j >= 0 {
			kept[j] = true
		}
	}
	for j, h := range heads {
		if kept[j] {
			continue
		}
		first, last, start, end := deleteSpan(stmts, h, len(d.src))
		for i := first; i < last; i++ {
			gone[i] = true
		}
		edits = append(edits, edit{start: start, end: end})
	}

	// Secret keys go wherever they are, with their line and any comment on
	// it, as the decoder side already dropped them from the maps.
	for i, st := range stmts {
		if !gone[i] && st.kind == stmtKeyValue && isSecretKey(st.path[len(st.path)-1]) {
			gone[i] = true
			edits = append(edits, edit{start: st.start, end: st.end})
		}
	}

	for k, j := range d.origIdx {
		if j < 0 {
			continue
		}
		es, err := patchProfile(d.src, stmts, heads[j], d.origProfiles[j], d.profiles[k], nl)
		if err != nil {
			return nil, err
		}
		edits = append(edits, es...)
	}

	// A new profile goes after the last one, so the profiles stay together
	// ahead of anything that follows them, or at the end of a file that has
	// none.
	at, after := len(d.src), ""
	if len(heads) > 0 {
		h := heads[len(heads)-1]
		l := lastContent(stmts, h, blockEnd(stmts, h))
		at = stmts[l].end
		if l+1 < len(stmts) && !(stmts[l+1].kind == stmtTrivia && !stmts[l+1].comment) {
			after = nl
		}
	}
	before := separate(d.src[:at], nl)
	for k, j := range d.origIdx {
		if j >= 0 {
			continue
		}
		block, err := newProfileBlock(d.profiles[k], nl)
		if err != nil {
			return nil, err
		}
		edits = append(edits, edit{start: at, end: at, text: before + block + after})
	}
	return applyEdits(d.src, edits)
}

// ownSection returns the statements that belong to the table whose header is
// stmts[h]: those up to the next header of any kind.
func ownSection(stmts []stmt, h int) (from, to int) {
	to = h + 1
	for to < len(stmts) && stmts[to].kind != stmtTable && stmts[to].kind != stmtArrayTable {
		to++
	}
	return h + 1, to
}

// deleteSpan is the part of the file a deleted profile takes with it: the
// comment lines directly above its header, the profile, and its own
// subtables ([profiles.x]), up to the comment lines directly above whatever
// comes next. A profile at the end of the file also takes the blank lines
// that separated it from the one before, so the file does not end in them.
func deleteSpan(stmts []stmt, h, size int) (first, last, start, end int) {
	isComment := func(i int) bool { return stmts[i].kind == stmtTrivia && stmts[i].comment }
	first = h
	for first > 0 && isComment(first-1) {
		first--
	}
	last = blockEnd(stmts, h)
	if last == len(stmts) {
		// Comments after the profile's last line are notes on the file,
		// not on the profile, and stay.
		for i := lastContent(stmts, h, last) + 1; i < last; i++ {
			if isComment(i) {
				return first, i, stmts[first].start, stmts[i].start
			}
		}
		for first > 0 && stmts[first-1].kind == stmtTrivia && !stmts[first-1].comment {
			first--
		}
		return first, last, stmts[first].start, size
	}
	for last > h+1 && isComment(last-1) {
		last--
	}
	return first, last, stmts[first].start, stmts[last].start
}

// lastContent is the last statement in stmts[h:end] that is not a blank line
// or a comment.
func lastContent(stmts []stmt, h, end int) int {
	l := h
	for i := h; i < end; i++ {
		if stmts[i].kind != stmtTrivia {
			l = i
		}
	}
	return l
}

// blockEnd is the statement after the profile whose header is stmts[h] and
// its subtables.
func blockEnd(stmts []stmt, h int) int {
	i := h + 1
	for i < len(stmts) {
		st := stmts[i]
		if (st.kind == stmtArrayTable || st.kind == stmtTable) && (len(st.path) < 2 || st.path[0] != keyProfiles) {
			break
		}
		i++
	}
	return i
}

// patchProfile edits the profile whose header is stmts[h] from old to now:
// a changed value is replaced where it stands, a removed key loses its line,
// and a new key goes after the profile's last key.
func patchProfile(src []byte, stmts []stmt, h int, old, now map[string]any, nl string) ([]edit, error) {
	from, to := ownSection(stmts, h)
	find := func(key string) (int, bool) {
		for i := from; i < to; i++ {
			if st := stmts[i]; st.kind == stmtKeyValue && len(st.path) == 1 && st.path[0] == key {
				return i, true
			}
		}
		return 0, false
	}

	keys := map[string]bool{}
	for k := range old {
		keys[k] = true
	}
	for k := range now {
		keys[k] = true
	}
	var edits []edit
	var added []string
	for k := range keys {
		ov, inOld := old[k]
		nv, inNow := now[k]
		if inOld && inNow && reflect.DeepEqual(ov, nv) {
			continue
		}
		if !inOld {
			added = append(added, k)
			continue
		}
		// A key set some other way, by a dotted key or a subtable, is
		// beyond a patch.
		i, ok := find(k)
		if !ok {
			return nil, errLayout
		}
		if !inNow {
			edits = append(edits, edit{start: stmts[i].start, end: stmts[i].end})
			continue
		}
		v, err := formatValue(nv)
		if err != nil {
			return nil, err
		}
		if lit, ok := literalString(src[stmts[i].valStart:stmts[i].valEnd], nv); ok {
			v = lit
		}
		edits = append(edits, edit{start: stmts[i].valStart, end: stmts[i].valEnd, text: v})
	}
	if len(added) == 0 {
		return edits, nil
	}

	at, indent := stmts[h].end, ""
	for i := from; i < to; i++ {
		if stmts[i].kind == stmtKeyValue {
			at, indent = stmts[i].end, stmts[i].indent
		}
	}
	// A last line without a newline gets one before the new keys follow.
	var b strings.Builder
	if at == len(src) && at > 0 && src[at-1] != '\n' {
		b.WriteString(nl)
	}
	for _, k := range orderKeys(added) {
		v, err := formatValue(now[k])
		if err != nil {
			return nil, err
		}
		b.WriteString(indent + k + " = " + v + nl)
	}
	return append(edits, edit{start: at, end: at, text: b.String()}), nil
}

// newProfileBlock is a [[profiles]] table for a profile added by a save.
func newProfileBlock(table map[string]any, nl string) (string, error) {
	var b strings.Builder
	b.WriteString("[[" + keyProfiles + "]]" + nl)
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	for _, k := range orderKeys(keys) {
		v, err := formatValue(table[k])
		if err != nil {
			return "", err
		}
		b.WriteString(k + " = " + v + nl)
	}
	return b.String(), nil
}

// orderKeys sorts keys into profileKeyOrder, with any others after them by
// name.
func orderKeys(keys []string) []string {
	rank := func(k string) int {
		if i := slices.Index(profileKeyOrder, k); i >= 0 {
			return i
		}
		return len(profileKeyOrder)
	}
	out := slices.Clone(keys)
	sort.Slice(out, func(a, b int) bool {
		ra, rb := rank(out[a]), rank(out[b])
		if ra != rb {
			return ra < rb
		}
		return out[a] < out[b]
	})
	return out
}

// formatValue writes v as TOML, the way the encoder would. Wicket only sets
// strings, booleans and integers.
func formatValue(v any) (string, error) {
	switch v.(type) {
	case string, bool, int64, int:
	default:
		return "", errLayout
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(map[string]any{"v": v}); err != nil {
		return "", err
	}
	out, ok := strings.CutPrefix(strings.TrimSuffix(buf.String(), "\n"), "v = ")
	if !ok || strings.Contains(out, "\n") {
		return "", fmt.Errorf("format %T: %w", v, errLayout)
	}
	return out, nil
}

// literalString writes v the way old, the value it replaces, was written
// when that was a one-line 'literal string' and v can be one too.
func literalString(old []byte, v any) (string, bool) {
	sv, ok := v.(string)
	if !ok || len(old) < 2 || old[0] != '\'' || bytes.HasPrefix(old, []byte("'''")) {
		return "", false
	}
	if strings.ContainsRune(sv, '\'') || strings.ContainsFunc(sv, unicode.IsControl) {
		return "", false
	}
	return "'" + sv + "'", true
}

// applyEdits splices edits into src. Edits must not overlap.
func applyEdits(src []byte, edits []edit) ([]byte, error) {
	sort.SliceStable(edits, func(a, b int) bool {
		if edits[a].start != edits[b].start {
			return edits[a].start < edits[b].start
		}
		return edits[a].end < edits[b].end
	})
	var out bytes.Buffer
	pos := 0
	for _, e := range edits {
		if e.start < pos {
			return nil, errLayout
		}
		out.Write(src[pos:e.start])
		out.WriteString(e.text)
		pos = e.end
	}
	out.Write(src[pos:])
	return out.Bytes(), nil
}

// separate is what goes between before and a table added after it: a
// newline to end an unfinished last line, and a blank line unless before is
// empty or already ends in one.
func separate(before []byte, nl string) string {
	if len(before) == 0 {
		return ""
	}
	s := ""
	if !bytes.HasSuffix(before, []byte("\n")) {
		s = nl
		before = append(slices.Clip(before), nl...)
	}
	if !bytes.HasSuffix(before, []byte(nl+nl)) {
		s += nl
	}
	return s
}

func isSecretKey(k string) bool {
	_, ok := secretKeyNames[strings.ToLower(k)]
	return ok
}
