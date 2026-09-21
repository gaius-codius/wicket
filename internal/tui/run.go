package tui

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
)

// quitGrace is how long Wicket, on its way out, gives a running client to
// stop on SIGTERM before killing it.
const quitGrace = 3 * time.Second

// Run starts the Bubble Tea program. cmd/wicket must not import Charm packages.
func Run(opt Options) error {
	return runProgram(New(opt))
}

// runProgram runs m until it quits, then stops any session it left running.
//
// The terminal stays in raw mode for the whole run, a session included, so
// Ctrl+C typed on the TTY arrives as a key (UX-007; see session.go). Signals
// sent to the process are Wicket's to handle too: Bubble Tea's own handler
// would end the program on SIGINT or SIGTERM with no chance to stop the
// client, so it is replaced by one that turns each signal into a message for
// the model. SIGHUP is included because a closed terminal window sends it,
// and the client, in its own process group, would not otherwise hear of it.
func runProgram(m Model, opts ...tea.ProgramOption) error {
	app := m.app
	p := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithoutSignalHandler()}, opts...)...)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for {
			select {
			case sig := <-sigs:
				// Send returns at once when the program has finished.
				p.Send(signalMsg{sig: sig})
			case <-done:
				return
			}
		}
	}()

	_, err := p.Run()
	// Whatever ended the program -- q, a signal, an error, a panic Bubble Tea
	// recovered from -- a client still running is stopped before Wicket
	// exits. The handler stays installed until then, so a second SIGTERM
	// cannot kill Wicket half way through stopping it.
	app.StopSession(quitGrace)
	signal.Stop(sigs)
	close(done)
	<-forwarded

	// Quitting with Ctrl+C or SIGINT is not a failure, so do not report it
	// as one.
	if errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return err
}
