package runtime

import (
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestNewEmptyReportDefaults(t *testing.T) {
	r := NewEmptyReport("/repo", ".reconc/policy.lock.json", policy.ModeWarn, Empty())
	if r.Schema != CheckReportSchema {
		t.Errorf("schema wrong: %s", r.Schema)
	}
	if r.FormatVersion != CheckReportFormatVersion {
		t.Errorf("format_version wrong: %s", r.FormatVersion)
	}
	if !r.OK {
		t.Errorf("empty report should be OK")
	}
	if r.Decision != DecisionPass {
		t.Errorf("empty report should be pass, got %s", r.Decision)
	}
	if r.RepoRoot != "/repo" {
		t.Errorf("repo root wrong")
	}
}

func TestFinalizePassWhenNoViolations(t *testing.T) {
	r := NewEmptyReport("/r", ".reconc/policy.lock.json", policy.ModeWarn, Empty())
	r.Finalize()
	if r.Decision != DecisionPass {
		t.Errorf("expected pass, got %s", r.Decision)
	}
	if !r.OK {
		t.Error("expected ok=true")
	}
	if r.ViolationCount != 0 {
		t.Errorf("count should be 0, got %d", r.ViolationCount)
	}
}

func TestFinalizeWarnDecision(t *testing.T) {
	r := NewEmptyReport("/r", ".reconc/policy.lock.json", policy.ModeWarn, Empty())
	r.Violations = []Violation{
		{RuleID: "r1", Kind: policy.KindDenyWrite, Mode: policy.ModeWarn},
	}
	r.Finalize()
	if r.Decision != DecisionWarn {
		t.Errorf("expected warn, got %s", r.Decision)
	}
	if !r.OK {
		t.Error("warn should keep ok=true")
	}
	if r.BlockingViolationCount != 0 {
		t.Errorf("blocking count should be 0, got %d", r.BlockingViolationCount)
	}
	if r.ViolationCount != 1 {
		t.Errorf("count should be 1, got %d", r.ViolationCount)
	}
}

func TestFinalizeBlockDecision(t *testing.T) {
	r := NewEmptyReport("/r", ".reconc/policy.lock.json", policy.ModeBlock, Empty())
	r.Violations = []Violation{
		{RuleID: "r1", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock},
		{RuleID: "r2", Kind: policy.KindRequireRead, Mode: policy.ModeWarn},
	}
	r.Finalize()
	if r.Decision != DecisionBlock {
		t.Errorf("expected block, got %s", r.Decision)
	}
	if r.OK {
		t.Error("block should set ok=false")
	}
	if r.BlockingViolationCount != 1 {
		t.Errorf("expected 1 blocking, got %d", r.BlockingViolationCount)
	}
	if r.ViolationCount != 2 {
		t.Errorf("expected 2 total, got %d", r.ViolationCount)
	}
}

func TestFinalizeFixModeIsBlocking(t *testing.T) {
	r := NewEmptyReport("/r", ".reconc/policy.lock.json", policy.ModeWarn, Empty())
	r.Violations = []Violation{
		{RuleID: "r1", Kind: policy.KindDenyWrite, Mode: policy.ModeFix},
	}
	r.Finalize()
	if r.Decision != DecisionBlock {
		t.Errorf("fix mode should block, got %s", r.Decision)
	}
}

func TestFinalizeObserveDoesNotChangeDecision(t *testing.T) {
	r := NewEmptyReport("/r", ".reconc/policy.lock.json", policy.ModeWarn, Empty())
	r.Violations = []Violation{
		{RuleID: "r1", Kind: policy.KindDenyWrite, Mode: policy.ModeObserve},
	}
	r.Finalize()
	if r.Decision != DecisionPass {
		t.Errorf("observe-only should pass, got %s", r.Decision)
	}
}

func TestSummaryStrings(t *testing.T) {
	r := NewEmptyReport("/r", ".reconc/policy.lock.json", policy.ModeWarn, Empty())
	r.Finalize()
	if r.Summary != "All policy rules satisfied." {
		t.Errorf("pass summary wrong: %q", r.Summary)
	}

	r.Violations = []Violation{{RuleID: "r1", Mode: policy.ModeBlock}}
	r.Finalize()
	if r.Summary == "" {
		t.Error("block summary should be non-empty")
	}

	r.Violations = []Violation{{RuleID: "r1", Mode: policy.ModeWarn}}
	r.Finalize()
	if r.Summary == "" {
		t.Error("warn summary should be non-empty")
	}
}

func TestViolationIsBlocking(t *testing.T) {
	cases := []struct {
		mode policy.Mode
		want bool
	}{
		{policy.ModeObserve, false},
		{policy.ModeWarn, false},
		{policy.ModeBlock, true},
		{policy.ModeFix, true},
	}
	for _, c := range cases {
		v := Violation{Mode: c.mode}
		if got := v.IsBlocking(); got != c.want {
			t.Errorf("Mode %s: want IsBlocking=%v, got %v", c.mode, c.want, got)
		}
	}
}

// --- W42: progressive disclosure ------------------------------------

func TestFinalizePopulatesActionsAndRuleIDs(t *testing.T) {
	r := CheckReport{Violations: []Violation{
		{RuleID: "r1", Mode: policy.ModeBlock, Message: "m1", RecommendedAction: "do x"},
		{RuleID: "r2", Mode: policy.ModeWarn, Message: "m2", RecommendedAction: "do y"},
	}}
	r.Finalize()
	if len(r.Actions) != 2 || len(r.RuleIDs) != 2 {
		t.Fatalf("expected 2 actions / rule_ids, got %v / %v", r.Actions, r.RuleIDs)
	}
	if r.RuleIDs[0] != "r1" || r.RuleIDs[1] != "r2" {
		t.Errorf("rule_ids order wrong: %v", r.RuleIDs)
	}
	if r.Actions[0] != "do x" || r.Actions[1] != "do y" {
		t.Errorf("actions content wrong: %v", r.Actions)
	}
}

func TestFinalizeActionsFallBackToMessage(t *testing.T) {
	r := CheckReport{Violations: []Violation{
		{RuleID: "r1", Mode: policy.ModeBlock, Message: "fallback text"},
	}}
	r.Finalize()
	if len(r.Actions) != 1 || r.Actions[0] != "fallback text" {
		t.Errorf("empty recommended_action should fall back to message; got %v", r.Actions)
	}
}

func TestFinalizeIdempotentActionsReset(t *testing.T) {
	r := CheckReport{Violations: []Violation{{RuleID: "r1", Mode: policy.ModeBlock, Message: "m"}}}
	r.Finalize()
	r.Finalize() // double-call should not duplicate
	if len(r.Actions) != 1 || len(r.RuleIDs) != 1 {
		t.Errorf("Finalize should be idempotent; got %v / %v", r.Actions, r.RuleIDs)
	}
}

func TestTerseUsesProgressiveFields(t *testing.T) {
	r := CheckReport{Violations: []Violation{
		{RuleID: "r1", Mode: policy.ModeBlock, RecommendedAction: "fix x"},
	}}
	r.Finalize()
	t2 := r.Terse()
	if len(t2.RuleIDs) != 1 || t2.RuleIDs[0] != "r1" {
		t.Errorf("Terse().RuleIDs should mirror Finalize output; got %v", t2.RuleIDs)
	}
	if len(t2.Actions) != 1 || t2.Actions[0] != "fix x" {
		t.Errorf("Terse().Actions should mirror Finalize output; got %v", t2.Actions)
	}
}

func TestFinalizePassReportHasEmptySlices(t *testing.T) {
	r := CheckReport{Violations: []Violation{}}
	r.Finalize()
	if r.Actions == nil || r.RuleIDs == nil {
		t.Error("Actions and RuleIDs must be non-nil even when empty, for stable JSON output")
	}
	if len(r.Actions) != 0 || len(r.RuleIDs) != 0 {
		t.Errorf("expected empty slices for passing report, got %v / %v", r.Actions, r.RuleIDs)
	}
}
