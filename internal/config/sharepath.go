package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

// maxLinks bounds the symlinks viaProc follows for one path, as the kernel
// bounds them, so a loop ends.
const maxLinks = 255

// maxShareEntries bounds the entries ShareRefusal has linkOut read for one
// share, in the folder and in the folders links in it lead to, so a check
// of a large tree ends.
const maxShareEntries = 100_000

// dataVolume is where macOS keeps the folders its root shows through
// firmlinks, the home folders among them: /System/Volumes/Data/Users/alice
// is /Users/alice, and a firmlink is not a symlink, so nothing resolves one
// name to the other.
const dataVolume = "/System/Volumes/Data"

// Reasons ShareRefusal gives that are the same for every path. An import
// gives RefusedHome for a Windows home folder too.
const (
	RefusedHome   = "holds the whole home folder; turn on share_home to share it"
	RefusedProc   = "folders under /proc or /dev/fd, or reached through them, are not shared"
	RefusedNoHome = "the home folder is unknown (HOME is unset or not absolute), so no folder is shared"
)

// ShareRefusal is why the folder at path must not be shared, or "" when it
// may be. Import asks it of every share it reads, and save and connect of
// every share a profile holds, so a config written by hand, or a link put in
// place after an import, is held to the same rules.
//
// A folder that holds the whole home folder is refused: the root, home
// itself, or a folder above it such as /home. A path that exists is compared
// with those folders by identity, not by name: on macOS /users/alice is
// /Users/alice, and following symlinks keeps the spelling it was given.
// Anything reached through /proc or /dev/fd is refused outright, since what
// their links name changes with the process that reads them, and FreeRDP,
// not Wicket, reads them last. A path that cannot be expanded is refused
// rather than guessed at, and so is every path when the home folder is
// unknown. FreeRDP follows symlinks inside a share, so a folder whose links
// lead to any of those is refused too; see linkOut.
func ShareRefusal(path string) string {
	expanded, err := ExpandPath(path)
	if err != nil {
		return err.Error()
	}
	if viaProc(expanded) {
		return RefusedProc
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		// Nothing tells /home from any other folder then, so nothing is
		// shared rather than everything.
		return RefusedNoHome
	}
	homes := homeNames(home, runtime.GOOS == "darwin")
	if holdsHome(expanded, homes) {
		return RefusedHome
	}
	return linkOut(expanded, homes, maxShareEntries)
}

// linkOut is why the folder at dir must not be shared for where the links
// in it lead, or "". FreeRDP reaches what the server names below a share
// with stat, open and opendir, which follow symlinks, and refuses only a
// ".." in the name, so a link inside a share hands the server whatever it
// leads to: Wine's dosdevices/z: is /. The tree is read breadth first, into
// the folders links lead to as well, since the server can browse those
// too. Each folder is read by its path with the links resolved, so how
// many links led to it does not change what its own links resolve to. A
// link that leads through /proc or /dev/fd is refused, as is one to a
// folder that holds home, and so is a folder in the tree that is one, as a
// bind mount of / can be. A link that leads nowhere is passed over, and so
// is a folder that cannot be entered: the server cannot reach through
// either. A folder that can be entered but not listed is refused, since a
// server that knows a name inside it can still open it, and so is a path
// too long to look up; see tooLong. A tree of more
// than limit entries is refused, since what it holds past them is unknown.
// A dir that does not exist holds nothing, and connect reports it. Names
// are quoted in the reason, as a file name may hold anything.
func linkOut(dir string, homes []string, limit int) string {
	resolved, err := filepath.EvalSymlinks(dir)
	if tooLong(err) {
		return tooLongReason("")
	}
	if err != nil {
		return ""
	}
	holders := homeHolders(homes)
	type folder struct{ real, rel string }
	queue := []folder{{real: resolved}}
	// Every folder queued, by its resolved path, so a link back up the
	// tree, or to a folder already read, is read once.
	queued := map[string]bool{resolved: true}
	read := 0
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		entries, err := readDir(d.real, limit-read+1)
		if err != nil {
			// Looking up "." in a folder needs leave to enter it, as
			// FreeRDP's lookups below it would; Join would clean it away.
			_, statErr := os.Stat(d.real + string(filepath.Separator) + ".")
			if tooLong(err) || tooLong(statErr) {
				return tooLongReason(d.rel)
			}
			if statErr != nil {
				continue
			}
			if d.rel == "" {
				return "cannot be listed to check for links that lead out of it"
			}
			return fmt.Sprintf("the folder %q in it cannot be listed to check for links that lead out of it", d.rel)
		}
		for _, e := range entries {
			read++
			if read > limit {
				return fmt.Sprintf("holds more than %d files and folders, with those its links lead to, too many to check for links that lead out of it", limit)
			}
			p := filepath.Join(d.real, e.Name())
			rel := filepath.Join(d.rel, e.Name())
			switch {
			case e.Type()&fs.ModeSymlink != 0:
				if viaProc(p) {
					return fmt.Sprintf("the link %q in it leads through /proc or /dev/fd, and FreeRDP follows links", rel)
				}
				target, err := filepath.EvalSymlinks(p)
				if tooLong(err) {
					return tooLongReason(rel)
				}
				if err != nil {
					continue
				}
				info, err := os.Stat(target)
				if tooLong(err) {
					return tooLongReason(rel)
				}
				if err != nil || !info.IsDir() {
					continue
				}
				if holdsHome(target, homes) {
					return fmt.Sprintf("the link %q in it leads to a folder that holds the whole home folder, and FreeRDP follows links", rel)
				}
				if !queued[target] {
					queued[target] = true
					queue = append(queue, folder{real: target, rel: rel})
				}
			case e.IsDir():
				info, err := e.Info()
				if tooLong(err) {
					return tooLongReason(rel)
				}
				if err != nil {
					continue
				}
				if slices.ContainsFunc(holders, func(h fs.FileInfo) bool { return os.SameFile(h, info) }) {
					return fmt.Sprintf("the folder %q in it holds the whole home folder", rel)
				}
				if !queued[p] {
					queued[p] = true
					queue = append(queue, folder{real: p, rel: rel})
				}
			}
		}
	}
	return ""
}

// tooLong reports whether err is a path too long for the system to look
// up. The walk names a folder by its resolved path, which can be too long
// where the shorter path FreeRDP is given below the share is not, so what
// it cannot look up is refused rather than passed over.
func tooLong(err error) bool {
	return errors.Is(err, syscall.ENAMETOOLONG)
}

// tooLongReason is the refusal for a path below the share, rel, that the
// walk could not look up.
func tooLongReason(rel string) string {
	if rel == "" {
		return "has a path too long to check for links that lead out of it"
	}
	return fmt.Sprintf("the path %q in it is too long to check for links that lead out of it", rel)
}

// readDir is the entries of the folder at dir, read a batch at a time and
// no more than n of them, so a folder of millions is not read whole to be
// refused.
func readDir(dir string, n int) ([]fs.DirEntry, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []fs.DirEntry
	for len(out) < n {
		batch, err := f.ReadDir(min(1024, n-len(out)))
		out = append(out, batch...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// homeHolders is home under each of its names, and every folder above it,
// as far as they exist: the folders a share must not reach.
func homeHolders(homes []string) []fs.FileInfo {
	var out []fs.FileInfo
	for _, home := range homes {
		for dir := home; ; dir = filepath.Dir(dir) {
			if info, err := os.Stat(dir); err == nil {
				out = append(out, info)
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	return out
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
// by name, or as the same folder when both exist. A folder that holds a
// home where a folder above it does counts too: in a toolbox /run/host is
// the host's /, and /run/host/home/alice is home.
func holdsHome(path string, homes []string) bool {
	info, statErr := os.Stat(path)
	for _, home := range homes {
		homeInfo, homeErr := os.Stat(home)
		for dir := home; ; dir = filepath.Dir(dir) {
			if dir == path {
				return true
			}
			if statErr == nil {
				if d, err := os.Stat(dir); err == nil && os.SameFile(info, d) {
					return true
				}
				below := filepath.Join(path, strings.TrimPrefix(home, dir))
				if h, err := os.Stat(below); err == nil && homeErr == nil && os.SameFile(h, homeInfo) {
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
		info, err := os.Lstat(next)
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
