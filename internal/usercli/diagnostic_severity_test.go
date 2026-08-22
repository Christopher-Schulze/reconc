package usercli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalDiagnosticDoesNotHideStaleReceiptBehindPATHShadow(t *testing.T) {
	installDirectory := t.TempDir()
	shadowDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	if _, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(installDirectory, executableName())
	if err := os.WriteFile(target, []byte("stale installed binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	shadow := filepath.Join(shadowDirectory, executableName())
	if err := os.WriteFile(shadow, []byte("path shadow"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shadowDirectory+string(os.PathListSeparator)+installDirectory)

	diagnostic, err := DiagnoseGlobal("test")
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.Status != DiagnosticStale {
		t.Fatalf("diagnostic status = %s, want %s: %+v", diagnostic.Status, DiagnosticStale, diagnostic)
	}
}
