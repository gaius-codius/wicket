package rdp

import "golang.org/x/sys/unix"

// waitExited blocks until pid has exited, leaving it for exec.Cmd.Wait to
// collect. It reports false if it could not wait, and the caller then has
// only cmd.Wait to go on.
func waitExited(pid int) bool {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			return err == nil
		}
	}
}
