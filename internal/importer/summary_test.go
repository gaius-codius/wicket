package importer

import (
	"bytes"
	"strings"
	"testing"
	"unicode"

	"github.com/gaius-codius/wicket/internal/config"
)

func TestWriteSummary(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	WriteSummary(&buf, []config.Profile{
		{Name: "work", User: "jdoe", Domain: "CORP", Clipboard: true},
		{Name: "lab", User: "alice@corp.com", Clipboard: true},
		{Name: "avd", Clipboard: true},
		{Name: "team", Domain: "CORP", Clipboard: true},
	}, nil, []Skip{
		{Name: "old-vnc", Reason: "not RDP"},
		{Name: "bad-host", Reason: "host: invalid character"},
	})
	got := buf.String()
	if !strings.Contains(got, `imported 4: work (CORP\jdoe), lab (alice@corp.com), avd (no user), team (no user, domain CORP)`+"\n") {
		t.Fatalf("imported line: %q", got)
	}
	if !strings.Contains(got, noUserNotice) {
		t.Fatalf("missing no-user notice: %q", got)
	}
	if !strings.Contains(got, "skipped 2: old-vnc (not RDP), bad-host (host: invalid character)\n") {
		t.Fatalf("skipped line: %q", got)
	}
	if !strings.Contains(got, passwordNotice) {
		t.Fatalf("missing password notice: %q", got)
	}
}

func TestWriteSummary_NoNoticeWhenEveryUserSet(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	WriteSummary(&buf, []config.Profile{{Name: "work", User: "jdoe", Clipboard: true}}, nil, nil)
	if strings.Contains(buf.String(), noUserNotice) {
		t.Fatalf("no-user notice with every user set: %q", buf.String())
	}
}

// A dry run shows what an import carried over: the clipboard when it is off,
// and each shared folder with its name.
func TestWriteSummary_ClipboardAndShares(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	WriteSummary(&buf, []config.Profile{{
		Name: "files", User: "bob",
		Shares: []config.Share{{Path: "/tmp/a"}, {Path: "~/docs", Name: "docs"}},
	}}, nil, nil)
	want := `imported 1: files (bob; clipboard off; share "/tmp/a"; share "~/docs" as docs)` + "\n"
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

// A skipped entry's name and reason come from the file, and must not reach
// the terminal with their control characters.
func TestWriteSummary_EscapesControlCharacters(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	WriteSummary(&buf, []config.Profile{{Name: "ok\x1b[2J", User: "u\x07", Clipboard: true}}, []config.DroppedShare{
		{Profile: "ok\x1b[2J", Path: "/tmp/a\x1b[2Jb\u202e", Reason: "path must not contain control characters"},
	}, []Skip{
		{Name: "evil\x1b[2J"},
		{Name: "bad\u009b31m", Reason: "host \x1b]0;title\x07 refused\r"},
		{Name: "raw\xff"},
		{Name: "flip\u202etxt.exe"},
	})
	got := buf.String()
	if strings.ContainsFunc(strings.TrimSuffix(got, "\n"), func(r rune) bool { return r != '\n' && unicode.IsControl(r) }) {
		t.Fatalf("control character in %q", got)
	}
	if !strings.Contains(got, "skipped 4: evil?[2J, bad?31m (host ?]0;title? refused?), raw\uFFFD, flip?txt.exe\n") {
		t.Fatalf("skipped line %q", got)
	}
	if !strings.Contains(got, "imported 1: ok?[2J (u?)\n") {
		t.Fatalf("imported line %q", got)
	}
	if !strings.Contains(got, `ok?[2J: share "/tmp/a\x1b[2Jb\u202e" dropped: path must not contain control characters`+"\n") {
		t.Fatalf("dropped line %q", got)
	}
}

// A share the import left out is named after the imported line, with its
// reason, so the summary is not read as every folder having come across.
func TestWriteSummary_DroppedShares(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	WriteSummary(&buf, []config.Profile{{Name: "files", User: "bob", Clipboard: true, Shares: []config.Share{{Path: "/tmp/a"}}}},
		[]config.DroppedShare{{Profile: "files", Path: "/tmp/a\nb", Reason: "path must not contain control characters"}}, nil)
	want := `imported 1: files (bob; share "/tmp/a")` + "\n" +
		`files: share "/tmp/a\nb" dropped: path must not contain control characters` + "\n"
	if !strings.HasPrefix(buf.String(), want) {
		t.Fatalf("got %q, want prefix %q", buf.String(), want)
	}
}
