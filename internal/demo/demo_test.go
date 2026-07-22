package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

var demoTestBinary string

func TestMain(m *testing.M) {
	tempRoot, err := os.MkdirTemp("", "reconc-demo-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create demo test binary directory:", err)
		os.Exit(1)
	}
	binaryName := "reconc"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	demoTestBinary = filepath.Join(tempRoot, binaryName)
	command := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.Version=test-version", "-o", demoTestBinary, "../../cmd/reconc")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		_ = os.RemoveAll(tempRoot)
		fmt.Fprintln(os.Stderr, "build demo test binary:", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(tempRoot); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, "clean demo test binary directory:", err)
		code = 1
	}
	os.Exit(code)
}

func TestRunCompletesRealJourneyAndKeepsInspectableProof(t *testing.T) {
	tempRoot := filepath.Join(t.TempDir(), "custom temp root with spaces")
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		t.Fatalf("create temp root: %v", err)
	}
	before := currentRepositoryStatus(t)
	result, err := Run(context.Background(), Options{
		Executable: demoTestBinary,
		Version:    "test-version",
		TempRoot:   tempRoot,
		Keep:       true,
	})
	if err != nil {
		t.Fatalf("run demo: %v\nresult: %+v", err, result)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(result.WorkspacePath); err != nil {
			t.Errorf("clean kept workspace: %v", err)
		}
	})
	if after := currentRepositoryStatus(t); after != before {
		t.Fatalf("source repository changed during demo\nbefore: %q\nafter:  %q", before, after)
	}
	if err := VerifyResult(result); err != nil {
		t.Fatalf("verify result: %v", err)
	}
	if result.Status != "passed" || !result.Kept || result.Cleaned {
		t.Fatalf("unexpected terminal state: %+v", result)
	}
	if !strings.HasPrefix(result.WorkspacePath, tempRoot+string(filepath.Separator)) {
		t.Fatalf("workspace %q is outside temp root %q", result.WorkspacePath, tempRoot)
	}
	wantDecisions := map[string]string{
		"protected-action":     "block",
		"missing-proof":        "block",
		"remediation":          "remediate",
		"real-test":            "pass",
		"corrected-evaluation": "pass",
		"done":                 "done",
		"portable-proof":       "proof",
	}
	for _, step := range result.Steps {
		if want, ok := wantDecisions[step.ID]; ok {
			if step.Decision != want {
				t.Errorf("step %s decision = %q, want %q", step.ID, step.Decision, want)
			}
			delete(wantDecisions, step.ID)
		}
	}
	if len(wantDecisions) != 0 {
		t.Fatalf("missing required steps: %v", wantDecisions)
	}
	if len(result.Artifacts) != 4 || result.CompletionDigest == "" || result.ProofDigest == "" {
		t.Fatalf("incomplete proof contract: %+v", result)
	}
	for _, artifact := range result.Artifacts {
		if artifact.SHA256 == "" {
			t.Errorf("artifact %s has no digest", artifact.Kind)
		}
		if _, err := os.Stat(artifact.Path); err != nil {
			t.Errorf("inspect artifact %s: %v", artifact.Kind, err)
		}
	}
	var completion map[string]any
	proof, err := os.ReadFile(filepath.Join(result.WorkspacePath, "proof", "completion.json"))
	if err != nil {
		t.Fatalf("read completion proof: %v", err)
	}
	if err := json.Unmarshal(proof, &completion); err != nil {
		t.Fatalf("decode completion proof: %v", err)
	}
	if completion["decision"] != "pass" || completion["digest"] != result.CompletionDigest {
		t.Fatalf("completion proof does not match result: %v", completion)
	}
	var portable map[string]any
	bundle, err := os.ReadFile(filepath.Join(result.WorkspacePath, "proof", "bundle.json"))
	if err != nil {
		t.Fatalf("read portable proof: %v", err)
	}
	if err := json.Unmarshal(bundle, &portable); err != nil {
		t.Fatalf("decode portable proof: %v", err)
	}
	if portable["decision"] != "pass" || portable["digest"] != result.ProofDigest {
		t.Fatalf("portable proof does not match result: %v", portable)
	}
}

func TestRunCleansWorkspaceByDefault(t *testing.T) {
	result, err := Run(context.Background(), Options{Executable: demoTestBinary, Version: "test-version"})
	if err != nil {
		t.Fatalf("run demo: %v", err)
	}
	if !result.Cleaned || result.Kept {
		t.Fatalf("unexpected cleanup state: %+v", result)
	}
	if _, err := os.Stat(result.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists or stat failed unexpectedly: %v", err)
	}
	if err := VerifyResult(result); err != nil {
		t.Fatalf("verify cleaned result: %v", err)
	}
}

func TestRunFailsSafelyWhenGitIsUnavailable(t *testing.T) {
	missingGit := filepath.Join(t.TempDir(), "missing-git")
	result, err := Run(context.Background(), Options{
		Executable:    demoTestBinary,
		Version:       "test-version",
		GitExecutable: missingGit,
	})
	if err == nil || !strings.Contains(err.Error(), "demo prerequisite Git failed") {
		t.Fatalf("error = %v, want Git prerequisite failure", err)
	}
	assertFailedRunCleaned(t, result)
}

func TestRunRejectsDeclaredVersionDrift(t *testing.T) {
	result, err := Run(context.Background(), Options{
		Executable: demoTestBinary,
		Version:    "different-version",
	})
	if err == nil || !strings.Contains(err.Error(), `does not match expected "different-version"`) {
		t.Fatalf("error = %v, want version drift failure", err)
	}
	assertFailedRunCleaned(t, result)
}

func TestRunCleansWorkspaceAfterInterruption(t *testing.T) {
	tempRoot := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		for {
			entries, err := os.ReadDir(tempRoot)
			if err == nil && len(entries) > 0 {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	result, err := Run(ctx, Options{
		Executable: demoTestBinary,
		Version:    "test-version",
		TempRoot:   tempRoot,
	})
	cancel()
	<-watchDone
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	assertFailedRunCleaned(t, result)
}

func TestCommandFailureIsRecordedFromRealProcess(t *testing.T) {
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("locate Git: %v", err)
	}
	repo := t.TempDir()
	result := &Result{Steps: []Step{}}
	runner := &journey{
		ctx:        context.Background(),
		git:        gitExecutable,
		repo:       repo,
		env:        isolatedEnvironment(t.TempDir()),
		result:     result,
		executable: demoTestBinary,
	}
	step, err := runner.command(commandRequest{
		id:       "real-command-failure",
		label:    "Run a real failing Git command",
		execPath: gitExecutable,
		display:  []string{"git", "definitely-not-a-command"},
		args:     []string{"definitely-not-a-command"},
		expected: exitCodes(0),
	})
	if err == nil {
		t.Fatal("real failing command unexpectedly passed")
	}
	if step.ExitCode == 0 || step.Decision != "error" || step.Stderr == "" {
		t.Fatalf("failure was not captured: %+v", step)
	}
}

func TestRunIsStructurallyDeterministic(t *testing.T) {
	first, err := Run(context.Background(), Options{Executable: demoTestBinary, Version: "test-version"})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := Run(context.Background(), Options{Executable: demoTestBinary, Version: "test-version"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !reflect.DeepEqual(stepContracts(first), stepContracts(second)) {
		t.Fatalf("step contracts drifted\nfirst:  %#v\nsecond: %#v", stepContracts(first), stepContracts(second))
	}
	if !reflect.DeepEqual(artifactKinds(first), artifactKinds(second)) {
		t.Fatalf("artifact contract drifted: %v != %v", artifactKinds(first), artifactKinds(second))
	}
}

func TestVerifyResultRejectsTampering(t *testing.T) {
	result, err := Run(context.Background(), Options{Executable: demoTestBinary, Version: "test-version"})
	if err != nil {
		t.Fatalf("run demo: %v", err)
	}
	result.Status = "failed"
	if err := VerifyResult(result); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("verify tampered result = %v", err)
	}
}

func TestVerifyResultRejectsSemanticallyIncompletePass(t *testing.T) {
	result, err := Run(context.Background(), Options{Executable: demoTestBinary, Version: "test-version"})
	if err != nil {
		t.Fatalf("run demo: %v", err)
	}
	result.Steps = result.Steps[:len(result.Steps)-1]
	result.Digest = resultDigest(result)
	if err := VerifyResult(result); err == nil || !strings.Contains(err.Error(), "missing required steps: portable-proof") {
		t.Fatalf("verify incomplete result = %v", err)
	}
}

func TestRenderTextUsesRecordedDecisions(t *testing.T) {
	result := &Result{
		FormatVersion:    FormatVersion,
		ReconcVersion:    "test-version",
		Status:           "passed",
		Cleaned:          true,
		CompletionDigest: "completion-digest",
		ProofDigest:      "proof-digest",
		Steps: []Step{
			{ID: "protected-action", Label: "Attempt an out-of-scope write", Decision: "block"},
			{ID: "missing-proof", Decision: "block"},
			{ID: "remediation", Decision: "remediate"},
			{ID: "real-test", Decision: "pass"},
			{ID: "corrected-evaluation", Decision: "pass"},
			{ID: "done", Label: "Run the evidence-complete final gate", Decision: "done"},
			{ID: "portable-proof", Label: "Export the portable completion proof bundle", Decision: "proof"},
		},
		Artifacts: []Artifact{
			{Kind: "policy-lock", Path: "policy", SHA256: "policy-digest"},
			{Kind: "task-detail", Path: "task", SHA256: "task-digest"},
			{Kind: "completion-report", Path: "completion", SHA256: "report-digest"},
			{Kind: "proof-bundle", Path: "proof", SHA256: "proof-digest"},
		},
	}
	result.Digest = resultDigest(result)
	var output bytes.Buffer
	if err := RenderText(&output, result); err != nil {
		t.Fatalf("render text: %v", err)
	}
	for _, want := range []string{"[BLOCK] Attempt an out-of-scope write", "[DONE] Run the evidence-complete final gate", "Workspace: cleaned"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output missing %q:\n%s", want, output.String())
		}
	}
}

func assertFailedRunCleaned(t *testing.T, result *Result) {
	t.Helper()
	if result == nil || result.Status != "failed" || !result.Cleaned {
		t.Fatalf("failed run did not clean safely: %+v", result)
	}
	if _, err := os.Stat(result.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("failed workspace still exists or stat failed unexpectedly: %v", err)
	}
	if err := VerifyResult(result); err != nil {
		t.Fatalf("verify failed result: %v", err)
	}
}

func currentRepositoryStatus(t *testing.T) string {
	t.Helper()
	command := exec.Command("git", "status", "--porcelain")
	command.Dir = filepath.Join("..", "..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("read source repository status: %v", err)
	}
	return string(output)
}

func stepContracts(result *Result) [][4]any {
	contracts := make([][4]any, 0, len(result.Steps))
	for _, step := range result.Steps {
		command := make([]string, 0, len(step.Command))
		for _, argument := range step.Command {
			command = append(command, strings.ReplaceAll(argument, result.WorkspacePath, "<workspace>"))
		}
		contracts = append(contracts, [4]any{step.ID, command, step.ExitCode, step.Decision})
	}
	return contracts
}

func artifactKinds(result *Result) []string {
	kinds := make([]string, 0, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		kinds = append(kinds, artifact.Kind)
	}
	return kinds
}
