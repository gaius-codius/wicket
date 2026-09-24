package importer

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestParseRDP_Work(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "work.rdp"))
	if err != nil {
		t.Fatal(err)
	}
	p, skip, err := ParseRDP(data, "work")
	if err != nil || skip.Reason != "" {
		t.Fatalf("err=%v skip=%v", err, skip)
	}
	if p.Host != "rdp.example.com:3390" || p.User != "jdoe" || p.Domain != "CORP" {
		t.Fatalf("profile %+v", p)
	}
	if !p.Fullscreen || p.Size != "1600x900" {
		t.Fatalf("display %+v", p)
	}
}

func TestParseRDP_IgnoresPasswordBlob(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "work.rdp"))
	if err != nil {
		t.Fatal(err)
	}
	vals, err := parseRDP(data)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range vals {
		if strings.Contains(strings.ToLower(k), "password") {
			t.Fatalf("password key kept: %s=%s", k, v)
		}
		if strings.Contains(v, "DEADBEEF") || strings.Contains(v, "01000000") {
			t.Fatalf("password blob leaked into %s", k)
		}
	}
}

func TestParseRDP_PlainDomain(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "plain.rdp"))
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := ParseRDP(data, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if p.User != "alice" || p.Domain != "CORP" || p.Fullscreen || p.Size != "" {
		t.Fatalf("%+v", p)
	}
}

func TestParseRDP_ZeroSizeIgnored(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "nosize.rdp"))
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := ParseRDP(data, "nosize")
	if err != nil {
		t.Fatal(err)
	}
	if p.Size != "" {
		t.Fatalf("size %q", p.Size)
	}
}

func TestParseRDPFiles_HardFailureWhenNoneReadable(t *testing.T) {
	t.Parallel()
	_, skipped, err := ParseRDPFiles([]string{filepath.Join(t.TempDir(), "missing.rdp")})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped %+v", skipped)
	}
}

// The Windows client saves .rdp files as UTF-16LE with a byte-order mark;
// other tools write UTF-8, sometimes with one.
func TestParseRDP_Encodings(t *testing.T) {
	t.Parallel()
	const doc = "full address:s:srv.example\r\nusername:s:CORP\\bob\r\n"
	utf16With := func(order binary.AppendByteOrder, bom []byte) []byte {
		b := append([]byte(nil), bom...)
		for _, u := range utf16.Encode([]rune(doc)) {
			b = order.AppendUint16(b, u)
		}
		return b
	}
	for name, data := range map[string][]byte{
		"utf-16le":  utf16With(binary.LittleEndian, []byte{0xFF, 0xFE}),
		"utf-16be":  utf16With(binary.BigEndian, []byte{0xFE, 0xFF}),
		"utf-8 bom": append([]byte{0xEF, 0xBB, 0xBF}, doc...),
		"utf-8":     []byte(doc),
	} {
		p, _, err := ParseRDP(data, "srv")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.Host != "srv.example" || p.User != "bob" || p.Domain != "CORP" {
			t.Errorf("%s: %+v", name, p)
		}
	}
}
