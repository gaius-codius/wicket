package rdp

import (
	"errors"
	"os/exec"
	"testing"
)

func TestClientBasenameRejectedWithoutLookPath(t *testing.T) {
	t.Parallel()
	l := &Launcher{Runner: failRunner{}, Stdout: nil, Stderr: nil}
	_, err := l.Start(Plan{Client: "/tmp/x"}, nil)
	if !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("err = %v", err)
	}
}

type failRunner struct{}

func (failRunner) LookPath(string) (string, error) {
	panic("LookPath must not be called for illegal basename")
}
func (failRunner) Command(string, ...string) *exec.Cmd { panic("Command") }
