package importer

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/gaius-codius/wicket/internal/config"
)

const passwordNotice = "passwords are never imported; you'll be asked on first connect"

// noUserNotice follows the summary when a profile came in with no user, which
// Wicket will not connect without.
const noUserNotice = "a profile with no user needs one before it connects: open it in wicket to add it"

// WriteSummary prints the import result in the form the CLI documents.
func WriteSummary(w io.Writer, imported []config.Profile, dropped []config.DroppedShare, skipped []Skip) {
	if len(imported) > 0 {
		// Each name carries the account it will sign in as, so a dry run
		// shows what an import made of the file's user and domain.
		names := make([]string, len(imported))
		noUser := false
		for i, p := range imported {
			names[i] = printable(fmt.Sprintf("%s (%s)", p.Name, details(p)))
			noUser = noUser || p.User == ""
		}
		fmt.Fprintf(w, "imported %d: %s\n", len(imported), strings.Join(names, ", "))
		// A folder the import left out is named, so the line above is not
		// read as every folder having come across.
		for _, d := range dropped {
			fmt.Fprintln(w, printable(fmt.Sprintf("%s: share %s dropped: %s", d.Profile, strconv.Quote(d.Path), d.Reason)))
		}
		if noUser {
			fmt.Fprintln(w, noUserNotice)
		}
	} else {
		fmt.Fprintf(w, "imported 0\n")
	}
	if len(skipped) > 0 {
		parts := make([]string, len(skipped))
		for i, s := range skipped {
			if s.Reason == "" {
				parts[i] = printable(s.Name)
			} else {
				parts[i] = printable(fmt.Sprintf("%s (%s)", s.Name, s.Reason))
			}
		}
		fmt.Fprintf(w, "skipped %d: %s\n", len(skipped), strings.Join(parts, ", "))
	}
	fmt.Fprintln(w, passwordNotice)
}

// details is the account a profile signs in as, then the settings an
// import carried over that differ from a new profile's: the clipboard when it
// is off, and each shared folder.
func details(p config.Profile) string {
	parts := []string{account(p)}
	if !p.Clipboard {
		parts = append(parts, "clipboard off")
	}
	for _, s := range p.Shares {
		// Quoted, since a path may hold "; " or " as " itself.
		if s.Name == "" {
			parts = append(parts, "share "+strconv.Quote(s.Path))
		} else {
			parts = append(parts, "share "+strconv.Quote(s.Path)+" as "+s.Name)
		}
	}
	return strings.Join(parts, "; ")
}

// printable replaces each control character with '?'. A skipped entry's name
// and reason come straight from the file, so without it a file could send
// escape sequences to the terminal. Bidi controls go too, so a name cannot
// print reordered; invalid UTF-8 becomes U+FFFD.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return '?'
		}
		return r
	}, s)
}

func account(p config.Profile) string {
	switch {
	case p.User == "" && p.Domain != "":
		return "no user, domain " + p.Domain
	case p.User == "":
		return "no user"
	case p.Domain != "":
		return p.Domain + `\` + p.User
	default:
		return p.User
	}
}
