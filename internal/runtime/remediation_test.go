package runtime

import (
	"encoding/json"
	"fmt"
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

func TestBuildFixPlanHonorsSchemaOverride(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://schemas.example.test")
	p := BuildFixPlan(&CheckReport{Decision: DecisionPass, Inputs: Empty()})
	if got, want := p.Schema, "https://schemas.example.test/schemas/policy-fix-plan/v2"; got != want {
		t.Fatalf("schema = %q, want %q", got, want)
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

func TestBuildFixPlanBoundsCurrentOutputAndReportsOmissions(t *testing.T) {
	commands := make([]string, MaxFixPlanActions+5)
	for index := range commands {
		commands[index] = "  command " + string(rune('α'+index)) + " ; $HOME\t"
	}
	readPaths := make([]string, MaxFixPlanInputItems+3)
	for index := range readPaths {
		readPaths[index] = "read/" + string(rune('界'+index))
	}
	writeEpochs := make(map[string]uint64, MaxFixPlanInputItems+3)
	for index := 0; index < MaxFixPlanInputItems+3; index++ {
		writeEpochs[fmt.Sprintf("epoch-%03d", index)] = uint64(index + 1)
	}
	violations := make([]Violation, MaxFixPlanRemediations+4)
	for index := range violations {
		violations[index] = Violation{RuleID: fmt.Sprintf("rule-%03d", index), Kind: policy.KindDenyWrite, Mode: policy.ModeBlock}
	}
	violations[0].Kind = policy.KindRequireCommand
	violations[0].RequiredCommands = commands
	report := &CheckReport{
		Decision:               DecisionBlock,
		RepoRoot:               "/repo exact\twith spaces",
		ViolationCount:         len(violations),
		BlockingViolationCount: len(violations),
		Inputs: ExecutionInputs{
			ReadPaths:   readPaths,
			WriteEpochs: writeEpochs,
		},
		Violations: violations,
	}

	plan := BuildFixPlan(report)
	if got, want := len(plan.Remediations), MaxFixPlanRemediations; got != want {
		t.Fatalf("retained remediations = %d, want %d", got, want)
	}
	if plan.RemediationCount != len(plan.Remediations) {
		t.Fatalf("remediation_count = %d, emitted = %d", plan.RemediationCount, len(plan.Remediations))
	}
	if plan.Omissions == nil || plan.Omissions.Remediations != len(violations)-MaxFixPlanRemediations {
		t.Fatalf("remediation omissions = %#v", plan.Omissions)
	}
	if got := plan.Inputs.ReadPaths[MaxFixPlanInputItems-1]; got != readPaths[MaxFixPlanInputItems-1] {
		t.Fatalf("retained path changed: %q", got)
	}
	if len(plan.Inputs.ReadPaths) != MaxFixPlanInputItems || plan.Omissions.InputItems == 0 {
		t.Fatalf("input bounds = len(%d), omissions=%#v", len(plan.Inputs.ReadPaths), plan.Omissions)
	}
	if len(plan.Inputs.WriteEpochs) != MaxFixPlanInputItems {
		t.Fatalf("write epoch bound = %d", len(plan.Inputs.WriteEpochs))
	}
	if _, ok := plan.Inputs.WriteEpochs["epoch-000"]; !ok {
		t.Fatal("deterministic epoch selection dropped the first key")
	}
	if _, ok := plan.Inputs.WriteEpochs["epoch-299"]; ok {
		t.Fatal("deterministic epoch selection retained an omitted key")
	}
	first := plan.Remediations[0]
	if len(first.SuggestedCommands) != MaxFixPlanFieldItems || first.SuggestedCommands[0] != commands[0] {
		t.Fatalf("suggested command bounds changed exact values: len=%d first=%q", len(first.SuggestedCommands), first.SuggestedCommands[0])
	}
	if len(first.Actions) != MaxFixPlanActions || first.Actions[0].Shell != commands[0] {
		t.Fatalf("action bounds changed exact values: len=%d first=%q", len(first.Actions), first.Actions[0].Shell)
	}
	if plan.Omissions.RemediationItems == 0 || plan.Omissions.ActionItems == 0 {
		t.Fatalf("nested omissions missing: %#v", plan.Omissions)
	}
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"omissions"`) {
		t.Fatalf("v2 JSON omitted metadata: %s", body)
	}
	text := RenderFixPlanText(plan)
	if !strings.Contains(text, "Omitted entries:") || !strings.Contains(text, commands[0]) {
		t.Fatalf("text omitted bounded metadata or exact action: %s", text[:minInt(len(text), 512)])
	}
}

func TestBuildLegacyFixPlanRetainsV1ShapeWhenCurrentPlanIsBounded(t *testing.T) {
	violations := make([]Violation, MaxFixPlanRemediations+1)
	for index := range violations {
		violations[index] = Violation{RuleID: fmt.Sprintf("legacy-%d", index), Kind: policy.KindRequireClaim, RequiredClaims: []string{"claim"}}
	}
	legacy := BuildLegacyFixPlan(&CheckReport{Violations: violations, ViolationCount: len(violations)})
	if len(legacy.Remediations) != len(violations) || legacy.Omissions != nil {
		t.Fatalf("legacy plan was bounded or gained metadata: len=%d omissions=%#v", len(legacy.Remediations), legacy.Omissions)
	}
	body, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"omissions"`) || strings.Contains(string(body), `"actions"`) {
		t.Fatalf("legacy shape changed: %s", body)
	}
}

func TestRenderCheckReportMarkdown(t *testing.T) {
	report := &CheckReport{
		Decision:       DecisionBlock,
		RepoRoot:       "/tmp/repo",
		LockfilePath:   ".reconc/policy.lock.json",
		DefaultMode:    policy.ModeWarn,
		Summary:        "summary-sentinel-95",
		Inputs:         ExecutionInputs{WritePaths: []string{"input-sentinel-94"}},
		ViolationCount: 1,
		Violations: []Violation{
			{RuleID: "rule-sentinel-91", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock, Explanation: "explanation-sentinel-92", RecommendedAction: "action-sentinel-93"},
		},
	}
	md := RenderCheckReportMarkdown(report)
	for _, want := range []string{"/tmp/repo", "summary-sentinel-95", "input-sentinel-94", "rule-sentinel-91", "explanation-sentinel-92", "action-sentinel-93"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lost dynamic report value %q, got:\n%s", want, md)
		}
	}
}

func TestRenderCheckReportMarkdownPass(t *testing.T) {
	report := &CheckReport{Decision: DecisionPass, ViolationCount: 0, Summary: "all good"}
	md := RenderCheckReportMarkdown(report)
	if !strings.Contains(md, report.Summary) {
		t.Errorf("passing markdown lost the report summary: %s", md)
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
