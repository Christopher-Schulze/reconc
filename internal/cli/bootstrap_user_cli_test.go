package cli

import (
	"bytes"
	"os"
	"testing"

	"reconc.dev/reconc/internal/usercli"
)

// Init and bootstrap need a callable CLI, not the running build: they must not
// replace or downgrade an intact receipt-owned install of another version.
func TestEnsureCurrentUserCLIKeepsAnIntactReceiptOwnedInstall(t *testing.T) {
	installDir := t.TempDir()
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("PATH", installDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := usercli.InstallCurrentWithReceipt("", usercli.InstallOptions{Version: "9.9.9-installed", SkillMode: usercli.SkillSkip}); err != nil {
		t.Fatal(err)
	}
	diagnostic, err := usercli.DiagnoseGlobal("1.0.0-running")
	if err != nil || diagnostic.ReceiptPath == nil {
		t.Fatalf("diagnose: %+v %v", diagnostic, err)
	}
	if diagnostic.Status != usercli.DiagnosticStale {
		t.Fatalf("fixture is not a stale-by-version install: %s", diagnostic.Status)
	}
	before, err := os.ReadFile(*diagnostic.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureCurrentUserCLI("apply", "1.0.0-running"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	after, err := os.ReadFile(*diagnostic.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("bootstrap rewrote the receipt of an intact install:\nbefore %s\nafter  %s", before, after)
	}
}
