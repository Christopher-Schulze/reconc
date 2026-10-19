package runtime

import (
	"fmt"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestCompositeChecksRunOncePerDistinctInput(t *testing.T) {
	calls := map[policy.Kind]int{}
	previous := evalCompositeCheck
	t.Cleanup(func() { evalCompositeCheck = previous })
	evalCompositeCheck = func(ctx *evalContext, c policy.Check, captures map[string]string, inputs ExecutionInputs, epoch uint64) (bool, string, error) {
		calls[c.Kind]++
		return previous(ctx, c, captures, inputs, epoch)
	}
	repo := makeRepoWithFiles(t, "rules:\n  - id: literal\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: deny_write\n        paths: ['generated/**']\n      - kind: require_claim\n        claims: ['reviewed']\n    mode: block\n    message: m\n  - id: captured\n    kind: all_of\n    when_paths: ['pkg/{module}/**']\n    checks:\n      - kind: deny_write\n        paths: ['generated/{module}/**']\n    mode: block\n    message: m\n", nil)
	inputs := Empty()
	for index := 0; index < 500; index++ {
		inputs.WritePaths = append(inputs.WritePaths, fmt.Sprintf("src/file%03d.go", index))
	}
	for _, module := range []string{"a", "b", "c"} {
		for index := 0; index < 50; index++ {
			inputs.WritePaths = append(inputs.WritePaths, fmt.Sprintf("pkg/%s/file%03d.go", module, index))
		}
	}
	inputs.Claims = []string{"reviewed"}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("decision = %s: %+v", report.Decision, report.Violations)
	}
	if calls[policy.KindRequireClaim] != 1 || calls[policy.KindDenyWrite] != 1+3 {
		t.Fatalf("sub-check evaluations = %v, want one claim and one deny_write per distinct capture set (4)", calls)
	}
}
