package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

func TestMalformedDownstreamResultFailsClosedAfterDispatch(t *testing.T) {
	harness, call := preparedGatewayCall(t)
	if err := harness.gateway.recordProgressFailure(context.Background(), call, action.ReasonProtocolError); err != nil {
		t.Fatal(err)
	}
	result, err := harness.gateway.failMalformedDownstream(
		context.Background(), call, json.RawMessage(`{"content":"private-malformed-result"}`),
	)
	assertGatewayFailureReason(t, result, err, action.ReasonProtocolError, "private-malformed-result")
}

func TestPostResultFailurePathsWithholdWithoutRawContent(t *testing.T) {
	t.Run("normalized request", func(t *testing.T) {
		harness, call := preparedGatewayCall(t)
		raw := json.RawMessage(`{"content":[{"type":"text","text":"private-normalized-result"}]}`)
		request, err := harness.gateway.normalizedRequest(
			call.snapshot, call.contract, call.callID, call.stateVersion, action.PhasePostResult, raw,
		)
		if err != nil {
			t.Fatal(err)
		}
		result, err := harness.gateway.withholdPostFailure(
			context.Background(), call, request, action.ReasonStateUnavailable,
		)
		assertGatewayFailureReason(t, result, err, action.ReasonInspectionIncomplete, "private-normalized-result")
	})
	t.Run("unnormalized result", func(t *testing.T) {
		harness, call := preparedGatewayCall(t)
		result, err := harness.gateway.withholdUnnormalizedPostFailure(
			context.Background(), call, action.ReasonLimitExceeded,
		)
		assertGatewayFailureReason(t, result, err, action.ReasonLimitExceeded, "private-unnormalized-result")
	})
}

func TestPostSuccessDecisionPreservesTheAuthorizedDecisionBinding(t *testing.T) {
	for _, test := range []struct {
		name              string
		decision          action.Decision
		reason            action.ReasonCode
		approvalCommitted bool
		wantDecision      action.Decision
		wantReason        action.ReasonCode
	}{
		{name: "allow", decision: action.DecisionAllow, reason: action.ReasonDeclaredTool, wantDecision: action.DecisionAllow, wantReason: action.ReasonDeclaredTool},
		{name: "warn", decision: action.DecisionWarn, reason: action.ReasonRuleMatched, wantDecision: action.DecisionWarn, wantReason: action.ReasonRuleMatched},
		{name: "approved", decision: action.DecisionRequireApproval, reason: action.ReasonApprovalRequired, approvalCommitted: true, wantDecision: action.DecisionAllow, wantReason: action.ReasonRuleMatched},
		{name: "unapproved", decision: action.DecisionRequireApproval, reason: action.ReasonApprovalRequired, wantDecision: action.DecisionRequireApproval, wantReason: action.ReasonApprovalRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := action.EvaluationResult{
				Decision: test.decision,
				Reason:   test.reason,
				Failure:  &action.Failure{Code: action.ReasonInternalInvariant},
			}
			got := postSuccessDecision(&gatewayCall{
				decision: input, approvalCommitted: test.approvalCommitted,
			})
			if got.Decision != test.wantDecision || got.Reason != test.wantReason ||
				got.PhaseOutcome != action.OutcomeDeliveryEligible || got.Failure != nil {
				t.Fatalf("postSuccessDecision() = %#v", got)
			}
		})
	}
}

func preparedGatewayCall(t *testing.T) (*rawGatewayHarness, *gatewayCall) {
	t.Helper()
	markers := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markers, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markers, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newRawGatewayHarness(t, plan, evaluator)
	contract, generation, exists := harness.gateway.tool("echo")
	if !exists {
		t.Fatal("gateway tool contract is unavailable")
	}
	callID, err := actionstate.NewRandomCallID()
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"value":"failure-path"}`)
	wire := upstreamWireCall{
		id: json.RawMessage(`1`),
		params: json.RawMessage(
			`{"name":"echo","arguments":{"value":"failure-path"}}`,
		),
	}
	harness.gateway.stateMu.Lock()
	call, response := harness.gateway.prepareCall(
		context.Background(), wire, contract, generation, callID, arguments, gatewayProtocolCurrent,
	)
	harness.gateway.stateMu.Unlock()
	if response != nil || call == nil {
		body, _ := json.Marshal(response)
		t.Fatalf("prepare gateway failure-path call = %s", body)
	}
	if _, err := os.Stat(filepath.Join(markers, "invoked")); !os.IsNotExist(err) {
		t.Fatalf("prepared call reached downstream unexpectedly: %v", err)
	}
	return harness, call
}

func assertGatewayFailureReason(
	t *testing.T,
	result *mcp.CallToolResult,
	err error,
	want action.ReasonCode,
	forbidden string,
) {
	t.Helper()
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("gateway failure result = %#v, %v", result, err)
	}
	body, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if !bytes.Contains(body, []byte(`"reason_code":"`+want+`"`)) ||
		forbidden != "" && bytes.Contains(body, []byte(forbidden)) {
		t.Fatalf("gateway failure result = %s", body)
	}
}
