package importer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
)

func TestWriteSummary(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	WriteSummary(&buf, []config.Profile{
		{Name: "work", User: "jdoe", Domain: "CORP"},
		{Name: "lab", User: "alice@corp.com"},
		{Name: "avd"},
	}, []Skip{
		{Name: "old-vnc", Reason: "not RDP"},
		{Name: "bad-host", Reason: "host: invalid character"},
	})
	got := buf.String()
	if !strings.Contains(got, `imported 3: work (CORP\jdoe), lab (alice@corp.com), avd (no user)`+"\n") {
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
	WriteSummary(&buf, []config.Profile{{Name: "work", User: "jdoe"}}, nil)
	if strings.Contains(buf.String(), noUserNotice) {
		t.Fatalf("no-user notice with every user set: %q", buf.String())
	}
}
