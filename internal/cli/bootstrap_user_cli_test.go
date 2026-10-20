package cli

import (
	"bytes"
	"os"
	"strings"
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

// A direct release install keeps its owner: bootstrap never replaces it with
// the running build implicitly and names the explicit commands instead.
func TestImplicitReplacementOfADirectInstallIsRefused(t *testing.T) {
	direct, source := usercli.ManagerDirect, usercli.ManagerSource
	for _, test := range []struct {
		name       string
		diagnostic usercli.GlobalDiagnostic
		refused    bool
	}{
		{name: "direct receipt", diagnostic: usercli.GlobalDiagnostic{Owner: &direct, ReceiptValid: true, Status: usercli.DiagnosticStale}, refused: true},
		{name: "source receipt", diagnostic: usercli.GlobalDiagnostic{Owner: &source, ReceiptValid: true, Status: usercli.DiagnosticStale}},
		{name: "no receipt", diagnostic: usercli.GlobalDiagnostic{Status: usercli.DiagnosticStale}},
		{name: "invalid direct receipt", diagnostic: usercli.GlobalDiagnostic{Owner: &direct, Status: usercli.DiagnosticStale}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reason := directInstallReplacementRefusal(&test.diagnostic)
			if (reason != "") != test.refused {
				t.Fatalf("refusal = %q, want refused=%t", reason, test.refused)
			}
			if test.refused && (!strings.Contains(reason, "direct") || !strings.Contains(reason, "reconc update")) {
				t.Fatalf("refusal names no explicit remediation: %q", reason)
			}
		})
	}
}
