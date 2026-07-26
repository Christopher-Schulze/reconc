package usercli

import (
	"os"
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
