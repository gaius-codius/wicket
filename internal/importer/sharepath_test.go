package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
)

// rdpRefused parses drivestoredirect v and returns what it kept and refused.
func rdpRefused(t *testing.T, v string) ([]config.Share, []config.DroppedShare) {
	t.Helper()
	p, _, err := ParseRDP([]byte("full address:s:h\ndrivestoredirect:s:"+v+"\n"), "x")
	if err != nil {
		t.Fatal(err)
	}
	return p.Shares, p.Refused
}

// A folder whose direct entry links to home or to / is refused, as a Wine
// prefix's dosdevices is: FreeRDP follows the link, and the server would
// browse through it. The summary names the link.
func TestShares_RefusesLinksOutOfTheShare(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{home, "/"} {
		dosdevices := filepath.Join(t.TempDir(), "dosdevices")
		if err := os.Mkdir(dosdevices, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dosdevices, "z:")); err != nil {
			t.Fatal(err)
		}
		shares, refused := rdpRefused(t, dosdevices)
		if len(shares) != 0 || len(refused) != 1 || !strings.Contains(refused[0].Reason, "z:") {
			t.Errorf("link to %s: shares %+v, refused %+v", target, shares, refused)
		}
	}
}

// One folder spelled with and without a trailing slash is one share, in
// either format, and one refusal.
func TestShares_TrailingSlashIsOneShare(t *testing.T) {
	t.Parallel()
	want := []config.Share{{Path: "/srv/wicket-dup"}}
	if shares, _ := rdpRefused(t, "/srv/wicket-dup;/srv/wicket-dup/"); !reflect.DeepEqual(shares, want) {
		t.Errorf("rdp: shares %+v, want %+v", shares, want)
	}
	if _, refused := rdpRefused(t, "/proc;/proc/"); !reflect.DeepEqual(refused, []config.DroppedShare{{Path: "/proc", Reason: refusedProc}}) {
		t.Errorf("rdp: refused %+v", refused)
	}
	doc := "[remmina]\nname=r\nprotocol=RDP\nserver=h\nusername=u\nsharefolder=/srv/wicket-dup\ndrive=/srv/wicket-dup/\n"
	p, _, err := ParseRemmina([]byte(doc), "r.remmina")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Shares, want) {
		t.Errorf("remmina: shares %+v, want %+v", p.Shares, want)
	}
}
