package importer

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/gaius-codius/wicket/internal/config"
)

// maxLinks bounds the symlinks viaProc follows for one path, as the kernel
// bounds them, so a loop ends.
const maxLinks = 255

// dataVolume is where macOS keeps the folders its root shows through
// firmlinks, the home folders among them: /System/Volumes/Data/Users/alice
// is /Users/alice, and a firmlink is not a symlink, so nothing resolves one
// name to the other.
const dataVolume = "/System/Volumes/Data"

// wholeHomeOrRoot is why path cannot be shared when it holds the whole home
// folder: the root, home itself, or a folder above it such as /home. A path
// that exists is compared with those folders by identity, not by name: on
// macOS /users/alice is /Users/alice, and following symlinks keeps the
// spelling it was given. Anything reached through /proc or /dev/fd is
// refused outright, since what their links name changes with the process
// that reads them, and FreeRDP, not the importer, reads them last. A path
// that cannot be expanded is refused rather than guessed at, and so is
// every path when the home folder is unknown. "" means it may be shared.
func wholeHomeOrRoot(path string) string {
	expanded, err := config.ExpandPath(path)
	if err != nil {
		return err.Error()
	}
	if viaProc(expanded) {
		return refusedProc
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		// Nothing tells /home from any other folder then, so nothing is
		// shared rather than everything.
		return refusedNoHome
	}
	if holdsHome(expanded, homeNames(home, runtime.GOOS == "darwin")) {
		return refusedHome
	}
	return ""
}

// homeNames is every name the home folder goes by that its parents are
// read from: home as given and with its symlinks followed, and on macOS
// each again under the data volume.
func homeNames(home string, darwin bool) []string {
	names := []string{filepath.Clean(home)}
	if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != names[0] {
		names = append(names, resolved)
	}
	if darwin {
		for _, n := range names {
			names = append(names, dataVolume+n)
		}
	}
	return names
}

// holdsHome reports whether path is one of homes or a folder above one,
// by name, or as the same folder when both exist.
func holdsHome(path string, homes []string) bool {
	info, statErr := os.Stat(path) //nolint:gosec // G703: the path is checked, not opened; finding what it names is the point
	for _, home := range homes {
		for dir := home; ; dir = filepath.Dir(dir) {
			if dir == path {
				return true
			}
			if statErr == nil {
				if d, err := os.Stat(dir); err == nil && os.SameFile(info, d) {
					return true
				}
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	return false
}

// viaProc reports whether path leads into /proc or /dev/fd at any step.
// Its symlinks are followed one component at a time, as the kernel follows
// them, so a link to /proc/self/cwd counts though it leads back out, and
// each folder on the way is compared with /proc and /dev/fd by identity, so
// another spelling of either counts too. Past a component that does not
// exist, the rest is compared by name.
func viaProc(path string) bool {
	special := specialDirs()
	rest := splitPath(path)
	cur := "/"
	links := 0
	for len(rest) > 0 {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case ".":
			continue
		case "..":
			// cur has no symlinks left in it, so its parent by name is
			// the one the kernel takes.
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		info, err := os.Lstat(next) //nolint:gosec // G703: the path is checked, not opened; finding what it names is the point
		if err != nil {
			return procName(filepath.Join(append([]string{next}, rest...)...))
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			links++
			target, err := os.Readlink(next)
			if err != nil || links > maxLinks {
				// The path cannot be followed, so it cannot be shared
				// either; connecting reports that.
				return false
			}
			if filepath.IsAbs(target) {
				cur = "/"
			}
			rest = append(splitPath(target), rest...)
			continue
		}
		if slices.ContainsFunc(special, func(s fs.FileInfo) bool { return os.SameFile(info, s) }) {
			return true
		}
		cur = next
	}
	return false
}

// procName reports whether p names /proc or /dev/fd, or a path under one.
func procName(p string) bool {
	for _, dir := range []string{"/proc", "/dev/fd"} {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

// specialDirs is /proc and /dev/fd, as far as this system has them.
func specialDirs() []fs.FileInfo {
	var out []fs.FileInfo
	for _, dir := range []string{"/proc", "/dev/fd"} {
		if info, err := os.Stat(dir); err == nil {
			out = append(out, info)
		}
	}
	return out
}

// splitPath is the components of p, without the empty ones a leading,
// trailing or doubled slash leaves.
func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
}
