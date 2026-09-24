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
	WriteSummary(&buf, []config.Profile{{Name: "work"}, {Name: "lab"}}, []Skip{
		{Name: "old-vnc", Reason: "not RDP"},
		{Name: "bad-host", Reason: "host: invalid character"},
	})
	got := buf.String()
	if !strings.Contains(got, "imported 2: work, lab\n") {
		t.Fatalf("imported line: %q", got)
	}
	if !strings.Contains(got, "skipped 2: old-vnc (not RDP), bad-host (host: invalid character)\n") {
		t.Fatalf("skipped line: %q", got)
	}
	if !strings.Contains(got, passwordNotice) {
		t.Fatalf("missing password notice: %q", got)
	}
}
