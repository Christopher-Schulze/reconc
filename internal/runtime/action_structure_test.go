package runtime

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestFixPlanCarriesLiteralShellActionStructure(t *testing.T) {
	command := "printf 'two words; $HOME' && printf \"quoted\"`echo no-parse`\n"
	report := &CheckReport{
		Decision: DecisionBlock,
		RepoRoot: "/repo with spaces",
		Violations: []Violation{{
			RuleID: "run-gate", Kind: policy.KindRequireCommand, Mode: policy.ModeBlock,
			RequiredCommands: []string{command},
		}},
	}
	plan := BuildFixPlan(report)
	if len(plan.Remediations) != 1 || len(plan.Remediations[0].Actions) != 1 {
		t.Fatalf("typed action missing: %#v", plan.Remediations)
	}
	action := plan.Remediations[0].Actions[0]
	if action.Code != ActionRunCommand || action.Kind != ActionKindShell {
		t.Fatalf("action identity = %#v", action)
	}
	if action.Shell != command || len(action.Argv) != 0 || action.Cwd != report.RepoRoot {
		t.Fatalf("shell action changed literal values: %#v", action)
	}
	if action.Authorization != "operator_approval" || len(action.RequiredEvidence) != 1 || action.RequiredEvidence[0] != "command_executed" {
		t.Fatalf("shell action prerequisites = %#v", action)
	}
	body, err := json.Marshal(BuildFixPlan(report))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"actions"`)) {
		t.Fatalf("fix plan has no typed executable action: %s", body)
	}
	legacy, err := json.Marshal(BuildLegacyFixPlan(report))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacy, []byte(`"actions"`)) || !bytes.Contains(legacy, []byte(`"format_version":"1"`)) {
		t.Fatalf("legacy compatibility output changed shape: %s", legacy)
	}
	text := RenderFixPlanText(plan)
	for _, want := range []string{"Action: run_command (shell)", "Shell (literal):", "$HOME", "operator_approval"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text rendering missing %q: %s", want, text)
		}
	}
}

func TestFixPlanClaimActionPreservesArgvBoundaries(t *testing.T) {
	claim := "claim with spaces; $HOME `ticks`"
	plan := BuildFixPlan(&CheckReport{
		Decision: DecisionBlock,
		RepoRoot: "/repo",
		Violations: []Violation{{
			RuleID: "claim-gate", Kind: policy.KindRequireClaim, Mode: policy.ModeBlock,
			RequiredClaims: []string{claim},
		}},
	})
	action := plan.Remediations[0].Actions[0]
	want := []string{"reconc", "check", "--claim", claim}
	if action.Kind != ActionKindArgv || len(action.Argv) != len(want) {
		t.Fatalf("claim action shape = %#v", action)
	}
	for index := range want {
		if action.Argv[index] != want[index] {
			t.Fatalf("argv[%d] = %q, want %q", index, action.Argv[index], want[index])
		}
	}
}

func TestFixPlanUsesStableNonExecutableActionCodes(t *testing.T) {
	tests := []struct {
		kind policy.Kind
		code RemediationActionCode
	}{
		{kind: policy.KindRequireFreshFile, code: ActionRefresh},
		{kind: policy.KindRequireScript, code: ActionRetry},
		{kind: policy.KindDenyWrite, code: ActionRequestApproval},
		{kind: policy.KindRequireRead, code: ActionProvideEvidence},
		{kind: policy.KindForbidCommand, code: ActionInspect},
	}
	for _, test := range tests {
		plan := BuildFixPlan(&CheckReport{RepoRoot: "/repo", Violations: []Violation{{
			RuleID: "rule", Kind: test.kind, Mode: policy.ModeBlock,
		}}})
		actions := plan.Remediations[0].Actions
		if len(actions) != 1 || actions[0].Code != test.code {
			t.Fatalf("kind %s actions = %#v, want code %s", test.kind, actions, test.code)
		}
	}
}
