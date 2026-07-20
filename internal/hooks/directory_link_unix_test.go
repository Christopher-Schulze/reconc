//go:build !windows

package hooks

import (
	"os"
	"testing"
)

func createDirectoryLinkForTest(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}
}
