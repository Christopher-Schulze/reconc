//go:build !windows

package boundedio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRegularFileReportsPermissionFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := ReadRegularFile(path, 6); err == nil {
		if os.Geteuid() == 0 {
			t.Skip("root can read mode-000 fixtures")
		}
		t.Fatal("mode-000 file was readable")
	}
}
