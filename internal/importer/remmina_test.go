package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gaius-codius/wicket/internal/config"
)

func TestParseRemmina_Work(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "work.remmina"))
	if err != nil {
		t.Fatal(err)
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
	vals, err := parseINISection(data, "remmina")
	if err != nil {
		t.Fatal(err)
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

// Remmina writes through GKeyFile, which escapes a backslash; lab.remmina
// holds "CORP\\alice" as Remmina saves it.
func TestUnescapeKeyFile(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		`CORP\\alice`:  `CORP\alice`,
		`\sa\tb\nc\rd`: " a\tb\nc\rd",
		`odd\q`:        `odd\q`,
		`trail\`:       `trail\`,
		"plain":        "plain",
	} {
		if got := unescapeKeyFile(in); got != want {
			t.Errorf("unescapeKeyFile(%q) = %q, want %q", in, got, want)
		}
	}
}

// The size is imported only for Remmina's custom resolution mode, or from a
// file older than resolution_mode that has both values.
func TestParseRemmina_ResolutionMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode, want string
	}{
		{"", "1280x720"},
		{"resolution_mode=0\n", "1280x720"},
		{"resolution_mode=1\n", ""},
		{"resolution_mode=2\n", ""},
	} {
		doc := "[remmina]\nname=r\nprotocol=RDP\nserver=h\n" + tc.mode +
			"resolution_width=1280\nresolution_height=720\n"
		p, _, err := ParseRemmina([]byte(doc), "r.remmina")
		if err != nil {
			t.Fatal(err)
		}
		if p.Size != tc.want {
			t.Errorf("%q: size %q, want %q", tc.mode, p.Size, tc.want)
		}
	}
}

func TestParseRemmina_Shares(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/shares.remmina")
	if err != nil {
		t.Fatal(err)
	}
	p, skip, err := ParseRemmina(data, "shares.remmina")
	if err != nil || skip.Reason != "" {
		t.Fatalf("err %v skip %#v", err, skip)
	}
	want := []config.Share{
		{Path: "/tmp/wicket-remmina-single"},
		{Path: "/tmp/wicket-remmina-docs", Name: "docs"},
		{Path: "/tmp/wicket-remmina-proj", Name: "projects"},
	}
	if !reflect.DeepEqual(p.Shares, want) {
		t.Fatalf("got %+v want %+v", p.Shares, want)
	}
}

func TestParseRemmina_SharesSkipsRelativeAndComma(t *testing.T) {
	t.Parallel()
	data := []byte(`[remmina]
name=shares
protocol=RDP
server=share.example
username=bob
sharefolder=Documents
drive=docs,/tmp/ok;bad,/tmp/a,/tmp/b;C:/windows
`)
	p, skip, err := ParseRemmina(data, "shares.remmina")
	if err != nil || skip.Reason != "" {
		t.Fatalf("err %v skip %#v", err, skip)
	}
	want := []config.Share{{Path: "/tmp/ok", Name: "docs"}}
	if !reflect.DeepEqual(p.Shares, want) {
		t.Fatalf("got %+v want %+v", p.Shares, want)
	}
	wantRefused := []config.DroppedShare{
		{Path: "Documents", Reason: refusedNotPath},
		{Path: "/tmp/a,/tmp/b", Reason: refusedComma},
		{Path: "C:/windows", Reason: refusedNotPath},
	}
	if !reflect.DeepEqual(p.Refused, wantRefused) {
		t.Fatalf("refused %+v, want %+v", p.Refused, wantRefused)
	}
}

// A share holding the home folder is refused with the reason, once however
// often the file names it, and a repeat of a kept share goes without a note.
func TestParseRemmina_RefusedHomeShare(t *testing.T) {
	t.Parallel()
	data := []byte("[remmina]\nname=h\nprotocol=RDP\nserver=h\nusername=u\n" +
		"sharefolder=~\ndrive=home,~;/tmp/ok;ok,/tmp/ok;/proc/self/cwd\n")
	p, _, err := ParseRemmina(data, "h.remmina")
	if err != nil {
		t.Fatal(err)
	}
	if want := []config.Share{{Path: "/tmp/ok"}, {Path: "/tmp/ok", Name: "ok"}}; !reflect.DeepEqual(p.Shares, want) {
		t.Fatalf("shares %+v, want %+v", p.Shares, want)
	}
	want := []config.DroppedShare{
		{Path: "~", Reason: refusedHome},
		{Path: "/proc/self/cwd", Reason: refusedProc},
	}
	if !reflect.DeepEqual(p.Refused, want) {
		t.Fatalf("refused %+v, want %+v", p.Refused, want)
	}
}

func TestParseRemmina_NoUserAndUPN(t *testing.T) {
	t.Parallel()
	var ps []config.Import
	for _, name := range []string{"nouser", "upn"} {
		p, skip, err := ParseRemminaFile(filepath.Join("testdata", name+".remmina"))
		if err != nil || skip.Reason != "" {
			t.Fatalf("%s: err=%v skip=%v", name, err, skip)
		}
		ps = append(ps, p)
	}
	if ps[0].User != "" || ps[0].Domain != "" {
		t.Fatalf("nouser: %+v", ps[0])
	}
	if ps[1].User != "alice@corp.com" || ps[1].Domain != "" {
		t.Fatalf("upn: %+v", ps[1])
	}
	assertImports(t, ps)
}

// disableclipboard=1 turns the clipboard off; a file without the key, or
// with a value Remmina would not write, keeps it on.
func TestParseRemmina_Clipboard(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"disableclipboard=1\n", false},
		{"disableclipboard=true\n", false},
		{"disableclipboard=0\n", true},
		{"", true},
		{"disableclipboard=maybe\n", true},
	} {
		doc := "[remmina]\nname=r\nprotocol=RDP\nserver=h\nusername=u\n" + tc.line
		p, _, err := ParseRemmina([]byte(doc), "r.remmina")
		if err != nil {
			t.Fatal(err)
		}
		if p.Clipboard != tc.want {
			t.Errorf("%q: clipboard %v, want %v", tc.line, p.Clipboard, tc.want)
		}
	}
}

// noclip.remmina has the clipboard off beside a password and a gateway
// password, neither of which may reach the profile.
func TestParseRemmina_NoClipboardNoPassword(t *testing.T) {
	t.Parallel()
	p, skip, err := ParseRemminaFile(filepath.Join("testdata", "noclip.remmina"))
	if err != nil || skip.Reason != "" {
		t.Fatalf("err=%v skip=%v", err, skip)
	}
	if p.Clipboard {
		t.Fatal("clipboard on, want off")
	}
	if got := fmt.Sprintf("%#v", p); strings.Contains(got, "SECRET") {
		t.Fatalf("password on the profile: %s", got)
	}
	assertImports(t, []config.Import{p})
}

// A DOMAIN\user beside a domain key is split like the .rdp importer splits
// it: the user keeps its bare name and the domain key wins.
func TestParseRemmina_DomainUserBesideDomain(t *testing.T) {
	t.Parallel()
	p, skip, err := ParseRemminaFile(filepath.Join("testdata", "otherdomain.remmina"))
	if err != nil || skip.Reason != "" {
		t.Fatalf("err=%v skip=%v", err, skip)
	}
	if p.User != "alice" || p.Domain != "CORP" {
		t.Fatalf("user %q domain %q, want alice and CORP", p.User, p.Domain)
	}
	r, _, err := ParseRDP([]byte("full address:s:h\nusername:s:OTHER\\alice\ndomain:s:CORP\n"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if r.User != p.User || r.Domain != p.Domain {
		t.Fatalf(".rdp made %q %q of the same pair", r.User, r.Domain)
	}
	assertImports(t, []config.Import{p})
}

// Remmina's home or root folder is not imported as a share either.
func TestParseRemmina_SharesSkipHomeAndRoot(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"sharefolder=~\n", "sharefolder=/\n", "drive=home,~/\n", "drive=/\n"} {
		doc := "[remmina]\nname=r\nprotocol=RDP\nserver=h\nusername=u\n" + v
		p, _, err := ParseRemmina([]byte(doc), "r.remmina")
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Shares) != 0 {
			t.Errorf("%q: shares %+v", v, p.Shares)
		}
	}
}
