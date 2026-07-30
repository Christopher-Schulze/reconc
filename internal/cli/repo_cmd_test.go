package cli

import (
	"bytes"
	"encoding/json"
	"os"
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

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{
		"repo", "sync", "recover", repo, "--json",
	}, "0.9.0", &stdout, &stderr); err != nil {
		t.Fatalf("repo sync recover: %v stderr=%s", err, stderr.String())
	}
	var recovery reconbootstrap.SyncRecovery
	if err := json.Unmarshal(stdout.Bytes(), &recovery); err != nil {
		t.Fatal(err)
	}
	if recovery.Status != reconbootstrap.SyncRecoveryClean ||
		recovery.NextAction != "No repository sync recovery is required." {
		t.Fatalf("sync recovery JSON = %+v", recovery)
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
		{"repo", "sync", "resolve"},
		{"repo", "sync", "resolve", "--plan", "one", "--plan", "two"},
		{"repo", "sync", "resolve", "--plan", "one", "--digest", strings.Repeat("a", 64), "--path", "x", "--strategy", "use-binary", "--binary", "x"},
		{"repo", "sync", "resolve", "--plan", "one", "--digest", strings.Repeat("a", 64), "--path", "x", "--strategy", "unknown"},
		{"repo", "sync", "resolve", "--unknown"},
		{"repo", "sync", "plan", "--replace-output"},
		{"repo", "sync", "plan", "--output"},
		{"repo", "sync", "plan", "--output", "one", "--output", "two"},
		{"repo", "sync", "plan", "--unknown"},
		{"repo", "sync", "plan", "one", "two"},
		{"repo", "sync", "verify", "--unknown"},
		{"repo", "sync", "verify", "one", "two"},
		{"repo", "sync", "recover", "--unknown"},
		{"repo", "sync", "recover", "one", "two"},
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
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"repo", "--help"}, want: "Usage: reconc repo sync"},
		{args: []string{"repo", "sync", "--help"}, want: "Usage: reconc repo sync <plan|apply|resolve|verify|recover>"},
		{args: []string{"repo", "sync", "plan", "--help"}, want: "Usage: reconc repo sync plan"},
		{args: []string{"repo", "sync", "apply", "--help"}, want: "Usage: reconc repo sync apply"},
		{args: []string{"repo", "sync", "resolve", "--help"}, want: "Usage: reconc repo sync resolve"},
		{args: []string{"repo", "sync", "verify", "--help"}, want: "Usage: reconc repo sync verify"},
		{args: []string{"repo", "sync", "recover", "--help"}, want: "Usage: reconc repo sync recover"},
	} {
		var stdout, stderr bytes.Buffer
		if err := Run(test.args, "0.9.0", &stdout, &stderr); err != nil {
			t.Fatalf("%v: %v stderr=%s", test.args, err, stderr.String())
		}
		if !strings.Contains(stdout.String(), test.want) {
			t.Fatalf("%v help = %s, want %q", test.args, stdout.String(), test.want)
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
	if !strings.Contains(stdout.String(), "repo sync plan") ||
		!strings.Contains(stdout.String(), "--output") {
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
	if !strings.Contains(stdout.String(), "Checks:") ||
		!strings.Contains(stdout.String(), "0 FAIL") ||
		!strings.Contains(stdout.String(), "Repository-owned Reconc artifacts are verified") {
		t.Fatalf("text verify output = %s", stdout.String())
	}
}

func TestRepoSyncResolveCLI(t *testing.T) {
	repo := t.TempDir()
	installDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	report, err := reconbootstrap.Initialize(reconbootstrap.InitRequest{
		RepoRoot: repo, Profile: reconbootstrap.ProfileAdvanced, NoHooks: true,
	}, "0.9.0")
	if err != nil || report.Status != reconbootstrap.InitComplete {
		t.Fatalf("initialize: %+v err=%v", report, err)
	}
	receipt, err := reconbootstrap.LoadRepositoryReceipt(repo)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := ""
	targetMode := uint32(0)
	for _, file := range receipt.ManagedFiles {
		if strings.HasPrefix(file.Component, "harness-pack:") {
			targetPath = file.Path
			targetMode = file.Mode
			break
		}
	}
	if targetPath == "" {
		t.Fatal("advanced fixture has no harness-owned file")
	}
	if err := os.WriteFile(
		filepath.Join(repo, filepath.FromSlash(targetPath)),
		[]byte("CLI drift\n"), os.FileMode(targetMode),
	); err != nil {
		t.Fatal(err)
	}
	plan, err := reconbootstrap.BuildSyncPlan(repo, "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "blocked-plan.json")
	if _, err := reconbootstrap.WriteSyncPlan(planPath, plan); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err = Run([]string{
		"repo", "sync", "resolve", "--plan", planPath,
		"--digest", plan.PlanDigest, "--path", targetPath,
		"--strategy", "use-target", "--json",
	}, "0.9.0", &stdout, &stderr)
	if err != nil {
		t.Fatalf("resolve CLI: %v stderr=%s", err, stderr.String())
	}
	var resolution reconbootstrap.SyncResolutionReport
	if err := json.Unmarshal(stdout.Bytes(), &resolution); err != nil {
		t.Fatal(err)
	}
	if resolution.Status != reconbootstrap.SyncComplete ||
		resolution.Strategy != reconbootstrap.SyncUseTarget {
		t.Fatalf("resolve CLI JSON = %+v", resolution)
	}
}
