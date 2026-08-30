//go:build !windows

package cli

import (
	"os"
	"testing"
)

func createHookWorkerDirectoryAliasForTest(t *testing.T, target, alias string) {
	t.Helper()
	if err := os.Symlink(target, alias); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}
}
