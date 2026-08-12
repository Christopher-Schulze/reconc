package actionstate

import (
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func TestApprovalStoreIssuesWithApprovalOnlyBudget(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval", action.BudgetLimits{ApprovalCount: 1}, action.BudgetResetNever,
	)})
	input, reserved := fixture.reserve(t, callID("a"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	if issued.issue.Evidence.Status != actionapproval.StatusPending {
		t.Fatalf("approval status = %q, want pending", issued.issue.Evidence.Status)
	}
}

func TestApprovalStoreIssuesWhenApprovalRuleDisablesCaching(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"approval", action.BudgetLimits{ApprovalCount: 1}, action.BudgetResetNever,
	)})
	fixture.plan = compileApprovalCachePlan(t, fixture.serverFingerprint, fixture.plan.Plan().Budgets)
	input, reserved := fixture.reserve(t, callID("b"))
	issued := issueFixtureApproval(t, fixture, input, reserved)
	if issued.issue.Evidence.Status != actionapproval.StatusPending {
		t.Fatalf("approval status = %q, want pending", issued.issue.Evidence.Status)
	}
}

func compileApprovalCachePlan(
	t testing.TB,
	fingerprint string,
	budgets []action.Budget,
) *action.CompiledPlan {
	t.Helper()
	compiled, err := action.CompilePlan(action.Plan{
		Tools: []action.Tool{{
			ID: "database-write", Transport: action.TransportMCPStdio,
			ServerLabel: "server", ServerFingerprint: fingerprint, Tool: "execute",
			Effect: action.Effect{Kind: action.EffectExternal},
			Origin: action.OriginActions, SourceIdentity: ".reconc.yml",
		}},
		Rules: []action.Rule{{
			ID: "approve-database-write", Selector: action.Selector{ToolIDs: []string{"database-write"}},
			Decision: action.DecisionRequireApproval, OnIndeterminate: action.DecisionBlock,
			Cache: action.CacheNever, SourceIdentity: ".reconc.yml",
		}},
		Budgets: budgets,
		Approvals: []action.ApprovalDisclosure{{
			ID: "database-write-summary", Selector: action.Selector{ToolIDs: []string{"database-write"}},
			SelectedArguments: []string{"/target"}, SourceIdentity: ".reconc.yml",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}
