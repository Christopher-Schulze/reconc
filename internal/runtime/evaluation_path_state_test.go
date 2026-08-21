package runtime

import (
	"strconv"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
)

func TestEvaluationReusesOneResolvedRootForEvidencePaths(t *testing.T) {
	const evidenceCount = 32
	fixtures := make(map[string]string, evidenceCount)
	var rules strings.Builder
	rules.WriteString("rules:\n  - id: evidence\n    kind: require_evidence\n    when_paths: ['src/**']\n    evidence:\n")
	for index := range evidenceCount {
		path := "proof/evidence-" + strconv.Itoa(index) + ".txt"
		fixtures[path] = "verified\n"
		rules.WriteString("      - file: '" + path + "'\n        must_exist: true\n")
	}
	rules.WriteString("    mode: block\n    message: evidence\n")
	rules.WriteString("  - id: composite\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n")
	rules.WriteString("      - kind: require_fresh_file\n        path: 'proof/evidence-0.txt'\n")
	rules.WriteString("      - kind: require_evidence\n        file: 'proof/evidence-1.txt'\n        must_exist: true\n")
	rules.WriteString("    mode: block\n    message: composite\n")
	rules.WriteString("  - id: pre-command\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n")
	rules.WriteString("      - kind: require_evidence\n        file: 'proof/evidence-2.txt'\n        must_exist: true\n")
	rules.WriteString("      - kind: forbid_command\n        commands: ['rm -rf']\n")
	rules.WriteString("    mode: block\n    message: pre-command\n")

	repo := makeRepoWithFiles(t, rules.String(), fixtures)
	evaluator := NewEvaluator()
	plan, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	inputs := ExecutionInputs{WritePaths: []string{"src/main.go"}, Commands: []string{"echo safe"}}
	rootResolutions := 0
	report, err := evaluateRuntimePlanWithRootResolver(repo, plan, inputs, nil, false, func(path string) (string, error) {
		rootResolutions++
		return pathidentity.ResolveExisting(path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if rootResolutions != 1 {
		t.Fatalf("repository root resolutions = %d, want 1", rootResolutions)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("full evaluation decision = %s; violations=%+v", report.Decision, report.Violations)
	}

	asserted, err := evaluator.AssertRuleByID(repo, "evidence", nil, inputs)
	if err != nil || asserted.Decision != DecisionPass {
		t.Fatalf("assert evaluation = (%v, %v)", asserted, err)
	}
	filtered, err := evaluator.CheckRepoPolicyForKinds(repo, inputs, map[policy.Kind]struct{}{policy.KindRequireEvidence: {}})
	if err != nil || filtered.Decision != DecisionPass {
		t.Fatalf("kind-filtered evaluation = (%v, %v)", filtered, err)
	}
	preCommand, err := evaluator.CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil || preCommand.Decision != DecisionPass {
		t.Fatalf("pre-command evaluation = (%v, %v)", preCommand, err)
	}
}
