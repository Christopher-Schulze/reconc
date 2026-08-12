package action

import (
	"strings"
	"testing"
)

func TestCompiledPlanContractsUseWildcardServerFingerprintLookup(t *testing.T) {
	plan, err := CompilePlan(Plan{
		Tools: []Tool{{
			ID: "echo-tool", Transport: TransportMCPStdio,
			ServerLabel: "echo", Tool: "run", Effect: Effect{Kind: EffectExternal},
			Origin: OriginActions, SourceIdentity: "test-policy",
		}},
		Budgets: []Budget{{
			ID: "echo-budget", Selector: Selector{ToolIDs: []string{"echo-tool"}},
			Limits: BudgetLimits{CallCount: 1}, Reset: BudgetResetNever,
			OnExhaustion: DecisionBlock, SourceIdentity: "test-policy",
		}},
		Approvals: []ApprovalDisclosure{{
			ID: "echo-disclosure", Selector: Selector{ToolIDs: []string{"echo-tool"}},
			SelectedArguments: []string{"/value"}, SourceIdentity: "test-policy",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Transport: TransportMCPStdio, ServerLabel: "echo", Tool: "run",
		ServerFingerprint: "hmac-sha256:v1:" + strings.Repeat("a", 32) + ":" + strings.Repeat("b", 64),
		Phase:             PhasePreCall,
	}
	tool, budgets, err := plan.BudgetContract(request)
	if err != nil || tool.ID != "echo-tool" || len(budgets) != 1 || budgets[0].ID != "echo-budget" {
		t.Fatalf("BudgetContract() = %#v, %#v, %v", tool, budgets, err)
	}
	disclosures, pointers, err := plan.ApprovalDisclosures(request)
	if err != nil || len(disclosures) != 1 || disclosures[0].ID != "echo-disclosure" ||
		len(pointers) != 1 || pointers[0] != "/value" {
		t.Fatalf("ApprovalDisclosures() = %#v, %#v, %v", disclosures, pointers, err)
	}
}
