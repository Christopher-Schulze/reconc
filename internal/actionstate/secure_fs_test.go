package actionstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/privatefs"
)

func privateTestHome(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	if err := privatefs.RepairDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func writePrivateTestFile(t testing.TB, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(privatefs.SecureFile(file), file.Close()); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateRegularFile(path, int64(len(body)+1)); err != nil {
		t.Fatal(err)
	}
}

func TestResolveHomeRejectsFilesystemRoot(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if _, err := ResolveHome(root); err == nil {
		t.Fatal("filesystem root was accepted as RECONC_HOME")
	}
}
