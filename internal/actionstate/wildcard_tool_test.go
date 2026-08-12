package actionstate

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestBudgetStoreAndEvaluatorShareWildcardToolResolution(t *testing.T) {
	budget := storeBudget(
		"wildcard-tool",
		action.BudgetLimits{CallCount: 1, ApprovalCount: 1},
		action.BudgetResetNever,
	)
	fixture := newStoreFixture(t, []action.Budget{budget})
	fixture.plan = compileStorePlan(t, "", []action.Budget{budget})
	input, reserved := fixture.reserve(t, callID("w"))
	evaluator, err := action.NewEvaluator(fixture.plan)
	if err != nil {
		t.Fatal(err)
	}
	request := input.Request
	request.StateVersion = reserved.Snapshot.StateVersion
	evaluation := action.EvaluationInput{
		Request: request, SourceIdentity: strings.Repeat("8", 64),
		ContextIdentity:  fixture.context.ContextIdentity,
		ExecutableDigest: fixture.server.ExecutableDigest,
		Principal:        fixture.context.Principal,
		CredentialLabels: credentialLabels(fixture.context.Credentials),
		Budget:           reserved.Snapshot,
		Approval:         action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
		Taint:            action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
		Lifecycle:        action.LifecycleActive, CachePolicyVersion: action.CacheIdentityVersion,
	}
	evaluation.ResampledIdentities = evaluator.IdentitySnapshot(evaluation)
	decision := evaluator.Evaluate(evaluation)
	if decision.Failure != nil || decision.Reason == action.ReasonStateCorrupt {
		t.Fatalf("wildcard budget evaluation = %#v", decision)
	}
}
