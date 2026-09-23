package config

import (
	"bytes"
	"errors"

	"github.com/BurntSushi/toml"
)

// errLayout means the file holds something the scanner does not follow. The
// save then rewrites the file in full, as it did before patching existed, so
// the scanner only has to be right about what it accepts, not complete.
var errLayout = errors.New("config layout not followed")

type stmtKind int

const (
	stmtTrivia     stmtKind = iota // a blank line or a comment
	stmtTable                      // [a.b]
	stmtArrayTable                 // [[a.b]]
	stmtKeyValue                   // a.b = value
)

// stmt is one top-level line of a TOML file, or several when a value spans
// lines.
type stmt struct {
	kind stmtKind
	// start and end cover the whole statement: its indentation, any
	// trailing comment, and the newline.
	start, end int
	// path is a header's table or a key/value pair's key.
	path []string
	// valStart and valEnd are the value of a key/value pair.
	valStart, valEnd int
	indent           string
	comment          bool
}

// scanStatements splits src into statements. It follows the TOML that
// decides where a statement ends (strings, arrays and inline tables across
// lines, comments) and leaves validating the rest to the decoder.
func scanStatements(src []byte) ([]stmt, error) {
	s := &scanner{src: src}
	var out []stmt
	for s.pos < len(src) {
		st, err := s.statement()
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

type scanner struct {
	src []byte
	pos int
}

func (s *scanner) peek() byte {
	if s.pos >= len(s.src) {
		return 0
	}
	return s.src[s.pos]
}

func (s *scanner) statement() (stmt, error) {
	st := stmt{start: s.pos}
	s.skipBlank()
	st.indent = string(s.src[st.start:s.pos])
	switch c := s.peek(); {
	case s.atLineEnd():
		st.kind = stmtTrivia
	case c == '#':
		st.kind, st.comment = stmtTrivia, true
		s.skipComment()
	case c == '[':
		st.kind = stmtTable
		s.pos++
		if s.peek() == '[' {
			st.kind = stmtArrayTable
			s.pos++
		}
		path, err := s.key()
		if err != nil {
			return st, err
		}
		st.path = path
		closing := "]"
		if st.kind == stmtArrayTable {
			closing = "]]"
		}
		if !bytes.HasPrefix(s.src[s.pos:], []byte(closing)) {
			return st, errLayout
		}
		s.pos += len(closing)
	default:
		st.kind = stmtKeyValue
		path, err := s.key()
		if err != nil {
			return st, err
		}
		st.path = path
		if s.peek() != '=' {
			return st, errLayout
		}
		s.pos++
		s.skipBlank()
		st.valStart = s.pos
		if err := s.value(); err != nil {
			return st, err
		}
		st.valEnd = s.pos
	}
	if err := s.lineEnd(); err != nil {
		return st, err
	}
	st.end = s.pos
	return st, nil
}

func (s *scanner) skipBlank() {
	for s.pos < len(s.src) && (s.src[s.pos] == ' ' || s.src[s.pos] == '\t') {
		s.pos++
	}
}

// skipComment moves to the newline that ends a comment.
func (s *scanner) skipComment() {
	for s.pos < len(s.src) && s.src[s.pos] != '\n' {
		s.pos++
	}
}

func (s *scanner) atLineEnd() bool {
	return s.pos >= len(s.src) || s.src[s.pos] == '\n' ||
		bytes.HasPrefix(s.src[s.pos:], []byte("\r\n"))
}

// lineEnd takes the rest of a statement's line: blanks, a comment, and the
// newline, which the last line of a file may lack.
func (s *scanner) lineEnd() error {
	s.skipBlank()
	if s.peek() == '#' {
		s.skipComment()
	}
	switch {
	case s.pos >= len(s.src):
	case s.src[s.pos] == '\n':
		s.pos++
	case bytes.HasPrefix(s.src[s.pos:], []byte("\r\n")):
		s.pos += 2
	default:
		return errLayout
	}
	return nil
}

// key reads a dotted key, leaving the scanner after any blanks behind it.
func (s *scanner) key() ([]string, error) {
	var path []string
	for {
		s.skipBlank()
		part, err := s.keyPart()
		if err != nil {
			return nil, err
		}
		path = append(path, part)
		s.skipBlank()
		if s.peek() != '.' {
			return path, nil
		}
		s.pos++
	}
}

func (s *scanner) keyPart() (string, error) {
	start := s.pos
	switch s.peek() {
	case '"', '\'':
		if err := s.str(); err != nil {
			return "", err
		}
		raw := s.src[start+1 : s.pos-1]
		if s.src[start] == '"' && bytes.IndexByte(raw, '\\') >= 0 {
			return unescape(s.src[start:s.pos])
		}
		return string(raw), nil
	}
	for s.pos < len(s.src) && isBareKeyChar(s.src[s.pos]) {
		s.pos++
	}
	if s.pos == start {
		return "", errLayout
	}
	return string(s.src[start:s.pos]), nil
}

// unescape reads a quoted key with escapes in it the way the decoder does,
// by having the decoder read it.
func unescape(quoted []byte) (string, error) {
	var v struct{ K string }
	if _, err := toml.Decode("K = "+string(quoted), &v); err != nil {
		return "", errLayout
	}
	return v.K, nil
}

func isBareKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func (s *scanner) value() error {
	switch s.peek() {
	case '"', '\'':
		return s.str()
	case '[', '{':
		return s.nested()
	}
	start := s.pos
	s.scalar()
	if s.pos == start {
		return errLayout
	}
	// A date and a time may be separated by a space: 1979-05-27 07:32:00.
	if isDate(s.src[start:s.pos]) && s.pos+1 < len(s.src) && s.src[s.pos] == ' ' && isDigit(s.src[s.pos+1]) {
		s.pos++
		s.scalar()
	}
	return nil
}

// scalar takes a number, boolean or date.
func (s *scanner) scalar() {
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case ' ', '\t', '\r', '\n', '#', ',', ']', '}':
			return
		}
		s.pos++
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isDate(b []byte) bool {
	if len(b) != 10 || b[4] != '-' || b[7] != '-' {
		return false
	}
	for i, c := range b {
		if i != 4 && i != 7 && !isDigit(c) {
			return false
		}
	}
	return true
}

// str takes a basic or literal string, on one line or, tripled, on many.
func (s *scanner) str() error {
	q := s.src[s.pos]
	delim := []byte{q, q, q}
	if bytes.HasPrefix(s.src[s.pos:], delim) {
		s.pos += 3
		for s.pos < len(s.src) {
			switch {
			case q == '"' && s.src[s.pos] == '\\':
				s.pos += 2
			case bytes.HasPrefix(s.src[s.pos:], delim):
				s.pos += 3
				// Up to two quotes may sit against the closing delimiter
				// as part of the string: """a""""".
				for i := 0; i < 2 && s.peek() == q; i++ {
					s.pos++
				}
				return nil
			default:
				s.pos++
			}
		}
		return errLayout
	}
	s.pos++
	for s.pos < len(s.src) {
		switch c := s.src[s.pos]; {
		case c == '\n':
			return errLayout
		case q == '"' && c == '\\':
			s.pos += 2
		case c == q:
			s.pos++
			return nil
		default:
			s.pos++
		}
	}
	return errLayout
}

// nested takes an array or inline table, which may hold strings, comments
// and newlines.
func (s *scanner) nested() error {
	depth := 0
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case '[', '{':
			depth++
			s.pos++
		case ']', '}':
			depth--
			s.pos++
			if depth == 0 {
				return nil
			}
		case '"', '\'':
			if err := s.str(); err != nil {
				return err
			}
		case '#':
			s.skipComment()
		default:
			s.pos++
		}
	}
	return errLayout
}
