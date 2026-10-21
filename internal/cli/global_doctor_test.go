package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/usercli"
)

func TestGlobalDoctorJSONAndTextShareHealthyOwnershipTruth(t *testing.T) {
	installDirectory := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
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

// A failed check is a failed diagnosis: a modified receipt-owned skill makes
// doctor exit 1 with a non-healthy status and keeps its targeted repair.
func TestGlobalDoctorFailsForAModifiedOwnedSkill(t *testing.T) {
	installDirectory := t.TempDir()
	skillDirectory := filepath.Join(t.TempDir(), "reconc")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"install-cli", "--skill-dir", skillDirectory, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("install-cli: %v stderr=%s", err, stderr.String())
	}
	if err := os.WriteFile(filepath.Join(skillDirectory, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	err := Run([]string{"doctor", "--global", "--json"}, "test", &stdout, &stderr)
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.ExitCode != 1 {
		t.Fatalf("global doctor with a modified owned skill = %v, want exit 1", err)
	}
	var report usercli.GlobalDiagnostic
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode global doctor: %v\n%s", err, stdout.String())
	}
	if report.Status != usercli.DiagnosticStale || len(report.Actions) != 1 ||
		report.Actions[0].Command != "reconc install-cli --skill-only" {
		t.Fatalf("global doctor JSON = status %s actions %+v next %q", report.Status, report.Actions, report.NextAction)
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
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
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
