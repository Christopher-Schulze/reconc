package runtime

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestBuildFixPlanFromNilReport(t *testing.T) {
	p := BuildFixPlan(nil)
	if p.Schema != FixPlanSchema {
		t.Errorf("schema wrong: %s", p.Schema)
	}
	if len(p.Remediations) != 0 {
		t.Errorf("expected zero remediations, got %d", len(p.Remediations))
	}
}

func TestBuildFixPlanPassReport(t *testing.T) {
	report := &CheckReport{Decision: DecisionPass, OK: true}
	p := BuildFixPlan(report)
	if p.RemediationCount != 0 {
		t.Errorf("expected 0 remediations for passing report, got %d", p.RemediationCount)
	}
	if !strings.Contains(p.Summary, "No remediation") {
		t.Errorf("expected pass summary, got: %s", p.Summary)
	}
}

func TestBuildFixPlanWithViolations(t *testing.T) {
	report := &CheckReport{
		Decision:               DecisionBlock,
		ViolationCount:         2,
		BlockingViolationCount: 1,
		Violations: []Violation{
			{
				RuleID:       "r1",
				Kind:         policy.KindDenyWrite,
				Mode:         policy.ModeBlock,
				MatchedPaths: []string{"gen/x.go"},
			},
			{
				RuleID:         "r2",
				Kind:           policy.KindRequireClaim,
				Mode:           policy.ModeWarn,
				RequiredClaims: []string{"ci-green"},
			},
		},
	}
	p := BuildFixPlan(report)
	if p.RemediationCount != 2 {
		t.Errorf("expected 2 remediations, got %d", p.RemediationCount)
	}
	if p.Remediations[0].Priority != "blocking" {
		t.Errorf("first remediation should be blocking, got %s", p.Remediations[0].Priority)
	}
	if p.Remediations[1].Priority != "non-blocking" {
		t.Errorf("second remediation should be non-blocking, got %s", p.Remediations[1].Priority)
	}
	if len(p.Remediations[1].SuggestedClaims) != 1 || p.Remediations[1].SuggestedClaims[0] != "ci-green" {
		t.Errorf("require_claim should produce SuggestedClaims, got %v", p.Remediations[1].SuggestedClaims)
	}
}

func TestBuildFixPlanRequireCommandSuggests(t *testing.T) {
	report := &CheckReport{
		Decision:       DecisionBlock,
		ViolationCount: 1,
		Violations: []Violation{
			{
				RuleID:           "tests-must-pass",
				Kind:             policy.KindRequireCommand,
				Mode:             policy.ModeBlock,
				RequiredCommands: []string{"go test ./..."},
			},
		},
	}
	p := BuildFixPlan(report)
	if len(p.Remediations[0].SuggestedCommands) != 1 {
		t.Errorf("expected SuggestedCommands populated, got %v", p.Remediations[0].SuggestedCommands)
	}
}

func TestBuildFixPlanForbidCommandShowsForbidden(t *testing.T) {
	report := &CheckReport{
		Decision:       DecisionBlock,
		ViolationCount: 1,
		Violations: []Violation{
			{
				RuleID:          "no-rm-rf",
				Kind:            policy.KindForbidCommand,
				Mode:            policy.ModeBlock,
				MatchedCommands: []string{"rm -rf /"},
			},
		},
	}
	p := BuildFixPlan(report)
	if len(p.Remediations[0].ForbiddenCommands) != 1 {
		t.Errorf("expected ForbiddenCommands populated, got %v", p.Remediations[0].ForbiddenCommands)
	}
}

func TestBuildFixPlanFilesToInspect(t *testing.T) {
	report := &CheckReport{
		Decision:       DecisionBlock,
		ViolationCount: 1,
		Violations: []Violation{
			{
				RuleID:        "couple",
				Kind:          policy.KindCoupleChange,
				Mode:          policy.ModeBlock,
				SourcePath:    "policies/x.yml",
				MatchedPaths:  []string{"src/main.go"},
				RequiredPaths: []string{"tests/**"},
			},
		},
	}
	p := BuildFixPlan(report)
	files := p.Remediations[0].FilesToInspect
	if len(files) != 3 {
		t.Errorf("expected 3 files to inspect, got %v", files)
	}
}

func TestBuildFixPlanStepsPerKind(t *testing.T) {
	for _, kind := range []policy.Kind{
		policy.KindDenyWrite,
		policy.KindRequireRead,
		policy.KindRequireCommand,
		policy.KindRequireClaim,
		policy.KindCoupleChange,
		policy.KindRequireFreshFile,
		policy.KindRequireEvidence,
		policy.KindAllOf,
		policy.KindRequireScript,
	} {
		report := &CheckReport{
			Decision:       DecisionBlock,
			ViolationCount: 1,
			Violations:     []Violation{{RuleID: "r", Kind: kind, Mode: policy.ModeBlock}},
		}
		p := BuildFixPlan(report)
		if len(p.Remediations[0].Steps) == 0 {
			t.Errorf("kind %s should have at least one step", kind)
		}
	}
}

func TestRenderFixPlanText(t *testing.T) {
	report := &CheckReport{
		Decision:               DecisionBlock,
		ViolationCount:         1,
		BlockingViolationCount: 1,
		Violations: []Violation{
			{
				RuleID:            "test-rule",
				Kind:              policy.KindDenyWrite,
				Mode:              policy.ModeBlock,
				Message:           "no writes",
				Explanation:       "the explanation",
				RecommendedAction: "do this",
				MatchedPaths:      []string{"gen/x.go"},
			},
		},
	}
	p := BuildFixPlan(report)
	text := RenderFixPlanText(p)
	for _, want := range []string{"Fix plan:", "test-rule", "the explanation", "do this", "[blocking | deny_write]"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered text missing %q, got:\n%s", want, text)
		}
	}
}

func TestRenderFixPlanTextEmpty(t *testing.T) {
	report := &CheckReport{Decision: DecisionPass, ViolationCount: 0}
	text := RenderFixPlanText(BuildFixPlan(report))
	if !strings.Contains(text, "No remediation") {
		t.Errorf("empty plan text wrong, got: %s", text)
	}
}

func TestRenderCheckReportMarkdown(t *testing.T) {
	report := &CheckReport{
		Decision:       DecisionBlock,
		RepoRoot:       "/tmp/repo",
		LockfilePath:   ".reconc/policy.lock.json",
		DefaultMode:    policy.ModeWarn,
		Summary:        "1 blocking",
		Inputs:         ExecutionInputs{WritePaths: []string{"x"}},
		ViolationCount: 1,
		Violations: []Violation{
			{RuleID: "r", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock, Explanation: "e", RecommendedAction: "a"},
		},
	}
	md := RenderCheckReportMarkdown(report)
	for _, want := range []string{"# Policy Check Report", "**Decision:**", "## Violations", "### 1. `r`", "**Action:**"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q, got:\n%s", want, md)
		}
	}
}

func TestRenderCheckReportMarkdownPass(t *testing.T) {
	report := &CheckReport{Decision: DecisionPass, ViolationCount: 0, Summary: "all good"}
	md := RenderCheckReportMarkdown(report)
	if !strings.Contains(md, "_None._") {
		t.Errorf("expected '_None._' in passing markdown, got: %s", md)
	}
}

func TestDedupeStrings(t *testing.T) {
	got := dedupeStrings([]string{"a", "b", "a", "", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("expected %d entries, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
}
