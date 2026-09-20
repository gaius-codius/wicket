package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"
)

// Run starts the Bubble Tea program. cmd/wicket must not import Charm packages.
// Production connect uses tea.Exec (not CSI-only screen switches) so the
// terminal leaves raw mode: Ctrl+C is SIGINT delivered to the child (UX-007).
func Run(opt Options) error {
	opt.Term = nopTerm{}
	m := New(opt)
	_, err := tea.NewProgram(m).Run()
	// A SIGINT never reaches Update: Bubble Tea turns it into ErrInterrupted
	// and stops. Quitting with Ctrl+C is not a failure, so do not report it
	// as one. Ctrl+C typed on a TTY still arrives as a key and is handled by
	// the model.
	if errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return err
}
