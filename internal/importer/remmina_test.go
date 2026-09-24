package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRemmina_Work(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "work.remmina"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "IGNORE_ME") {
		// Fixture still has the key; the parser must not keep the value.
	}
	p, skip, err := ParseRemmina(data, "work.remmina")
	if err != nil {
		t.Fatal(err)
	}
	if skip.Reason != "" {
		t.Fatalf("skip %v", skip)
	}
	if p.Name != "work" || p.Host != "192.168.1.20:3389" || p.User != "jdoe" || p.Domain != "CORP" {
		t.Fatalf("profile %+v", p)
	}
	if !p.Fullscreen || p.Size != "1920x1080" {
		t.Fatalf("display %+v", p)
	}
	if p.Scale != 100 || !p.DynamicResolution || !p.Clipboard {
		t.Fatalf("defaults %+v", p)
	}
	if p.Multimon {
		t.Fatal("multimon must stay off")
	}
}

func TestParseRemmina_DomainInUsername(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "lab.remmina"))
	if err != nil {
		t.Fatal(err)
	}
	p, skip, err := ParseRemmina(data, "lab.remmina")
	if err != nil || skip.Reason != "" {
		t.Fatalf("err=%v skip=%v", err, skip)
	}
	if p.User != "alice" || p.Domain != "CORP" {
		t.Fatalf("got user=%q domain=%q", p.User, p.Domain)
	}
	if p.Fullscreen || p.Size != "" {
		t.Fatalf("expected windowed empty size, got fullscreen=%v size=%q", p.Fullscreen, p.Size)
	}
}

func TestParseRemmina_SkipsNonRDP(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "old-vnc.remmina"))
	if err != nil {
		t.Fatal(err)
	}
	_, skip, err := ParseRemmina(data, "old-vnc.remmina")
	if err != nil {
		t.Fatal(err)
	}
	if skip.Name != "old-vnc" || !strings.Contains(skip.Reason, "not RDP") {
		t.Fatalf("skip %+v", skip)
	}
}

func TestParseRemmina_IgnoresPasswordValue(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "work.remmina"))
	if err != nil {
		t.Fatal(err)
	}
	vals, saw, err := parseINISection(data, "remmina")
	if err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("expected password key detected")
	}
	if _, ok := vals["password"]; ok {
		t.Fatal("password value must not be stored")
	}
	if strings.Contains(vals["server"], "IGNORE") {
		t.Fatal("password leaked into another field")
	}
}

func TestParseRemminaDir(t *testing.T) {
	t.Parallel()
	ok, skipped, err := ParseRemminaDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(ok) < 3 {
		t.Fatalf("imported %d: %+v", len(ok), ok)
	}
	var sawVNC bool
	for _, s := range skipped {
		if s.Name == "old-vnc" {
			sawVNC = true
		}
	}
	if !sawVNC {
		t.Fatalf("expected old-vnc skip, got %+v", skipped)
	}
}
