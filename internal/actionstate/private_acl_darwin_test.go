//go:build darwin

package actionstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateDarwinFilesystemObjectsRejectExtendedACLs(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "private.json")
	if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateDirectory(directory); err != nil {
		t.Fatalf("plain private directory: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateFile(file, info); err != nil {
		t.Fatalf("plain private file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{path, directory} {
		command := exec.Command("/bin/chmod", "+a", "everyone allow read", target)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("add test ACL to %s: %v: %s", target, err, output)
		}
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateFile(file, info); err == nil || !strings.Contains(err.Error(), "extended ACL") {
		t.Fatalf("file ACL error = %v", err)
	}
	if err := validatePrivateDirectory(directory); err == nil || !strings.Contains(err.Error(), "extended ACL") {
		t.Fatalf("directory ACL error = %v", err)
	}
}
