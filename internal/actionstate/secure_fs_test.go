package actionstate

import (
	"os"
	"path/filepath"
	"testing"
)

func privateTestHome(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureDirectoryMode(root, info.Mode()); err != nil {
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
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := securePrivateFileMode(path, info.Mode()); err != nil {
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
