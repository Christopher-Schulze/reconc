package agentsession

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestStopEvaluationRetriesBoundedlyWhenScriptMutatesDeclaredInput(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	repo := setupStopBenchmarkRepo(t)
	script := filepath.Join(repo, ".reconc", "scripts", "mutate.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf x >> mutation.txt\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	policyText := "rules:\n" +
		"  - id: unstable-script\n" +
		"    kind: require_script\n" +
		"    when_paths: ['src/**']\n" +
		"    script: .reconc/scripts/mutate.sh\n" +
		"    cache_inputs: [mutation.txt]\n" +
		"    mode: block\n" +
		"    message: unstable\n"
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(policyText), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	stopGenerationGit(t, repo, "add", "-A")
	stopGenerationGit(t, repo, "commit", "-m", "unstable policy fixture", "--quiet")
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), []byte("package fixture\n// dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "unstable-stop")
	if err != nil {
		t.Fatal(err)
	}
	state = RecordWriteEvent(state, []string{"src/a.go"})
	if err := SaveSessionState(state); err != nil {
		t.Fatal(err)
	}
	scanCache := &stopPolicyScanCache{}
	taskSnapshot, err := captureStopTaskSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	gitSnapshot := stopPolicyGitSnapshotFor(repo)
	before := captureStopPolicyBoundarySnapshot(
		stopCaptureBeforeEvaluation, repo, state, mustStopPolicyEvidenceRevision(state), taskSnapshot, gitSnapshot, scanCache,
	)
	input := stopPolicyFingerprintInputForSnapshotWithScan(repo, state, gitSnapshot, taskSnapshot, before.generationCapture(), scanCache)
	if !stopPolicyFingerprintCacheableWithScan(input, scanCache) {
		t.Fatalf("unstable retry fixture is not cacheable: %+v scan=%+v", input, scanCache.scan)
	}
	_, err = runStopPolicyCheckWithSnapshot(repo, state)
	if err == nil || !strings.Contains(err.Error(), "changed during 3 consecutive policy evaluations") {
		t.Fatalf("unstable Stop did not exhaust its bounded retry contract: %v", err)
	}
	body, readErr := os.ReadFile(filepath.Join(repo, "mutation.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(body) != maxStopPolicyStabilityRuns {
		t.Fatalf("script executions = %d, want %d", len(body), maxStopPolicyStabilityRuns)
	}
}
