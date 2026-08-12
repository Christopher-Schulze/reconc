package mcpgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func TestGatewayPolicyMutationsCannotBypassEnforcementBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		plan          func(*testing.T) (*action.CompiledPlan, *action.Evaluator)
		approvalPhase action.Phase
		wantInvoked   bool
		wantSensitive bool
		wantError     bool
	}{
		{name: "direct allow", plan: gatewayAllowMutationPlan, wantInvoked: true, wantSensitive: true},
		{name: "pre block", plan: gatewayPreBlockMutationPlan, wantError: true},
		{name: "post block", plan: testGatewayPostBlockPlan, wantInvoked: true, wantError: true},
		{name: "pre approval refused", plan: gatewayPreApprovalMutationPlan, approvalPhase: action.PhasePreCall, wantError: true},
		{name: "post approval refused", plan: testGatewayPostApprovalPlan, approvalPhase: action.PhasePostResult, wantInvoked: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "invoked")
			t.Setenv(fakeProcessEnvironment, "1")
			t.Setenv(fakeMarkerEnvironment, marker)
			t.Setenv(fakeModeEnvironment, "sensitive-result")
			plan, evaluator := test.plan(t)
			registry, policyID, options := mutationApprovalContext(t, test.approvalPhase)
			result := runGatewayScenario(t, plan, evaluator, registry, policyID, options, callMutationEcho(t))
			assertMutationOutcome(t, result, marker, test.wantInvoked, test.wantSensitive, test.wantError)
		})
	}
}

func gatewayAllowMutationPlan(t *testing.T) (*action.CompiledPlan, *action.Evaluator) {
	return testGatewayPlan(t, action.DecisionAllow)
}

func gatewayPreBlockMutationPlan(t *testing.T) (*action.CompiledPlan, *action.Evaluator) {
	return testGatewayPlan(t, action.DecisionBlock)
}

func gatewayPreApprovalMutationPlan(t *testing.T) (*action.CompiledPlan, *action.Evaluator) {
	return testGatewayApprovalPlan(t, action.PhasePreCall)
}

func mutationApprovalContext(
	t *testing.T,
	phase action.Phase,
) (string, string, *mcp.ClientOptions) {
	t.Helper()
	if phase == "" {
		return "", "", nil
	}
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x6d}, ed25519.SeedSize))
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	options := approvalClientOptionsForDecision(privateKey, actionapproval.DecisionReject)
	return registry, "post-result-policy", options
}

func callMutationEcho(
	t *testing.T,
) func(context.Context, *mcp.ClientSession) *mcp.CallToolResult {
	t.Helper()
	return func(ctx context.Context, session *mcp.ClientSession) *mcp.CallToolResult {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "echo", Arguments: json.RawMessage(`{"value":"mutation"}`),
		})
		if err != nil {
			t.Fatalf("CallTool(): %v", err)
		}
		return result
	}
}

func assertMutationOutcome(
	t *testing.T,
	result *mcp.CallToolResult,
	marker string,
	wantInvoked bool,
	wantSensitive bool,
	wantError bool,
) {
	t.Helper()
	if result == nil || result.IsError != wantError {
		t.Fatalf("gateway result = %#v, want IsError=%t", result, wantError)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Contains(body, []byte("private-result-value")); got != wantSensitive {
		t.Fatalf("sensitive result delivered = %t, want %t; result=%s", got, wantSensitive, body)
	}
	_, err = os.Stat(marker)
	invoked := err == nil
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if invoked != wantInvoked {
		t.Fatalf("downstream invoked = %t, want %t", invoked, wantInvoked)
	}
}
