package runtime

import (
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestCompiledPolicyEvaluatorMatchesFreshRepositoryEvaluator(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: deny-src\n    kind: deny_write\n    paths: [src/**]\n    mode: block\n    message: blocked\n")
	_, body, err := compiler.RenderRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := NewCompiledPolicyEvaluator(body)
	if err != nil {
		t.Fatal(err)
	}
	inputs := ExecutionInputs{
		ReadPaths: []string{}, WritePaths: []string{"src/main.go"}, WriteEpochs: map[string]uint64{},
		Commands: []string{}, Claims: []string{}, CommandResults: []CommandResult{},
	}
	currentReport, currentMetrics, err := NewEvaluator().CheckRepoPolicyWithMetrics(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	candidateReport, candidateMetrics, err := candidate.Check(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(currentReport, candidateReport) || currentMetrics != candidateMetrics {
		t.Fatalf("compiled evaluator drift:\ncurrent=%+v %+v\ncandidate=%+v %+v",
			currentReport, currentMetrics, candidateReport, candidateMetrics)
	}
	if !reflect.DeepEqual(candidate.RuleIDs(), []string{"deny-src"}) {
		t.Fatalf("candidate rule ids = %v", candidate.RuleIDs())
	}
}

func TestNormalizeReplayInputsUsesProductionPathAndCommandSemantics(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	inputs := Empty()
	inputs.WritePaths = []string{repo + "/src/main.go"}
	inputs.Commands = []string{"go   test ./..."}
	normalized, err := NormalizeReplayInputs(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized.WritePaths, []string{"src/main.go"}) ||
		!reflect.DeepEqual(normalized.Commands, []string{"go test ./..."}) {
		t.Fatalf("normalized replay = %+v", normalized)
	}
}

func TestCompiledPolicyEvaluatorTracesSatisfiedRuleTriggers(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: read-first\n    kind: require_read\n    paths: [src/**]\n    before_paths: [README.md]\n    mode: block\n    message: read first\n")
	_, body, err := compiler.RenderRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewCompiledPolicyEvaluator(body)
	if err != nil {
		t.Fatal(err)
	}
	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.ReadPaths = []string{"README.md"}
	report, _, trace, err := evaluator.CheckWithTrace(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass || !reflect.DeepEqual(trace.MatchedRuleIDs, []string{"read-first"}) {
		t.Fatalf("satisfied trace = %+v, report=%+v", trace, report)
	}
}
