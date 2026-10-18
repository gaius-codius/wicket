package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

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
		if got := ShareRefusal(a); got != RefusedHome {
			t.Errorf("%s: %q", a, got)
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
		if got := ShareRefusal(p); got != RefusedHome {
			t.Errorf("%s: %q", p, got)
		}
	}
	// What holds no home at that place is kept.
	if got := ShareRefusal(filepath.Join(mirror, "tmp-other")); got != "" {
		t.Errorf("unrelated folder: %q", got)
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
		if got := ShareRefusal(p); got != RefusedProc {
			t.Errorf("%s: %q", p, got)
		}
	}
	// A link to an ordinary folder, and a folder whose name only starts
	// like /proc, are kept.
	for _, p := range []string{link("ok", plain), "/procfs-backup", "/dev/fdx"} {
		if got := ShareRefusal(p); got != "" {
			t.Errorf("%s: %q", p, got)
		}
	}
}

// With no home folder to compare against, nothing tells /home from any
// other folder, so every share is refused, with the reason.
func TestShares_RefusedWithoutHome(t *testing.T) {
	for _, home := range []string{"", "relative/home"} {
		t.Run(home, func(t *testing.T) {
			t.Setenv("HOME", home)
			for _, p := range []string{"/home", "/srv/wicket-data"} {
				if got := ShareRefusal(p); got != RefusedNoHome {
					t.Errorf("%s: %q", p, got)
				}
			}
		})
	}
}

// linkTree makes dir/rel for each rel ending in "/" and a link at each other
// rel, to its target, under a new folder it returns.
func linkTree(t *testing.T, links map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, target := range links {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// FreeRDP follows links inside a share, so a share is refused when a link
// anywhere in it leads to home, a folder above it, or /, or through /proc
// or /dev/fd, and the reason names the link. A link to a folder outside the
// share is read through too, since the server can browse it: a link there
// that leads to / is as good as one in the share.
func TestShareRefusal_LinksInside(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	outside := linkTree(t, map[string]string{"z:": "/"})
	cases := []struct {
		name  string
		links map[string]string
		link  string
	}{
		{"home", map[string]string{"h": home}, "h"},
		{"root", map[string]string{"z:": "/"}, "z:"},
		{"above home", map[string]string{"up": filepath.Dir(home)}, "up"},
		{"deep", map[string]string{"drive_c/": "", "dosdevices/z:": "/"}, filepath.Join("dosdevices", "z:")},
		{"relative", map[string]string{"r": strings.Repeat("../", 40)}, "r"},
		{"through a link", map[string]string{"out": outside}, filepath.Join("out", "z:")},
		{"dev fd", map[string]string{"fd": "/dev/fd/0"}, "fd"},
	}
	if _, err := os.Stat("/proc/self"); err == nil {
		cases = append(cases, struct {
			name  string
			links map[string]string
			link  string
		}{"proc", map[string]string{"cwd": "/proc/self/cwd"}, "cwd"})
	}
	for _, tc := range cases {
		dir := linkTree(t, tc.links)
		got := ShareRefusal(dir)
		if got == "" || !strings.Contains(got, fmt.Sprintf("link %q ", tc.link)) {
			t.Errorf("%s: %q, want a refusal naming %s", tc.name, got, tc.link)
		}
	}
}

// Links that lead to ordinary folders or files, nowhere, or back into the
// share are kept, and a loop of them ends.
func TestShareRefusal_HarmlessLinksKept(t *testing.T) {
	t.Parallel()
	plain := linkTree(t, map[string]string{"docs/": ""})
	if err := os.WriteFile(filepath.Join(plain, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := linkTree(t, map[string]string{
		"sub/":      "",
		"docs":      filepath.Join(plain, "docs"),
		"file":      filepath.Join(plain, "file"),
		"gone":      "/nonexistent/wicket-target",
		"self":      ".",
		"sub/up":    "..",
		"loop/a":    "b",
		"loop/b":    "a",
		"plain":     plain,
		"plain-too": plain,
	})
	if got := ShareRefusal(dir); got != "" {
		t.Errorf("refused: %q", got)
	}
}

// A tree too large to read in full is refused, rather than shared with what
// lies past the limit unchecked; one within it is read to the end.
func TestLinkOut_Limit(t *testing.T) {
	t.Parallel()
	dir := linkTree(t, map[string]string{"a/": "", "b/": "", "a/c/": ""})
	homes := homeNames("/nonexistent/wicket-home", false)
	if got := walk(t, dir, homes, 3); got != "" {
		t.Errorf("within the limit: %q", got)
	}
	if got := walk(t, dir, homes, 2); !strings.Contains(got, "more than 2") {
		t.Errorf("past the limit: %q", got)
	}
	// A folder larger than one batch is read in several.
	big := t.TempDir()
	for i := range 1500 {
		if err := os.WriteFile(filepath.Join(big, fmt.Sprint(i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := walk(t, big, homes, 1500); got != "" {
		t.Errorf("1500 entries within the limit: %q", got)
	}
	if got := walk(t, big, homes, 1499); !strings.Contains(got, "more than 1499") {
		t.Errorf("1500 entries past the limit: %q", got)
	}
}

// walk is linkOut with a context nothing cancels.
func walk(t *testing.T, dir string, homes []string, limit int) string {
	t.Helper()
	return linkOut(t.Context(), dir, homes, limit)
}

// stopAfter is a context that is cancelled once Err has been asked n
// times, as if the user stopped waiting part way through a walk. asked
// counts the times Err was asked.
type stopAfter struct {
	context.Context
	n     int32
	asked atomic.Int32
	once  sync.Once
	done  chan struct{}
}

func newStopAfter(t *testing.T, n int32) *stopAfter {
	t.Helper()
	return &stopAfter{Context: t.Context(), n: n, done: make(chan struct{})}
}

func (c *stopAfter) Err() error {
	if c.asked.Add(1) > c.n {
		c.once.Do(func() { close(c.done) })
		return context.Canceled
	}
	return nil
}

// Done agrees with Err: it is closed once Err has said the context is done.
func (c *stopAfter) Done() <-chan struct{} { return c.done }

// A check the user stops waiting for reads no further folders: a tree of
// many folders, with a link to / below all of them, is refused when read
// to the end, and left unread past the stop when stopped early, rather
// than read on for a result nobody wants.
func TestLinkOut_StopsWhenCancelled(t *testing.T) {
	t.Parallel()
	tree := map[string]string{"d0/deep/z:": "/"}
	for i := range 200 {
		tree[fmt.Sprintf("d%d/", i)] = ""
	}
	dir := linkTree(t, tree)
	homes := homeNames("/nonexistent/wicket-home", false)
	if got := walk(t, dir, homes, maxShareEntries); !strings.Contains(got, "FreeRDP follows links") {
		t.Fatalf("read to the end: %q", got)
	}
	ctx := newStopAfter(t, 10)
	if got := linkOut(ctx, dir, homes, maxShareEntries); got != "" {
		t.Fatalf("stopped: %q", got)
	}
	// Nothing more is looked at once it has seen it is stopped.
	if n := ctx.asked.Load(); n != 11 {
		t.Fatalf("asked %d times, want 11: stopped at the first ask after the stop", n)
	}
}

// A check stopped while it reads a folder looks at none of the entries
// left in it: each link in it could lead to another hung mount, and
// resolving them one after another would keep the stopped check going.
func TestLinkOut_StopsWithinAFolder(t *testing.T) {
	t.Parallel()
	tree := map[string]string{"z:": "/"}
	for i := range 300 {
		tree[fmt.Sprintf("l%d", i)] = "/nonexistent/wicket-target"
	}
	dir := linkTree(t, tree)
	homes := homeNames("/nonexistent/wicket-home", false)
	if got := walk(t, dir, homes, maxShareEntries); !strings.Contains(got, "FreeRDP follows links") {
		t.Fatalf("read to the end: %q", got)
	}
	// Stopped by the time the folder's entries are looked at, the link
	// to / among them is never reached.
	if got := linkOut(newStopAfter(t, 3), dir, homes, maxShareEntries); got != "" {
		t.Fatalf("stopped: %q", got)
	}
}

// A share check whose context is done reports it, wrapped, rather than
// passing the profile.
func TestCheckShares_Cancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p := Profile{
		Name: "work", Host: "h", User: "u", Client: "sdl-freerdp3", Scale: 100,
		Shares: []Share{{Path: t.TempDir()}},
	}
	if _, err := CheckShares(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if _, err := CheckShares(t.Context(), p); err != nil {
		t.Fatalf("not cancelled: %v", err)
	}
}

// A folder the server can enter but Wicket cannot list could hold a link
// whose name the server knows, so the share is refused. One that cannot be
// entered at all is passed over, since the server cannot reach through it.
func TestShareRefusal_UnlistableFolder(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root lists every folder")
	}
	closed := filepath.Join(linkTree(t, map[string]string{"closed/z:": "/"}), "closed")
	hidden := filepath.Join(linkTree(t, map[string]string{"hidden/z:": "/"}), "hidden")
	for dir, mode := range map[string]os.FileMode{closed: 0, hidden: 0o100} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	}
	if got := ShareRefusal(filepath.Dir(closed)); got != "" {
		t.Errorf("a folder that cannot be entered: %q", got)
	}
	if got := ShareRefusal(filepath.Dir(hidden)); !strings.Contains(got, `folder "hidden" in it cannot be listed`) {
		t.Errorf("a folder that can be entered but not listed: %q", got)
	}
}

// A link's name goes into the reason quoted, so a name holding a terminal
// escape is printed as text.
func TestShareRefusal_QuotesNames(t *testing.T) {
	t.Parallel()
	dir := linkTree(t, map[string]string{"\x1b]52;c;SGk=\a": "/"})
	if got := ShareRefusal(dir); got == "" || strings.ContainsAny(got, "\x1b\a") {
		t.Errorf("%q", got)
	}
}

// A share reached by a short path can hold folders whose resolved path is
// too long to look up, though FreeRDP, given the short one, reaches them.
// What the walk cannot look up is refused, not passed over, whether that is
// the share itself or a folder below it. The tree is built through links,
// each a short name for the folder below the last, as deep as the system
// allows.
func TestShareRefusal_PathTooLong(t *testing.T) {
	t.Parallel()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("d", 200)
	// aliases[i] is a short name for base and i+1 long folders below it.
	var aliases []string
	prev := base
	for i := 0; len(base)+i*(len(long)+1) < 4600; i++ {
		dir := filepath.Join(prev, long)
		if err := os.Mkdir(dir, 0o700); err != nil {
			// macOS will not make a folder through a link past its path
			// limit, so it cannot reach one there either.
			if errors.Is(err, syscall.ENAMETOOLONG) && len(aliases) > 0 {
				break
			}
			t.Fatal(err)
		}
		alias := filepath.Join(base, fmt.Sprint("s", i))
		if err := os.Symlink(dir, alias); err != nil {
			t.Fatal(err)
		}
		aliases = append(aliases, alias)
		prev = alias
	}
	if err := os.Symlink("/", filepath.Join(prev, "z")); err != nil {
		t.Fatal(err)
	}
	// The deepest share whose own path can still be looked up holds
	// folders whose paths cannot; the last cannot be looked up itself.
	var fits string
	for _, a := range aliases {
		if _, err := filepath.EvalSymlinks(a); err == nil {
			fits = a
		}
	}
	if fits == "" {
		t.Fatal("no share resolves")
	}
	if fits == prev {
		t.Skip("this system reaches no folder through a link past its path limit")
	}
	for _, share := range []string{fits, prev} {
		if got := ShareRefusal(share); !strings.Contains(got, "too long") {
			t.Errorf("%s: %q, want a refusal for a path too long", filepath.Base(share), got)
		}
	}
}
