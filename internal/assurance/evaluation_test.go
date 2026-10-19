package assurance

import (
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/stackdetect"
)

func TestEvaluationSharesDiscoveryAndModuleFactsAcrossRules(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "services/api/go.mod", "module example/api\n")
	writeAssuranceFile(t, root, "services/api/main.go", "package api\nfunc answer() { }\n")
	previous := detectModuleRoots
	t.Cleanup(func() { detectModuleRoots = previous })
	detections := 0
	detectModuleRoots = func(root string) (stackdetect.Result, error) {
		detections++
		return previous(root)
	}
	evaluation, err := NewEvaluation(root, Inputs{ChangedPaths: []string{"services/api/main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first-rule", "second-rule"} {
		gate := policy.AssuranceGate{ID: id, Type: policy.AssuranceGoFormat,
			ApplicableIf: []string{"go.mod"}, ScanPaths: []string{"**/*.go"}}
		findings, err := evaluation.Evaluate([]policy.AssuranceGate{gate})
		if err != nil || len(findings) != 1 || findings[0].GateID != id {
			t.Fatalf("per-rule findings changed: %+v, err=%v", findings, err)
		}
	}
	stats := evaluation.state.analysisStats()
	if detections != 1 || len(evaluation.state.scoped) != 1 || stats.BodyReads != 1 || stats.GoFormats != 1 {
		t.Fatalf("repeated rule work: detection=%d scopes=%d stats=%+v", detections, len(evaluation.state.scoped), stats)
	}
}
