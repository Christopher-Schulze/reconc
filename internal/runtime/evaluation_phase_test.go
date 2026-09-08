package runtime

import (
	"context"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

const mixedWritePhasePolicy = "rules:\n  - id: mixed\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: deny_write\n        paths: ['protected/**']\n      - kind: require_claim\n        claims: ['finished']\n    mode: block\n    message: mixed boundary\n"

func TestWritePhaseRetainsCompletionEvidence(t *testing.T) {
	repo := makeRepoWithFiles(t, mixedWritePhasePolicy, nil)
	evaluator := NewEvaluator()
	inputs := ExecutionInputs{WritePaths: []string{"src/main.go"}}
	for _, test := range []struct {
		name   string
		claims []string
		want   Decision
	}{
		{name: "unfinished", want: DecisionBlock},
		{name: "finished", claims: []string{"finished"}, want: DecisionPass},
	} {
		t.Run(test.name, func(t *testing.T) {
			inputs.Claims = test.claims
			pre, err := evaluator.CheckRepoPolicyForPreWrite(repo, inputs)
			if err != nil || pre.Decision != DecisionPass {
				t.Fatalf("pre-write demanded future evidence: %+v %v", pre, err)
			}
			complete, err := evaluator.CheckRepoPolicy(repo, inputs)
			if err != nil || complete.Decision != test.want {
				t.Fatalf("completion evidence lost: %+v %v", complete, err)
			}
		})
	}
}

func TestWritePhaseRejectsMixedDisjunctionInCompiledLock(t *testing.T) {
	repo := makeRepoWithFiles(t, mixedWritePhasePolicy, nil)
	warm := NewEvaluator()
	if _, err := warm.CheckRepoPolicyForPreWrite(repo, Empty()); err != nil {
		t.Fatal(err)
	}
	rewriteLockfileWithDigest(t, repo, func(payload map[string]interface{}) {
		payload["rules"].([]interface{})[0].(map[string]interface{})["kind"] = string(policy.KindAnyOf)
	})
	for _, evaluator := range []*Evaluator{warm, NewEvaluator()} {
		if _, err := evaluator.CheckRepoPolicyForPreWrite(repo, Empty()); err == nil || !strings.Contains(err.Error(), "across enforcement phases") {
			t.Fatalf("compiled mixed disjunction admitted: %v", err)
		}
	}
}

func TestWritePhaseHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewEvaluator().CheckRepoPolicyForPreWriteContext(ctx, "unreachable", Empty()); err != context.Canceled {
		t.Fatalf("canceled pre-write returned %v", err)
	}
}
