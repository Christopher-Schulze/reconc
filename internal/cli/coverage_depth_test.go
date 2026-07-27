package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
	"reconc.dev/reconc/internal/tasklifecycle"
)

func TestHookEvidenceStatusCLIParsesEverySupportedShape(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		name       string
		args       []string
		wantOutput string
		wantError  string
	}{
		{name: "default text", args: []string{repo}, wantOutput: "evidence taint: none\n"},
		{name: "json", args: []string{repo, "--json"}, wantOutput: `"present": false`},
		{name: "help", args: []string{"--help"}, wantOutput: "Usage: reconc hook evidence-status"},
		{name: "unknown flag", args: []string{"--unknown"}, wantError: `unknown flag "--unknown"`},
		{name: "extra repo", args: []string{repo, repo}, wantError: "accepts at most one repo path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := runHookEvidenceStatus(test.args, &stdout)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want substring %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("runHookEvidenceStatus: %v", err)
			}
			if !strings.Contains(stdout.String(), test.wantOutput) {
				t.Fatalf("stdout = %q, want substring %q", stdout.String(), test.wantOutput)
			}
			if test.name == "json" {
				var decoded map[string]interface{}
				if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
					t.Fatalf("decode status JSON: %v", err)
				}
				if present, ok := decoded["present"].(bool); !ok || present {
					t.Fatalf("present = %#v, want false", decoded["present"])
				}
			}
		})
	}
}

func TestHookEvidenceResolveCLIRejectsIncompleteAndUnresolvableRequests(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		name       string
		args       []string
		wantOutput string
		wantError  string
	}{
		{name: "help", args: []string{"--help"}, wantOutput: "Usage: reconc hook evidence-resolve"},
		{name: "missing token value", args: []string{repo, "--token"}, wantError: "--token requires a value"},
		{name: "missing reason value", args: []string{repo, "--reason"}, wantError: "--reason requires a value"},
		{name: "unknown flag", args: []string{repo, "--unknown"}, wantError: `unknown flag "--unknown"`},
		{name: "extra repo", args: []string{repo, repo, "--token", "token", "--reason", "reviewed"}, wantError: "accepts exactly one repo path"},
		{name: "missing required fields", args: []string{repo}, wantError: "usage:"},
		{name: "no persisted taint", args: []string{repo, "--token", "token", "--reason", "reviewed", "--json"}, wantError: "no persisted evidence taint exists"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := runHookEvidenceResolve(test.args, &stdout)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want substring %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("runHookEvidenceResolve: %v", err)
			}
			if !strings.Contains(stdout.String(), test.wantOutput) {
				t.Fatalf("stdout = %q, want substring %q", stdout.String(), test.wantOutput)
			}
		})
	}
}

func TestRunHookRoutesEvidenceCommands(t *testing.T) {
	repo := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runHook([]string{"evidence-status", repo}, &stdout, &stderr); err != nil {
		t.Fatalf("runHook evidence-status: %v", err)
	}
	if stdout.String() != "evidence taint: none\n" {
		t.Fatalf("evidence-status stdout = %q", stdout.String())
	}

	stdout.Reset()
	err := runHook([]string{"evidence-resolve", repo, "--token", "token", "--reason", "reviewed"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no persisted evidence taint exists") {
		t.Fatalf("evidence-resolve error = %v", err)
	}
}

func TestBootstrapTextRenderersExposeEveryOperationalField(t *testing.T) {
	var stdout bytes.Buffer
	renderBootstrapEvidence(&stdout, "Stacks", nil)
	renderBootstrapEvidence(&stdout, "Stacks", []reconbootstrap.DetectionEvidence{{
		Name:  "go",
		Paths: []string{"go.mod", "cmd/reconc"},
	}})
	renderBootstrapReport(&stdout, &reconbootstrap.Report{
		RepoRoot:    "/repo",
		Status:      reconbootstrap.ApplyDrift,
		Created:     []string{"created"},
		Unchanged:   []string{"unchanged"},
		Candidates:  []string{"candidate"},
		RolledBack:  []string{"rollback"},
		ReceiptPath: "receipt.json",
		Summary: reconbootstrap.ApplySummary{
			Created: 1, Preserved: 2, Drifted: 3, Skipped: 4,
			Installed: 5, Configured: 6, Live: 7, LivenessKnown: false,
		},
		NextAction: "review",
	})
	renderBootstrapRemoval(&stdout, &reconbootstrap.RemovalReport{
		RepoRoot: "/repo", Status: reconbootstrap.RemovalComplete,
		Removed: []string{"removed"}, Updated: []string{"updated"},
		Preserved: []string{"preserved"}, Candidates: []string{"candidate"},
		RolledBack: []string{"rollback"}, ReceiptPath: "remove.json", NextAction: "done",
	})
	renderBootstrapVerification(&stdout, &reconbootstrap.Verification{
		RepoRoot: "/repo",
		Valid:    false,
		Checks: []reconbootstrap.Check{
			{Name: "binary", Status: "pass", Detail: "current"},
			{Name: "hooks", Status: "fail", Detail: "missing"},
		},
		NextAction: "repair",
	})

	output := stdout.String()
	for _, want := range []string{
		"Stacks: unknown",
		"go <- go.mod, cmd/reconc",
		"Bootstrap apply: drift",
		"live=unknown",
		"Receipt: receipt.json",
		"Bootstrap remove: complete",
		"Removed: removed",
		"Bootstrap verify: valid=false",
		"binary: current",
		"Next: repair",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("renderer output missing %q:\n%s", want, output)
		}
	}
}

func TestWriteTaskBriefingRendersEmptyAndBoundedDetailStates(t *testing.T) {
	var stdout bytes.Buffer
	writeTaskBriefing(&stdout, tasklifecycle.Briefing{
		Profile:     tasklifecycle.ProfileSections,
		Remediation: "claim the next task",
	})
	if !strings.Contains(stdout.String(), "Current: none") {
		t.Fatalf("empty briefing output = %q", stdout.String())
	}

	stdout.Reset()
	writeTaskBriefing(&stdout, tasklifecycle.Briefing{
		Profile: tasklifecycle.ProfileSections,
		Current: &tasklifecycle.BriefingTask{
			ID: "0092", Title: "Raise coverage", Path: "tasks/0092.md", CurrentSubTask: "Add strict tests",
		},
		Blockers:         []tasklifecycle.BriefingBlocker{{ID: "0091", Reason: "waiting for CI"}},
		OmittedBlockers:  2,
		RequiredEvidence: []string{"tests", "coverage"},
		OmittedEvidence:  1,
		Remediation:      "finish the active sub-task",
	})
	output := stdout.String()
	for _, want := range []string{
		"Current: 0092 Raise coverage -> tasks/0092.md",
		"Sub-Task: Add strict tests",
		"Blocked: 0091 waiting for CI",
		"Blocked: +2 more",
		"Evidence: tests, coverage",
		"Evidence: +1 more",
		"Next: finish the active sub-task",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("briefing output missing %q:\n%s", want, output)
		}
	}
}
