//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package rdp

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// waitExited blocks until pid has exited, leaving it for exec.Cmd.Wait to
// collect, as waitid with WNOWAIT does on Linux: a kqueue reports the exit
// of a process without reaping it. It reports false if it could not wait,
// and the caller then has only cmd.Wait to go on.
func waitExited(pid int) bool {
	// Hold ForkLock while the descriptor is not yet close-on-exec, so a
	// client started at the same moment cannot inherit it, as the os
	// package does for its own kqueues.
	syscall.ForkLock.RLock()
	kq, err := unix.Kqueue()
	if err == nil {
		unix.CloseOnExec(kq)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return false
	}
	defer unix.Close(kq)

	var ev unix.Kevent_t
	unix.SetKevent(&ev, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	ev.Fflags = unix.NOTE_EXIT
	changes := []unix.Kevent_t{ev}
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, changes, events, nil)
		switch {
		case err == unix.EINTR:
			// The registration, if it was made, stays; only wait again.
			changes = nil
			continue
		case err == unix.ESRCH:
			// Some kernels refuse to watch a process that is already a
			// zombie. Only cmd.Wait collects this child, and it has not run
			// yet, so the pid is still ours: it has exited.
			return true
		case err != nil:
			return false
		}
		if n < 1 {
			changes = nil
			continue
		}
		if events[0].Flags&unix.EV_ERROR != 0 {
			return syscall.Errno(events[0].Data) == unix.ESRCH
		}
		if events[0].Fflags&unix.NOTE_EXIT != 0 {
			return true
		}
		changes = nil
	}
}
