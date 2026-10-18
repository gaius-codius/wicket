package importer

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gaius-codius/wicket/internal/config"
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
	for _, name := range []string{"work.rdp", "drives.rdp"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		vals, err := parseRDP(data)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range vals {
			if strings.Contains(strings.ToLower(k), "password") {
				t.Fatalf("%s: password key kept: %s=%s", name, k, v)
			}
			for _, secret := range []string{"DEADBEEF", "01000000", "SECRETBLOB", "GATEWAYSECRET"} {
				if strings.Contains(v, secret) {
					t.Fatalf("%s: password leaked into %s", name, k)
				}
			}
		}
		p, _, err := ParseRDP(data, "x")
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%#v", p); strings.Contains(got, "SECRET") || strings.Contains(got, "DEADBEEF") {
			t.Fatalf("%s: password on the profile: %s", name, got)
		}
	}
}

// redirectclipboard turns the clipboard off only when the file says so; a
// file without it, or with a value that is not a number, keeps the default.
func TestParseRDP_Clipboard(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"redirectclipboard:i:0\n", false},
		{"redirectclipboard:i: 0\n", false},
		{"redirectclipboard:i:1\n", true},
		{"", true},
		{"redirectclipboard:i:off\n", true},
	} {
		p, _, err := ParseRDP([]byte("full address:s:h\nusername:s:u\n"+tc.line), "x")
		if err != nil {
			t.Fatal(err)
		}
		if p.Clipboard != tc.want {
			t.Errorf("%q: clipboard %v, want %v", tc.line, p.Clipboard, tc.want)
		}
	}
}

// use multimon spans every monitor, and Wicket does that only full screen,
// so it turns both on whatever screen mode id says.
func TestParseRDP_Multimon(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		lines                string
		multimon, fullscreen bool
	}{
		{"use multimon:i:1\n", true, true},
		{"use multimon:i:1\nscreen mode id:i:1\n", true, true},
		{"use multimon:i:0\nscreen mode id:i:2\n", false, true},
		{"use multimon:i:0\n", false, false},
		{"use multimon:i:yes\n", false, false},
		{"", false, false},
	} {
		p, _, err := ParseRDP([]byte("full address:s:h\nusername:s:u\n"+tc.lines), "x")
		if err != nil {
			t.Fatal(err)
		}
		if p.Multimon != tc.multimon || p.Fullscreen != tc.fullscreen {
			t.Errorf("%q: multimon %v fullscreen %v, want %v %v", tc.lines, p.Multimon, p.Fullscreen, tc.multimon, tc.fullscreen)
		}
	}
}

// drives.rdp names folders in each form FreeRDP reads, among entries that are
// not one local folder and are refused with the reason: "*", DynamicDrives,
// "%", a drive letter, a relative path and a path with a comma. An empty
// entry names nothing, so it is passed over without a note.
func TestParseRDP_Drives(t *testing.T) {
	t.Parallel()
	p, skip, err := ParseRDPFile(filepath.Join("testdata", "drives.rdp"))
	if err != nil || skip.Reason != "" {
		t.Fatalf("err=%v skip=%v", err, skip)
	}
	if p.Clipboard {
		t.Fatal("clipboard on, want off")
	}
	if p.ShareHome {
		t.Fatal("share_home turned on by %")
	}
	want := []config.Share{
		{Path: "/tmp/wicket-rdp-plain"},
		{Path: "/tmp/wicket-rdp-docs", Name: "docs"},
		{Path: "~/wicket-rdp-proj", Name: "projects"},
		{Path: "/tmp/wicket-rdp-nolabel"},
	}
	if !reflect.DeepEqual(p.Shares, want) {
		t.Fatalf("shares %+v, want %+v", p.Shares, want)
	}
	wantRefused := []config.DroppedShare{
		{Path: "*", Reason: refusedAllDrive},
		{Path: "DynamicDrives", Reason: refusedAllDrive},
		{Path: "%", Reason: refusedHome},
		{Path: `C:\`, Reason: refusedNotPath},
		{Path: "relative/dir", Reason: refusedNotPath},
		{Path: "/tmp/a,b", Reason: refusedComma},
	}
	if !reflect.DeepEqual(p.Refused, wantRefused) {
		t.Fatalf("refused %+v, want %+v", p.Refused, wantRefused)
	}
	ps := []config.Import{p}
	assertImports(t, ps)
}

// None of these is one folder an import may share: the whole home folder
// and the root are refused however they are written.
func TestParseRDP_NoDrives(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	drives := []string{
		"", "*", "DynamicDrives", "%", "C:\\;D:\\", "docs(relative)", "(x)(y)",
		"~", "~/", "~/.", "home(~)", "~(home)", "/", "/..", "/(root)", "//", home, home + "/", "h(" + home + ")",
		"~/..", "a(~/..)", filepath.Dir(home), "/proc", "/proc/self/root", "/proc/self/cwd",
		// A symlink into /proc on Linux; on macOS a folder of its own, as
		// changeable.
		"/dev/fd", "/dev/fd/0",
	}
	for _, v := range drives {
		p, _, err := ParseRDP([]byte("full address:s:h\ndrivestoredirect:s:"+v+"\n"), "x")
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Shares) != 0 || p.ShareHome {
			t.Errorf("%q: shares %+v share_home %v", v, p.Shares, p.ShareHome)
		}
		// Each is named in the summary, but for the empty entry, which
		// names nothing.
		if (len(p.Refused) == 0) != (v == "") {
			t.Errorf("%q: refused %+v", v, p.Refused)
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

func TestParseRDP_ServerPort(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ addr, port, want string }{
		{"srv3", "3390", "srv3:3390"},
		{"srv3:3391", "3390", "srv3:3391"},
		{"srv3", "3389", "srv3"},
		{"srv3", "", "srv3"},
		{"fe80::1", "3390", "[fe80::1]:3390"},
		{"fe80::1", "", "[fe80::1]"},
		{"fe80::1", "3389", "[fe80::1]"},
		{"[fe80::1]", "3390", "[fe80::1]:3390"},
		{"[fe80::1]:3391", "3390", "[fe80::1]:3391"},
		{"srv3", "0", "srv3"},
		{"srv3", "65536", "srv3"},
		{"srv3", "-1", "srv3"},
		{"srv3", "+3390", "srv3"},
		{"srv3", "33x", "srv3"},
		{"srv3", " 3390 ", "srv3:3390"},
		{"", "3390", ""},
	} {
		doc := "full address:s:" + tc.addr + "\n"
		if tc.port != "" {
			doc += "server port:i:" + tc.port + "\n"
		}
		p, _, err := ParseRDP([]byte(doc), "srv")
		if err != nil {
			t.Fatal(err)
		}
		if p.Host != tc.want {
			t.Errorf("full address %q, server port %q: host %q, want %q", tc.addr, tc.port, p.Host, tc.want)
		}
	}
}

func TestParseRDP_PortFixture(t *testing.T) {
	t.Parallel()
	p, _, err := ParseRDPFile(filepath.Join("testdata", "port.rdp"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Host != "srv3:3390" {
		t.Fatalf("host %q", p.Host)
	}
}

// A file with no user, and one with a UPN user beside a domain, used to be
// skipped at import. Both now import: the first with no user for the TUI to
// ask for, the second keeping the UPN and dropping the domain it repeats.
func TestParseRDP_NoUserAndUPN(t *testing.T) {
	t.Parallel()
	var ps []config.Import
	for _, name := range []string{"nouser", "upn"} {
		p, _, err := ParseRDPFile(filepath.Join("testdata", name+".rdp"))
		if err != nil {
			t.Fatal(err)
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

// Only user@suffix is a UPN. A user that merely holds an "@" keeps its
// domain, so the pair is still refused rather than imported without it.
func TestParseRDP_DegenerateUPNKeepsDomain(t *testing.T) {
	t.Parallel()
	for _, user := range []string{"@", "alice@", "@corp.com"} {
		p, _, err := ParseRDP([]byte("full address:s:h\nusername:s:"+user+"\ndomain:s:CORP\n"), "x")
		if err != nil {
			t.Fatal(err)
		}
		if p.Domain != "CORP" {
			t.Errorf("user %q: domain %q, want CORP kept", user, p.Domain)
		}
	}
}

// assertImports checks that every profile survives the import plan rather
// than being skipped, and that the plan drops no share of its own: the only
// ones it reports are those the parser refused.
func assertImports(t *testing.T, ps []config.Import) {
	t.Helper()
	refused := 0
	for i := range ps {
		ps[i].Client = config.DefaultClient
		refused += len(ps[i].Refused)
	}
	ok, skipped, dropped := config.PlanProfiles(nil, ps, false)
	if len(skipped) != 0 || len(ok) != len(ps) || len(dropped) != refused {
		t.Fatalf("imported %+v, skipped %+v, dropped %+v", ok, skipped, dropped)
	}
}

// path(path) names two folders, and both are shared, as FreeRDP shares them.
func TestParseRDP_TwoPathDrive(t *testing.T) {
	t.Parallel()
	p, _, err := ParseRDP([]byte("full address:s:h\ndrivestoredirect:s:/tmp/wicket-a(/tmp/wicket-b)\n"), "x")
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Share{{Path: "/tmp/wicket-a"}, {Path: "/tmp/wicket-b"}}
	if !reflect.DeepEqual(p.Shares, want) {
		t.Fatalf("shares %+v, want %+v", p.Shares, want)
	}
}

// A share that is a symlink to the home folder is refused like home itself.
func TestParseRDP_DriveSymlinkToHome(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	p, _, err := ParseRDP([]byte("full address:s:h\ndrivestoredirect:s:"+link+";/srv/wicket-kept("+filepath.Dir(home)+")\n"), "x")
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Share{{Path: "/srv/wicket-kept"}}
	if !reflect.DeepEqual(p.Shares, want) {
		t.Fatalf("shares %+v, want %+v", p.Shares, want)
	}
	wantRefused := []config.DroppedShare{
		{Path: link, Reason: refusedHome},
		{Path: filepath.Dir(home), Reason: refusedHome},
	}
	if !reflect.DeepEqual(p.Refused, wantRefused) {
		t.Fatalf("refused %+v, want %+v", p.Refused, wantRefused)
	}
}
