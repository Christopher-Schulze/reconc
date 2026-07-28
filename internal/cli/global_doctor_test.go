package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/usercli"
)

func TestGlobalDoctorJSONAndTextShareHealthyOwnershipTruth(t *testing.T) {
	installDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"install-cli", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("install-cli: %v stderr=%s", err, stderr.String())
	}

	stdout.Reset()
	if err := Run([]string{"doctor", "--global", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("global doctor JSON: %v stderr=%s", err, stderr.String())
	}
	var report usercli.GlobalDiagnostic
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode global doctor: %v\n%s", err, stdout.String())
	}
	if report.Status != usercli.DiagnosticHealthy || report.Owner == nil ||
		*report.Owner != usercli.ManagerSource || !report.ReceiptValid {
		t.Fatalf("global doctor JSON = %+v", report)
	}

	stdout.Reset()
	if err := Run([]string{"doctor", "--global"}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	text := stdout.String()
	if !strings.Contains(text, "Status: healthy") || !strings.Contains(text, "Owner: source") ||
		strings.Count(text, "Next:") != 1 {
		t.Fatalf("global doctor text = %q", text)
	}
}

func TestGlobalDoctorRejectsRepositoryAndDeepCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"doctor", "--global", "."},
		{"doctor", "--global", "--deep"},
		{"doctor", ".", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(args, "test", &stdout, &stderr)
		if err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}
}

func TestGlobalDoctorWritesExactOutputFile(t *testing.T) {
	installDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"install-cli"}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	output := filepath.Join(t.TempDir(), "doctor.json")
	if err := Run([]string{"doctor", "--global", "--json", "--output", output}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != stdout.String() {
		t.Fatalf("global doctor output file differs\nstdout=%s\nfile=%s", stdout.String(), body)
	}
}
