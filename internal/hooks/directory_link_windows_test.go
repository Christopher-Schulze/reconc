//go:build windows

package hooks

import (
	"os/exec"
	"testing"
)

func createDirectoryLinkForTest(t *testing.T, target, link string) {
	t.Helper()
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create directory junction: %v: %s", err, output)
	}
}
