package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
	"reconc.dev/reconc/internal/usercli"
)

func TestBootstrapPhasesGovernedRoundTrip(t *testing.T) {
	repo := t.TempDir()
	planPath := filepath.Join(t.TempDir(), "bootstrap-plan.json")

	var stdout, stderr bytes.Buffer
	if err := Run([]string{
		"bootstrap", "plan", repo, "--profile", "governed", "--output", planPath, "--json",
	}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("plan: %v stderr=%s", err, stderr.String())
	}
	var plan reconbootstrap.Plan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, stdout.String())
	}
	if plan.Selection.Profile != reconbootstrap.ProfileGoverned || plan.PlanDigest == "" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("plan file missing: %v", err)
	}

	stdout.Reset()
	if err := Run([]string{"bootstrap", "apply", "--plan", planPath, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("apply: %v stderr=%s", err, stderr.String())
	}
	var report reconbootstrap.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode apply report: %v\n%s", err, stdout.String())
	}
	if report.Status != reconbootstrap.ApplyComplete {
		t.Fatalf("apply status = %s, want complete: %+v", report.Status, report)
	}

	stdout.Reset()
	if err := Run([]string{"bootstrap", "verify", "--plan", planPath, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("verify: %v stderr=%s", err, stderr.String())
	}
	var verification reconbootstrap.Verification
	if err := json.Unmarshal(stdout.Bytes(), &verification); err != nil {
		t.Fatalf("decode verification: %v\n%s", err, stdout.String())
	}
	if !verification.Valid {
		t.Fatalf("verification invalid: %+v", verification)
	}

	stdout.Reset()
	if err := Run([]string{
		"bootstrap", "apply", repo, "--profile", "governed", "--json",
	}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("idempotent inline apply: %v stderr=%s", err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode idempotent report: %v\n%s", err, stdout.String())
	}
	if report.Status != reconbootstrap.ApplyComplete || len(report.Created) != 0 || len(report.Unchanged) == 0 {
		t.Fatalf("idempotent apply changed repository: %+v", report)
	}

	stdout.Reset()
	if err := Run([]string{"bootstrap", "remove", "--plan", planPath, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("remove: %v stderr=%s", err, stderr.String())
	}
	var removal reconbootstrap.RemovalReport
	if err := json.Unmarshal(stdout.Bytes(), &removal); err != nil {
		t.Fatalf("decode removal report: %v\n%s", err, stdout.String())
	}
	if removal.Status != reconbootstrap.RemovalComplete || len(removal.Removed) == 0 {
		t.Fatalf("removal did not reverse owned files: %+v", removal)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc.yml")); !os.IsNotExist(err) {
		t.Fatalf("bootstrap-owned policy remains after removal: %v", err)
	}
}

func TestBootstrapApplyReportsDriftWithoutOverwriting(t *testing.T) {
	repo := t.TempDir()
	agentsPath := filepath.Join(repo, "AGENTS.md")
	original := []byte("# Custom instructions\n")
	if err := os.WriteFile(agentsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{
		"bootstrap", "apply", repo, "--profile", "minimal", "--json",
	}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("drift apply error = %v, want exit 1", err)
	}
	var report reconbootstrap.Report
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode drift report: %v\n%s", decodeErr, stdout.String())
	}
	if report.Status != reconbootstrap.ApplyDrift || len(report.Candidates) != 1 {
		t.Fatalf("unexpected drift report: %+v", report)
	}
	current, readErr := os.ReadFile(agentsPath)
	if readErr != nil || !bytes.Equal(current, original) {
		t.Fatalf("custom AGENTS.md changed: err=%v content=%q", readErr, current)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".reconc.yml")); !os.IsNotExist(statErr) {
		t.Fatalf("policy should not be installed during drift preflight: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(repo, filepath.FromSlash(report.Candidates[0]))); statErr != nil {
		t.Fatalf("candidate missing: %v", statErr)
	}
}

func TestBootstrapInspectIsMachineReadableAndPlanRequiresProfile(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"bootstrap", "inspect", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	var inspection reconbootstrap.Inspection
	if err := json.Unmarshal(stdout.Bytes(), &inspection); err != nil {
		t.Fatalf("decode inspection: %v\n%s", err, stdout.String())
	}
	if len(inspection.DetectedStacks) != 1 || inspection.DetectedStacks[0] != "go" {
		t.Fatalf("detected stacks = %v, want go", inspection.DetectedStacks)
	}

	stdout.Reset()
	err := Run([]string{"bootstrap", "plan", repo}, "test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--profile is required") {
		t.Fatalf("missing profile error = %v", err)
	}
}

func TestBootstrapProfilesExposeExistingRepositoryWiring(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"bootstrap", "profiles", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("profiles: %v stderr=%s", err, stderr.String())
	}
	var profiles []reconbootstrap.Profile
	if err := json.Unmarshal(stdout.Bytes(), &profiles); err != nil {
		t.Fatalf("decode profiles: %v", err)
	}
	for _, profile := range profiles {
		if profile.Name == reconbootstrap.ProfileExisting {
			if profile.Policy || profile.AgentDoc || profile.Tasks || profile.Docs || profile.Ignores || !profile.Wrapper {
				t.Fatalf("existing profile owns the wrong surfaces: %+v", profile)
			}
			return
		}
	}
	t.Fatal("existing bootstrap profile missing")
}

func TestBootstrapLegacyRejectsForce(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"bootstrap", t.TempDir(), "--force"}, "test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--force is unsupported") {
		t.Fatalf("force error = %v", err)
	}
}

func TestBootstrapApplyFailureStillEmitsRollbackReportJSON(t *testing.T) {
	repo := t.TempDir()
	planPath := filepath.Join(t.TempDir(), "bootstrap-plan.json")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{
		"bootstrap", "plan", repo, "--profile", "minimal", "--output", planPath, "--json",
	}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("external edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	err := Run([]string{"bootstrap", "apply", "--plan", planPath, "--json"}, "test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "plan is stale") {
		t.Fatalf("stale apply error = %v", err)
	}
	var report reconbootstrap.Report
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode failed-apply report: %v\n%s", decodeErr, stdout.String())
	}
	if report.Status != reconbootstrap.ApplyRolledBack || !strings.Contains(report.NextAction, "plan is stale") {
		t.Fatalf("failed apply report lost rollback truth: %+v", report)
	}
}

func TestBootstrapSelectionRejectsAmbiguousSingletonFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{
		"bootstrap", "plan", t.TempDir(), "--profile", "minimal", "--profile", "governed",
	}, "test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "only once") {
		t.Fatalf("duplicate singleton flag error = %v", err)
	}
}

func TestBootstrapApplyInstallsTheRunningBuildAndRequiresItOnPATHBeforeWriting(t *testing.T) {
	repo := t.TempDir()
	planPath := filepath.Join(t.TempDir(), "bootstrap-plan.json")
	installDirectory := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{
		"bootstrap", "plan", repo, "--profile", "minimal", "--output", planPath, "--json",
	}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", t.TempDir())
	stdout.Reset()
	err := Run([]string{"bootstrap", "apply", "--plan", planPath, "--json"}, "test", &stdout, &stderr)
	status, inspectErr := usercli.InspectCurrent(installDirectory)
	if inspectErr != nil || !status.Installed || !status.Current {
		t.Fatalf("bootstrap did not install the running build: status=%+v err=%v", status, inspectErr)
	}
	if err == nil ||
		!strings.Contains(err.Error(), "was installed but is not directly callable from PATH") ||
		status.NextAction == "" ||
		!strings.Contains(err.Error(), status.NextAction) {
		t.Fatalf("user CLI preflight error = %v, want native remediation %q", err, status.NextAction)
	}
	if entries, readErr := os.ReadDir(repo); readErr != nil || len(entries) != 0 {
		t.Fatalf("failed user CLI preflight mutated repo: entries=%v err=%v", entries, readErr)
	}
}

func TestInstallCLICommandPublishesAReadyBareCommand(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("RECONC_INSTALL_DIR", directory)
	t.Setenv("PATH", directory)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"install-cli", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("install-cli: %v stderr=%s", err, stderr.String())
	}
	var report usercli.InstallReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode install-cli report: %v\n%s", err, stdout.String())
	}
	if report.Status == nil || !report.Status.Ready || report.Status.ResolvedPath == "" {
		t.Fatalf("install-cli did not publish a bare command: %+v", report)
	}
}
