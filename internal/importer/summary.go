package importer

import (
	"fmt"
	"io"
	"strings"

	"github.com/gaius-codius/wicket/internal/config"
)

const passwordNotice = "passwords are never imported; you'll be asked on first connect"

// WriteSummary prints the import result in the form the CLI documents.
func WriteSummary(w io.Writer, imported []config.Profile, skipped []Skip) {
	if len(imported) > 0 {
		names := make([]string, len(imported))
		for i, p := range imported {
			names[i] = p.Name
		}
		fmt.Fprintf(w, "imported %d: %s\n", len(imported), strings.Join(names, ", "))
	} else {
		fmt.Fprintf(w, "imported 0\n")
	}
	if len(skipped) > 0 {
		parts := make([]string, len(skipped))
		for i, s := range skipped {
			if s.Reason == "" {
				parts[i] = s.Name
			} else {
				parts[i] = fmt.Sprintf("%s (%s)", s.Name, s.Reason)
			}
		}
		fmt.Fprintf(w, "skipped %d: %s\n", len(skipped), strings.Join(parts, ", "))
	}
	fmt.Fprintln(w, passwordNotice)
}
