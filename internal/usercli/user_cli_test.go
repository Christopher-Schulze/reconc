package usercli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallCurrentPublishesAndRepairsBareCLI(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("RECONC_INSTALL_DIR", directory)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))

	first, err := InstallCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.Status.Installed || !first.Status.Executable ||
		!first.Status.Current || !first.Status.PathVisible || !first.Status.Ready {
		t.Fatalf("first install = %+v", first)
	}
	if err := os.WriteFile(first.Status.TargetPath, []byte("stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale, err := InspectCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Current || stale.Ready {
		t.Fatalf("corrupt CLI reported ready: %+v", stale)
	}
	repaired, err := InstallCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if !repaired.Changed || !repaired.Status.Current || !repaired.Status.Ready {
		t.Fatalf("repair = %+v", repaired)
	}
	idempotent, err := InstallCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if idempotent.Changed || !idempotent.Status.Ready {
		t.Fatalf("idempotent install = %+v", idempotent)
	}
}

func TestInstallCurrentReportsMissingPATHWithoutFalseSuccess(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("RECONC_INSTALL_DIR", directory)
	t.Setenv("PATH", t.TempDir())

	report, err := InstallCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Status.Installed || !report.Status.Current || report.Status.PathVisible || report.Status.Ready {
		t.Fatalf("off-PATH install = %+v", report)
	}
	if !strings.Contains(report.Status.NextAction, "PATH") {
		t.Fatalf("off-PATH remediation = %q", report.Status.NextAction)
	}
}

func TestInstallCurrentRejectsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is platform-specific")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell quoting is not used on Windows")
	}
	got := pathRemediation("/tmp/reconc user's bin")
	want := "export PATH='/tmp/reconc user'\\''s bin':$PATH"
	if !strings.Contains(got, want) {
		t.Fatalf("PATH remediation = %q, want substring %q", got, want)
	}
}
