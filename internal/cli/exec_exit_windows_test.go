//go:build windows

package cli

import (
	"errors"
	"os/exec"
	"testing"
)

func TestCommandExitCodeWindowsFallback(t *testing.T) {
	command := exec.Command("cmd.exe", "/S", "/C", "exit 7")
	err := command.Run()
	if got := commandExitCode(err); got != 7 {
		t.Fatalf("ordinary exit code = %d, err=%v; want 7", got, err)
	}
	if got := commandExitCode(errors.New("launch failed")); got != 1 {
		t.Fatalf("non-exit error code = %d, want 1", got)
	}
}
