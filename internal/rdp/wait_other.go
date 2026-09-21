//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package rdp

// waitExited cannot wait without collecting the child here, so a signal
// sent in the instant between the exit and cmd.Wait returning is the one
// case left unguarded, and helpers a client leaves in its group when it
// exits unasked are not stopped with it. Linux uses waitid with WNOWAIT and
// the BSDs, macOS included, a kqueue.
func waitExited(int) bool { return false }
