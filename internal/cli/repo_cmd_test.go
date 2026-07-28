package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
)

func TestRepoSyncPlanAndVerifyCLI(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	report, err := reconbootstrap.Initialize(reconbootstrap.InitRequest{
		RepoRoot: repo, Profile: reconbootstrap.ProfileGoverned, NoHooks: true,
	}, "0.9.0")
	if err != nil || report.Status != reconbootstrap.InitComplete {
		t.Fatalf("initialize: %+v err=%v", report, err)
	}
	planPath := filepath.Join(t.TempDir(), "sync-plan.json")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{
		"repo", "sync", "plan", repo, "--output", planPath, "--json",
	}, "0.9.0", &stdout, &stderr); err != nil {
		t.Fatalf("repo sync plan: %v stderr=%s", err, stderr.String())
	}
	var plan reconbootstrap.SyncPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.PlanDigest == "" || plan.RepoRoot != report.RepoRoot {
		t.Fatalf("sync plan JSON = %+v", plan)
	}
	loaded, err := reconbootstrap.LoadSyncPlan(planPath)
	if err != nil || loaded.PlanDigest != plan.PlanDigest {
		t.Fatalf("saved sync plan = %+v err=%v", loaded, err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{
		"repo", "sync", "verify", repo, "--json",
	}, "0.9.0", &stdout, &stderr); err != nil {
		t.Fatalf("repo sync verify: %v stderr=%s", err, stderr.String())
	}
	var verification reconbootstrap.SyncVerification
	if err := json.Unmarshal(stdout.Bytes(), &verification); err != nil {
		t.Fatal(err)
	}
	if !verification.Valid || verification.ReceiptDigest == "" {
		t.Fatalf("sync verification JSON = %+v", verification)
	}
}

func TestRepoSyncCLIRequiresExactApplyInputs(t *testing.T) {
	for _, args := range [][]string{
		{"repo", "sync"},
		{"repo", "sync", "apply"},
		{"repo", "sync", "apply", "--plan"},
		{"repo", "sync", "apply", "--digest"},
		{"repo", "sync", "apply", "--plan", "one", "--plan", "two"},
		{"repo", "sync", "apply", "--digest", strings.Repeat("a", 64), "--digest", strings.Repeat("b", 64)},
		{"repo", "sync", "apply", "--unknown"},
		{"repo", "sync", "plan", "--replace-output"},
		{"repo", "sync", "plan", "--output"},
		{"repo", "sync", "plan", "--output", "one", "--output", "two"},
		{"repo", "sync", "plan", "--unknown"},
		{"repo", "sync", "plan", "one", "two"},
		{"repo", "sync", "verify", "--unknown"},
		{"repo", "sync", "verify", "one", "two"},
		{"repo", "unknown"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(args, "0.9.0", &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "reconc repo:") {
			t.Fatalf("%v error = %v", args, err)
		}
	}
}

func TestRepoSyncHelpAndReadOnlyPlan(t *testing.T) {
	for _, args := range [][]string{
		{"repo", "--help"},
		{"repo", "sync", "--help"},
		{"repo", "sync", "plan", "--help"},
		{"repo", "sync", "apply", "--help"},
		{"repo", "sync", "verify", "--help"},
	} {
		var stdout, stderr bytes.Buffer
		if err := Run(args, "0.9.0", &stdout, &stderr); err != nil {
			t.Fatalf("%v: %v stderr=%s", args, err, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage: reconc repo sync plan") {
			t.Fatalf("%v help = %s", args, stdout.String())
		}
	}

	repo := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	report, err := reconbootstrap.Initialize(reconbootstrap.InitRequest{
		RepoRoot: repo, Profile: reconbootstrap.ProfileGoverned, NoHooks: true,
	}, "0.9.0")
	if err != nil || report.Status != reconbootstrap.InitComplete {
		t.Fatalf("initialize: %+v err=%v", report, err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"repo", "sync", "plan", repo}, "0.9.0", &stdout, &stderr); err != nil {
		t.Fatalf("read-only plan: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "rerun with --output PATH") {
		t.Fatalf("read-only plan output = %s", stdout.String())
	}
}

func TestRepoSyncTextLifecycle(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	installDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	report, err := reconbootstrap.Initialize(reconbootstrap.InitRequest{
		RepoRoot: repo, Profile: reconbootstrap.ProfileGoverned, NoHooks: true,
	}, "0.9.0")
	if err != nil || report.Status != reconbootstrap.InitComplete {
		t.Fatalf("initialize: %+v err=%v", report, err)
	}
	planPath := filepath.Join(t.TempDir(), "sync-plan.json")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{
		"repo", "sync", "plan", repo, "--output", planPath,
	}, "0.9.0", &stdout, &stderr); err != nil {
		t.Fatalf("text plan: %v stderr=%s", err, stderr.String())
	}
	plan, err := reconbootstrap.LoadSyncPlan(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Plan digest: "+plan.PlanDigest) ||
		!strings.Contains(stdout.String(), "repo sync apply") {
		t.Fatalf("text plan output = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{
		"repo", "sync", "apply", "--plan", planPath, "--digest", plan.PlanDigest,
	}, "0.9.0", &stdout, &stderr); err != nil {
		t.Fatalf("text apply: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Status: complete") ||
		!strings.Contains(stdout.String(), "Repository-owned Reconc artifacts already match") {
		t.Fatalf("text apply output = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{
		"repo", "sync", "verify", repo,
	}, "0.9.0", &stdout, &stderr); err != nil {
		t.Fatalf("text verify: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "PASS repository-receipt") ||
		!strings.Contains(stdout.String(), "Repository-owned Reconc artifacts are verified") {
		t.Fatalf("text verify output = %s", stdout.String())
	}
}
