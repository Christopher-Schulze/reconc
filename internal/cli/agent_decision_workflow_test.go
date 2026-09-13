package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/runtime"
)

func TestDirectNextKeepsFullCheckAndFailedCommandEvidence(t *testing.T) {
	repo := makeCheckRepo(t, `rules:
  - id: architecture-read
    kind: require_read
    paths: ['src/**']
    before_paths: ['ARCHITECTURE.md']
    mode: block
    message: read architecture first
  - id: tests-pass
    kind: require_command_success
    when_paths: ['src/**']
    commands: ['go test ./...']
    mode: block
    message: tests must pass
`)
	for path, content := range map[string]string{
		"ARCHITECTURE.md": "# Architecture\n",
		"go.mod":          "module example.com/reconc-workflow\n\ngo 1.22\n",
		"src/main.go":     "package src\n",
	} {
		fullPath := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lockPath := filepath.Join(repo, ".reconc", "policy.lock.json")
	lockBefore, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	invoke := func(args ...string) ([]byte, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		err := Run(args, "test", &stdout, &stderr)
		if stderr.Len() > 0 {
			t.Fatalf("%v emitted stderr: %s", args, stderr.String())
		}
		return stdout.Bytes(), err
	}
	full, checkErr := invoke("check", repo, "--write", "src/main.go", "--json")
	if checkErr == nil || ExitCode(checkErr) != 2 {
		t.Fatalf("full check must block: %v\n%s", checkErr, full)
	}
	var report struct {
		Violations []struct {
			RuleID string `json:"rule_id"`
		} `json:"violations"`
	}
	if err := json.Unmarshal(full, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Violations) != 2 || report.Violations[0].RuleID != "architecture-read" || report.Violations[1].RuleID != "tests-pass" {
		t.Fatalf("full check must retain both blockers: %s", full)
	}

	first, nextErr := invoke("next", repo, "--write", "src/main.go", "--json")
	if nextErr == nil || ExitCode(nextErr) != 2 {
		t.Fatalf("direct next must block: %v\n%s", nextErr, first)
	}
	var firstAction runtime.Remediation
	if err := json.Unmarshal(first, &firstAction); err != nil {
		t.Fatal(err)
	}
	if firstAction.RuleID != report.Violations[0].RuleID || len(firstAction.Actions) == 0 ||
		firstAction.Actions[0].Kind != runtime.ActionKindEvidence ||
		len(firstAction.Actions[0].RequiredEvidence) != 1 ||
		firstAction.Actions[0].RequiredEvidence[0] != "ARCHITECTURE.md" {
		t.Fatalf("direct next lost first typed action: %s", first)
	}

	failed := exec.Command("go", "test", "./...")
	failed.Dir = repo
	failed.Env = append(os.Environ(), "GOFLAGS=-invalid-reconc-test-flag")
	if output, err := failed.CombinedOutput(); err == nil {
		t.Fatalf("command fixture unexpectedly passed: %s", output)
	}
	second, nextErr := invoke("next", repo, "--write", "src/main.go", "--read", "ARCHITECTURE.md",
		"--command-failure", "go test ./...", "--json")
	if nextErr == nil || ExitCode(nextErr) != 2 {
		t.Fatalf("failed command cannot satisfy policy: %v\n%s", nextErr, second)
	}
	var secondAction runtime.Remediation
	if err := json.Unmarshal(second, &secondAction); err != nil {
		t.Fatal(err)
	}
	if secondAction.RuleID != "tests-pass" || len(secondAction.Actions) == 0 ||
		secondAction.Actions[0].Kind != runtime.ActionKindShell || secondAction.Actions[0].Shell != "go test ./..." ||
		secondAction.Actions[0].Cwd != repo ||
		secondAction.Actions[0].Authorization != "operator_approval" ||
		len(secondAction.Actions[0].RequiredEvidence) != 1 || secondAction.Actions[0].RequiredEvidence[0] != "command_success" {
		t.Fatalf("failed command remediation lost execution contract: %s", second)
	}

	passed := exec.Command("go", "test", "./...")
	passed.Dir = repo
	if output, err := passed.CombinedOutput(); err != nil {
		t.Fatalf("required command did not pass: %v\n%s", err, output)
	}
	clear, nextErr := invoke("next", repo, "--write", "src/main.go", "--read", "ARCHITECTURE.md",
		"--command-success", "go test ./...", "--json")
	if nextErr != nil {
		t.Fatalf("direct next after real evidence: %v\n%s", nextErr, clear)
	}
	var clearState struct {
		RemediationCount *int   `json:"remediation_count"`
		Summary          string `json:"summary"`
	}
	if err := json.Unmarshal(clear, &clearState); err != nil || clearState.RemediationCount == nil ||
		*clearState.RemediationCount != 0 || clearState.Summary == "" {
		t.Fatalf("direct next should be clear: %v\n%s", err, clear)
	}
	final, checkErr := invoke("check", repo, "--write", "src/main.go", "--read", "ARCHITECTURE.md",
		"--command-success", "go test ./...", "--json")
	if checkErr != nil {
		t.Fatalf("full check after real evidence: %v\n%s", checkErr, final)
	}
	if err := json.Unmarshal(final, &report); err != nil || len(report.Violations) != 0 {
		t.Fatalf("full check did not clear all blockers: %v\n%s", err, final)
	}
	lockAfter, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lockBefore, lockAfter) {
		t.Fatal("read-only decision loop changed compiled policy")
	}
}
