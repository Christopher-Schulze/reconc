package compiler

import (
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestDetectConflictsEmptyRules(t *testing.T) {
	c := DetectConflicts(nil)
	if len(c) != 0 {
		t.Errorf("expected no conflicts for empty ruleset, got %v", c)
	}
}

func TestDetectConflictsDuplicateDeny(t *testing.T) {
	rules := []policy.Rule{
		{ID: "r1", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock, Paths: []string{"src/**"}},
		{ID: "r2", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock, Paths: []string{"src/**"}},
	}
	c := DetectConflicts(rules)
	if len(c) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %+v", len(c), c)
	}
	if c[0].Kind != ConflictDuplicateDeny {
		t.Errorf("expected ConflictDuplicateDeny, got %s", c[0].Kind)
	}
	if c[0].RuleIDA != "r1" || c[0].RuleIDB != "r2" {
		t.Errorf("expected pair (r1,r2), got (%s,%s)", c[0].RuleIDA, c[0].RuleIDB)
	}
}

func TestDetectConflictsDuplicateDenyOrderIndependent(t *testing.T) {
	// Path lists in different order still match.
	rules := []policy.Rule{
		{ID: "r1", Kind: policy.KindDenyWrite, Paths: []string{"a", "b"}},
		{ID: "r2", Kind: policy.KindDenyWrite, Paths: []string{"b", "a"}},
	}
	c := DetectConflicts(rules)
	if len(c) != 1 {
		t.Errorf("expected 1 conflict (order-insensitive match), got %d", len(c))
	}
}

func TestDetectConflictsNotFlaggedForDifferentPaths(t *testing.T) {
	rules := []policy.Rule{
		{ID: "r1", Kind: policy.KindDenyWrite, Paths: []string{"src/**"}},
		{ID: "r2", Kind: policy.KindDenyWrite, Paths: []string{"tests/**"}},
	}
	c := DetectConflicts(rules)
	if len(c) != 0 {
		t.Errorf("expected no conflict for disjoint paths, got %v", c)
	}
}

func TestDetectConflictsDenyVsRequireRead(t *testing.T) {
	rules := []policy.Rule{
		{ID: "deny-docs", Kind: policy.KindDenyWrite, Paths: []string{"docs/**"}},
		{ID: "read-on-docs", Kind: policy.KindRequireRead,
			WhenPaths: []string{"docs/**"}, Paths: []string{"README.md"}},
	}
	c := DetectConflicts(rules)
	if len(c) != 1 {
		t.Fatalf("expected 1 deny-vs-require_read conflict, got %d: %+v", len(c), c)
	}
	if c[0].Kind != ConflictDenyVsRequireRead {
		t.Errorf("expected ConflictDenyVsRequireRead, got %s", c[0].Kind)
	}
}

func TestDetectConflictsForbidVsRequireCommand(t *testing.T) {
	rules := []policy.Rule{
		{ID: "no-rm", Kind: policy.KindForbidCommand, Commands: []string{"rm -rf /"}},
		{ID: "require-rm", Kind: policy.KindRequireCommand,
			WhenPaths: []string{"**"}, Commands: []string{"rm -rf /"}},
	}
	c := DetectConflicts(rules)
	if len(c) != 1 {
		t.Fatalf("expected 1 forbid-vs-require_command conflict, got %d", len(c))
	}
	if c[0].Kind != ConflictForbidVsRequireCommand {
		t.Errorf("expected ConflictForbidVsRequireCommand, got %s", c[0].Kind)
	}
}

func TestDetectConflictsDuplicateRequireClaim(t *testing.T) {
	rules := []policy.Rule{
		{ID: "ci-a", Kind: policy.KindRequireClaim,
			WhenPaths: []string{"**"}, Claims: []string{"ci-green"}},
		{ID: "ci-b", Kind: policy.KindRequireClaim,
			WhenPaths: []string{"**"}, Claims: []string{"ci-green"}},
	}
	c := DetectConflicts(rules)
	found := false
	for _, cf := range c {
		if cf.Kind == ConflictDuplicateRequireClaim {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ConflictDuplicateRequireClaim, got %+v", c)
	}
}

func TestDetectConflictsDeterministicOrdering(t *testing.T) {
	rules := []policy.Rule{
		{ID: "z-rule", Kind: policy.KindDenyWrite, Paths: []string{"x"}},
		{ID: "a-rule", Kind: policy.KindDenyWrite, Paths: []string{"x"}},
		{ID: "m-rule", Kind: policy.KindDenyWrite, Paths: []string{"x"}},
	}
	first := DetectConflicts(rules)
	second := DetectConflicts(rules)
	if len(first) != len(second) {
		t.Fatalf("non-deterministic conflict count")
	}
	for i := range first {
		if first[i].Kind != second[i].Kind ||
			first[i].RuleIDA != second[i].RuleIDA ||
			first[i].RuleIDB != second[i].RuleIDB {
			t.Errorf("ordering drift at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
	// Must be sorted by (IDA, IDB).
	for i := 1; i < len(first); i++ {
		if first[i-1].RuleIDA > first[i].RuleIDA {
			t.Errorf("RuleIDA not sorted: %+v", first)
		}
	}
}

func TestDetectConflictsSingleRuleHasNoConflict(t *testing.T) {
	rules := []policy.Rule{
		{ID: "only", Kind: policy.KindDenyWrite, Paths: []string{"src/**"}},
	}
	if c := DetectConflicts(rules); len(c) != 0 {
		t.Errorf("single rule should never conflict with itself, got %v", c)
	}
}
