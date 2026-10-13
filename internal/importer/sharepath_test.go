package importer

import (
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
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

// On macOS the home folder answers to other spellings than its own: any
// case on a case-insensitive volume, and its name under the data volume,
// through a firmlink, which following symlinks does not undo. Each is
// refused, for home and for the folder above it. It runs against the real
// file system, so only on macOS; TestHoldsHome_DataVolume covers the names
// everywhere.
func TestShares_HomeAliasesOnDarwin(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("macOS file systems only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	checkHomeAliases(t, home)
}

// TestShares_HomeAliasesOnDarwin's sandboxed home is under /var/folders;
// this is the account's own, under /Users, the case #100 names. Only stat
// is used on it. Not parallel: it sets HOME.
func TestShares_AccountHomeAliasesOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS file systems only")
	}
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skipf("no account home: %v", err)
	}
	t.Setenv("HOME", u.HomeDir)
	checkHomeAliases(t, u.HomeDir)
}

func checkHomeAliases(t *testing.T, home string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	var aliases []string
	if _, err := os.Stat(strings.ToUpper(home)); err == nil {
		aliases = append(aliases, strings.ToUpper(home), strings.ToLower(filepath.Dir(home)))
	} else {
		t.Logf("volume is case-sensitive; case variants not checked: %v", err)
	}
	if _, err := os.Stat(dataVolume + resolved); err == nil {
		aliases = append(aliases, dataVolume+resolved, dataVolume+filepath.Dir(resolved), dataVolume)
	} else {
		t.Logf("no data volume alias of %s: %v", resolved, err)
	}
	if len(aliases) == 0 {
		t.Skip("no alias of home on this system")
	}
	for _, a := range aliases {
		shares, refused := rdpRefused(t, a)
		if len(shares) != 0 || !reflect.DeepEqual(refused, []config.DroppedShare{{Path: a, Reason: refusedHome}}) {
			t.Errorf("%s: shares %+v, refused %+v", a, shares, refused)
		}
	}
}

// A folder that holds home at the place / does is refused, as / is: in a
// toolbox /run/host is the host's root. A tree of links stands in for one
// here, and so does a folder holding home where home's parent does.
func TestShares_RefusesMirrorOfRoot(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	mirror := t.TempDir()
	at := filepath.Join(mirror, home)
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, at); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{mirror, filepath.Dir(at)} {
		shares, refused := rdpRefused(t, p)
		if len(shares) != 0 || !reflect.DeepEqual(refused, []config.DroppedShare{{Path: p, Reason: refusedHome}}) {
			t.Errorf("%s: shares %+v, refused %+v", p, shares, refused)
		}
	}
	// What holds no home at that place is kept.
	if shares, _ := rdpRefused(t, filepath.Join(mirror, "tmp-other")); len(shares) != 1 {
		t.Errorf("unrelated folder: shares %+v", shares)
	}
}

// The data volume names of home and the folders above it are refused
// wherever the check runs as macOS's, though nothing there exists here.
func TestHoldsHome_DataVolume(t *testing.T) {
	t.Parallel()
	names := homeNames("/Users/alice", true)
	for _, p := range []string{
		"/Users/alice", "/Users", "/",
		dataVolume + "/Users/alice", dataVolume + "/Users", dataVolume, "/System/Volumes", "/System",
	} {
		if !holdsHome(p, names) {
			t.Errorf("%s: kept", p)
		}
	}
	for _, p := range []string{"/Users/alice/docs", "/Users/bob", dataVolume + "/Users/bob", dataVolume + "/Users/alice/docs"} {
		if holdsHome(p, names) {
			t.Errorf("%s: refused", p)
		}
	}
	if holdsHome(dataVolume, homeNames("/Users/alice", false)) {
		t.Error("data volume refused off macOS")
	}
}

// A link whose target passes through /proc or /dev/fd is refused, though
// following it to the end leads elsewhere: /proc/self/cwd is the importer's
// folder when it checks and FreeRDP's when it connects. /dev/fd/N is refused
// whether or not N is open.
func TestShares_RefusesLinksThroughProc(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	link := func(name, target string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	plain := filepath.Join(dir, "plain")
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	refused := []string{
		"/dev/fd/0", "/dev/fd/99",
		link("dev", "/dev") + "/fd/0",
		link("fd", "/dev/fd") + "/0",
	}
	if _, err := os.Stat("/proc/self"); err == nil {
		up := strings.Repeat("../", strings.Count(dir, "/"))
		refused = append(refused,
			link("cwd", "/proc/self/cwd"),
			link("proc", "/proc")+"/self/cwd",
			link("rel", up+"proc/self/cwd"),
			link("via", link("cwd2", "/proc/self/cwd")),
		)
	}
	for _, p := range refused {
		shares, got := rdpRefused(t, p)
		if len(shares) != 0 || !reflect.DeepEqual(got, []config.DroppedShare{{Path: p, Reason: refusedProc}}) {
			t.Errorf("%s: shares %+v, refused %+v", p, shares, got)
		}
	}
	// A link to an ordinary folder, and a folder whose name only starts
	// like /proc, are kept.
	for _, p := range []string{link("ok", plain), "/procfs-backup", "/dev/fdx"} {
		if shares, got := rdpRefused(t, p); len(shares) != 1 || len(got) != 0 {
			t.Errorf("%s: shares %+v, refused %+v", p, shares, got)
		}
	}
}

// With no home folder to compare against, nothing tells /home from any
// other folder, so every share is refused, with the reason.
func TestShares_RefusedWithoutHome(t *testing.T) {
	for _, home := range []string{"", "relative/home"} {
		t.Run(home, func(t *testing.T) {
			t.Setenv("HOME", home)
			shares, refused := rdpRefused(t, "/home;/srv/wicket-data")
			want := []config.DroppedShare{
				{Path: "/home", Reason: refusedNoHome},
				{Path: "/srv/wicket-data", Reason: refusedNoHome},
			}
			if len(shares) != 0 || !reflect.DeepEqual(refused, want) {
				t.Fatalf("shares %+v, refused %+v", shares, refused)
			}
		})
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
