//go:build windows

package cli

import (
	"os/exec"
	"testing"
)

func createHookWorkerDirectoryAliasForTest(t *testing.T, target, alias string) {
	t.Helper()
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", alias, target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create directory junction: %v: %s", err, output)
	}
}
