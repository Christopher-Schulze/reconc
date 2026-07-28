package usercli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalDiagnosticReportsHealthyReceiptedSourceInstall(t *testing.T) {
	installDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	report, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Receipt == nil {
		t.Fatalf("install receipt = %+v", report)
	}

	diagnostic, err := DiagnoseGlobal("test")
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Status != DiagnosticHealthy || diagnostic.Blocking() ||
		diagnostic.Owner == nil || *diagnostic.Owner != ManagerSource ||
		!diagnostic.ReceiptValid || !diagnostic.ChecksumIdentity {
		t.Fatalf("healthy diagnostic = %+v", diagnostic)
	}
	if diagnostic.NextAction != "Global Reconc installation is healthy." {
		t.Fatalf("healthy next action = %q", diagnostic.NextAction)
	}
}

func TestGlobalDiagnosticFailsClosedOnPATHShadow(t *testing.T) {
	installDirectory := t.TempDir()
	shadowDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	if _, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test"}); err != nil {
		t.Fatal(err)
	}
	shadow := filepath.Join(shadowDirectory, executableName())
	if err := os.WriteFile(shadow, []byte("not Reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shadowDirectory+string(os.PathListSeparator)+installDirectory)

	diagnostic, err := DiagnoseGlobal("test")
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Status != DiagnosticShadowed || !diagnostic.Blocking() {
		t.Fatalf("shadow diagnostic = %+v", diagnostic)
	}
	if diagnostic.ResolvedPath == nil || !samePath(*diagnostic.ResolvedPath, shadow) ||
		len(diagnostic.PathShadows) != 1 || !samePath(diagnostic.PathShadows[0], filepath.Join(installDirectory, executableName())) {
		t.Fatalf("shadow identities = %+v", diagnostic)
	}
}

func TestGlobalDiagnosticReportsMalformedReceiptWithoutGuessingOwner(t *testing.T) {
	installDirectory := t.TempDir()
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	if _, err := InstallCurrent(""); err != nil {
		t.Fatal(err)
	}
	receiptDirectory := filepath.Join(home, installationStateDirName)
	if err := os.MkdirAll(receiptDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receiptDirectory, receiptFileName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	diagnostic, err := DiagnoseGlobal("test")
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Status != DiagnosticInvalid || diagnostic.Owner != nil || !diagnostic.Blocking() {
		t.Fatalf("malformed receipt diagnostic = %+v", diagnostic)
	}
}

func TestGlobalDiagnosticReportsStaleReceiptAfterBinarySwap(t *testing.T) {
	installDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	if _, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(installDirectory, executableName())
	if err := os.WriteFile(target, []byte("swapped binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	diagnostic, err := DiagnoseGlobal("test")
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Status != DiagnosticStale || !diagnostic.Blocking() ||
		diagnostic.ChecksumIdentity {
		t.Fatalf("stale diagnostic = %+v", diagnostic)
	}
}

func TestGlobalDiagnosticClassifiesMultipleLegacyInstallsAsAmbiguous(t *testing.T) {
	installDirectory := t.TempDir()
	shadowDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	if _, err := InstallCurrent(""); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(installDirectory, executableName()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shadowDirectory, executableName()), body, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", installDirectory+string(os.PathListSeparator)+shadowDirectory)

	diagnostic, err := DiagnoseGlobal("test")
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Status != DiagnosticAmbiguous || !diagnostic.Blocking() ||
		len(diagnostic.PathShadows) != 1 {
		t.Fatalf("multiple legacy install diagnostic = %+v", diagnostic)
	}
}

func TestPathCandidateScanIsBounded(t *testing.T) {
	entries := make([]string, maxPATHEntries+1)
	for index := range entries {
		entries[index] = t.TempDir()
	}
	t.Setenv("PATH", strings.Join(entries, string(os.PathListSeparator)))
	if _, err := pathCandidates(); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("oversized PATH error = %v", err)
	}
}
