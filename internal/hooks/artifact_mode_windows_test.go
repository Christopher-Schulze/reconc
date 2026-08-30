//go:build windows

package hooks

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestGeneratedArtifactPublicationPreservesWindowsModeProxy(t *testing.T) {
	for _, test := range []struct {
		name         string
		before       string
		after        string
		mode         os.FileMode
		executable   bool
		wantReadOnly bool
		wantAction   string
	}{
		{name: "strict data unchanged", before: "same", after: "same", mode: 0o400, wantReadOnly: true, wantAction: "unchanged"},
		{name: "writable data unchanged", before: "same", after: "same", mode: 0o600, wantAction: "unchanged"},
		{name: "read-only executable intent", before: "same", after: "same", mode: 0o400, executable: true, wantReadOnly: true, wantAction: "unchanged"},
		{name: "writable executable intent", before: "same", after: "same", mode: 0o600, executable: true, wantAction: "unchanged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "artifact")
			if err := os.WriteFile(path, []byte(test.before), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			snapshot, err := readManagedArtifactSnapshot(path)
			if err != nil {
				t.Fatal(err)
			}
			action, err := writeGeneratedArtifact(path, test.after, test.executable, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			readOnly := windowsArtifactReadOnly(t, path)
			if action != test.wantAction || readOnly != test.wantReadOnly {
				t.Fatalf("publication = action %q read-only %t, want action %q read-only %t", action, readOnly, test.wantAction, test.wantReadOnly)
			}
		})
	}
}

func windowsArtifactReadOnly(t *testing.T, path string) bool {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		t.Fatal(err)
	}
	return attributes&windows.FILE_ATTRIBUTE_READONLY != 0
}
