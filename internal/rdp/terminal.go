package rdp

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// foregroundGroup is the process group that owns tty now: Wicket's own,
// when it was started from a shell in the foreground.
func foregroundGroup(tty *os.File) (int, error) {
	group, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return 0, fmt.Errorf("no foreground group for %s: %w", tty.Name(), err)
	}
	return group, nil
}

// returnTerminal makes group the foreground process group of tty again.
// Wicket asks from the background, which the terminal answers with SIGTTOU,
// a stop, unless the signal is ignored; it is ignored for just that call.
// A failure is not reported: the client is gone either way, and a shell
// takes its terminal back when Wicket exits.
func returnTerminal(tty *os.File, group int) {
	signal.Ignore(syscall.SIGTTOU)
	defer signal.Reset(syscall.SIGTTOU)
	_ = unix.IoctlSetPointerInt(int(tty.Fd()), unix.TIOCSPGRP, group)
}

// OwnsTerminal reports whether tty is a terminal whose foreground process
// group is Wicket's: only then can Wicket hand it to a client and expect it
// back. Started in the background, or with stdin redirected, it cannot.
func OwnsTerminal(tty *os.File) bool {
	group, err := foregroundGroup(tty)
	return err == nil && group == unix.Getpgrp()
}
