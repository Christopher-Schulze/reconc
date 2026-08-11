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

func TestResolveHomeRejectsFilesystemRoot(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if _, err := ResolveHome(root); err == nil {
		t.Fatal("filesystem root was accepted as RECONC_HOME")
	}
}
