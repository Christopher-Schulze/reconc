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

func TestInspectCurrentPreservesUnreadableTargetDiagnostic(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("RECONC_INSTALL_DIR", directory)
	t.Setenv("PATH", directory)
	status, err := InspectCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(status.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(status.TargetPath, body, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(status.TargetPath, 0o700) })
	unreadable, err := InspectCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if len(unreadable.Diagnostics) == 0 {
		if os.Geteuid() == 0 {
			t.Skip("root can read a mode-000 binary")
		}
		t.Fatal("unreadable target was reduced to an outdated status")
	}
	if unreadable.Diagnostics[0].Name != "target-checksum" || unreadable.Diagnostics[0].Status != "fail" ||
		!strings.Contains(unreadable.NextAction, "target-checksum") {
		t.Fatalf("unreadable status = %+v", unreadable)
	}
}
