//go:build !windows

package usercli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCurrentRejectsSymlinkTarget(t *testing.T) {
	directory := t.TempDir()
	external := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(external, []byte("external"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "reconc")
	if err := os.Symlink(external, target); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECONC_INSTALL_DIR", directory)
	t.Setenv("PATH", directory)

	if _, err := InstallCurrent(""); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink install error = %v", err)
	}
	body, err := os.ReadFile(external)
	if err != nil || string(body) != "external" {
		t.Fatalf("external symlink target changed: body=%q err=%v", body, err)
	}
}

func TestPOSIXPathRemediationIsCopyPasteSafe(t *testing.T) {
	got := pathRemediation("/tmp/reconc user's bin")
	want := "export PATH='/tmp/reconc user'\\''s bin':$PATH"
	if !strings.Contains(got, want) {
		t.Fatalf("PATH remediation = %q, want substring %q", got, want)
	}
}
