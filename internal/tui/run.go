package tui

import tea "charm.land/bubbletea/v2"

// Run starts the Bubble Tea program. cmd/wicket must not import Charm packages.
// Production connect uses tea.Exec (not CSI-only screen switches) so the
// terminal leaves raw mode: Ctrl+C is SIGINT delivered to the child (UX-007).
func Run(opt Options) error {
	opt.Term = nopTerm{}
	m := New(opt)
	_, err := tea.NewProgram(m).Run()
	return err
}
