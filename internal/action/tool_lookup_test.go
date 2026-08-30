package action

import (
	"errors"
	"reflect"
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

func TestBudgetContractMatchesRuntimeDigestConstraintsExactly(t *testing.T) {
	t.Parallel()
	matchingDigest := "sha256:" + strings.Repeat("1", 64)
	otherDigest := "sha256:" + strings.Repeat("2", 64)
	tool := Tool{
		ID: "echo-tool", Transport: TransportMCPStdio,
		ServerLabel: "echo", Tool: "run", Effect: Effect{Kind: EffectExternal},
		Origin: OriginActions, SourceIdentity: "test-policy",
	}
	budgets := []Budget{
		{
			ID: "digest-only", Selector: Selector{ToolContractDigests: []string{matchingDigest}},
			Limits: BudgetLimits{CallCount: 1}, Reset: BudgetResetNever,
			OnExhaustion: DecisionBlock, SourceIdentity: "test-policy",
		},
		{
			ID: "mixed", Selector: Selector{
				ToolIDs: []string{"echo-tool"}, ToolContractDigests: []string{matchingDigest},
			},
			Limits: BudgetLimits{CallCount: 1}, Reset: BudgetResetNever,
			OnExhaustion: DecisionBlock, SourceIdentity: "test-policy",
		},
	}
	plan, err := CompilePlan(Plan{Tools: []Tool{tool}, Budgets: budgets})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Transport: TransportMCPStdio, ServerLabel: "echo", Tool: "run",
		ToolContractDigest: matchingDigest, Phase: PhasePreCall,
	}
	_, matched, err := plan.BudgetContract(request)
	if err != nil || len(matched) != 2 || matched[0].ID != "digest-only" || matched[1].ID != "mixed" {
		t.Fatalf("matching digest budgets = %#v, %v", matched, err)
	}
	request.ToolContractDigest = otherDigest
	_, unmatched, err := plan.BudgetContract(request)
	if err != nil || len(unmatched) != 0 {
		t.Fatalf("other digest budgets = %#v, %v", unmatched, err)
	}

	budgets[1].Selector.Tools = []string{"different"}
	if _, err := CompilePlan(Plan{Tools: []Tool{tool}, Budgets: budgets[1:]}); err == nil || !strings.Contains(err.Error(), "cannot match any declared tool") {
		t.Fatalf("mixed declaration mismatch error = %v", err)
	}
}

func TestBudgetContractDistinguishesUnknownAndUnbudgetedTools(t *testing.T) {
	t.Parallel()
	plan, err := CompilePlan(Plan{Tools: []Tool{{
		ID: "echo-tool", Transport: TransportMCPStdio,
		ServerLabel: "echo", Tool: "run", Effect: Effect{Kind: EffectExternal},
		Origin: OriginActions, SourceIdentity: "test-policy",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Transport: TransportMCPStdio, ServerLabel: "echo", Tool: "run", Phase: PhasePreCall,
	}
	tool, budgets, err := plan.BudgetContract(request)
	if err != nil || tool.ID != "echo-tool" || len(budgets) != 0 {
		t.Fatalf("unbudgeted declared tool = %#v, %#v, %v", tool, budgets, err)
	}

	request.Tool = "missing"
	tool, budgets, err = plan.BudgetContract(request)
	var requestErr *RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != ReasonToolUnclassified ||
		!reflect.DeepEqual(tool, Tool{}) || budgets != nil {
		t.Fatalf("unknown tool contract = %#v, %#v, %v", tool, budgets, err)
	}
}
