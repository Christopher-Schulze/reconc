//go:build windows

package privatefs

import "testing"

func assertPrivateLockSecurity(t *testing.T, path string) {
	t.Helper()
	file, err := OpenExistingLock(path)
	if err != nil {
		t.Fatalf("lock security: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSecureWindowsDescriptorPersistsProtectedDACL(t *testing.T) {
	path := t.TempDir()
	file, _, err := openDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureDirectoryDescriptor(file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := validatePrivateWindowsHandle(file, true); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
