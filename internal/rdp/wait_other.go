//go:build !linux

package rdp

// waitExited cannot wait without collecting the child here, so a signal
// sent in the instant between the exit and cmd.Wait returning is the one
// case left unguarded.
func waitExited(int) bool { return false }
