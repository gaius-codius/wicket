package importer

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
)

// FuzzParseRDP checks that no .rdp file, the main untrusted input, makes the
// parser panic, and that what it keeps holds checkImport's rules.
func FuzzParseRDP(f *testing.F) {
	addSeeds(f, "*.rdp")
	f.Add([]byte("full address:s:h\ndrivestoredirect:s:/a(/b);x(~/y);/c/;*;%\n"))
	f.Add([]byte("\xff\xfef\x00u\x00l\x00l\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		in, skip, err := ParseRDP(data, "f.rdp")
		if err != nil || skip.Reason != "" {
			return
		}
		checkImport(t, in)
	})
}

// FuzzParseRemmina is FuzzParseRDP for Remmina files.
func FuzzParseRemmina(f *testing.F) {
	addSeeds(f, "*.remmina")
	f.Add([]byte("[remmina]\nprotocol=RDP\nserver=h\nsharefolder=/a/\ndrive=x,~/y;/b;c,d\\n\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		in, skip, err := ParseRemmina(data, "f.remmina")
		if err != nil || skip.Reason != "" {
			return
		}
		checkImport(t, in)
	})
}

func addSeeds(f *testing.F, glob string) {
	f.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", glob))
	if err != nil || len(files) == 0 {
		f.Fatalf("no %s seeds: %v", glob, err)
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
}

// checkImport holds what an import keeps to the rules the parsers promise:
// every share it keeps is one the share rules allow as written, its shares
// never cost a profile its import, and what the plan saves passes the shape
// checks a loaded config makes and comes from the file.
func checkImport(t *testing.T, in config.Import) {
	t.Helper()
	for _, s := range in.Shares {
		if reason := shareRefusal(s.Path); reason != "" {
			t.Fatalf("kept share %q, which the rules refuse: %s", s.Path, reason)
		}
	}
	in.Client = config.DefaultClient
	bare := in
	bare.Shares = nil
	if ok, _, _ := config.PlanProfiles(nil, []config.Import{bare}, false); len(ok) == 0 {
		return
	}
	ok, skipped, _ := config.PlanProfiles(nil, []config.Import{in}, false)
	if len(ok) != 1 {
		t.Fatalf("shares %+v cost the profile its import: %+v", in.Shares, skipped)
	}
	if err := config.ValidateProfile(ok[0]); err != nil {
		t.Fatalf("saved profile does not load: %v", err)
	}
	for _, s := range ok[0].Shares {
		if !slices.ContainsFunc(in.Shares, func(k config.Share) bool { return k.Path == s.Path }) {
			t.Fatalf("saved share %q is not one the parser kept: %+v", s.Path, in.Shares)
		}
	}
}
