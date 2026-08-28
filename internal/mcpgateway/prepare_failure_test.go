package mcpgateway

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

type evidenceProviderFunc func(
	context.Context,
	PolicySnapshot,
	action.Request,
	action.Tool,
) (EvidenceSnapshot, error)

func (f evidenceProviderFunc) Observe(
	ctx context.Context,
	snapshot PolicySnapshot,
	request action.Request,
	tool action.Tool,
) (EvidenceSnapshot, error) {
	return f(ctx, snapshot, request, tool)
}

func TestPrepareCallFailsClosedAndReleasesSetupReservations(t *testing.T) {
	tests := []struct {
		name              string
		wantReason        action.ReasonCode
		wantTerminalCalls int
		breakSetup        func(*Gateway) func()
	}{
		{
			name:              "reservation error is not overwritten",
			wantReason:        action.ReasonIdentityUnavailable,
			wantTerminalCalls: 0,
			breakSetup: func(gateway *Gateway) func() {
				original := gateway.boundContext
				gateway.config.EvidenceProvider = evidenceProviderFunc(func(
					context.Context,
					PolicySnapshot,
					action.Request,
					action.Tool,
				) (EvidenceSnapshot, error) {
					gateway.boundContext.ContextIdentity = ""
					return EvidenceSnapshot{
						Taint: action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
					}, nil
				})
				return func() {
					gateway.boundContext = original
					gateway.config.EvidenceProvider = nil
				}
			},
		},
		{
			name:              "ledger construction failure releases reservation",
			wantReason:        action.ReasonLedgerUnavailable,
			wantTerminalCalls: 1,
			breakSetup: func(gateway *Gateway) func() {
				original := gateway.storage
				gateway.config.EvidenceProvider = evidenceProviderFunc(func(
					context.Context,
					PolicySnapshot,
					action.Request,
					action.Tool,
				) (EvidenceSnapshot, error) {
					gateway.storage = actionstate.PrivateProjectStorage{}
					return EvidenceSnapshot{
						Taint: action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
					}, nil
				})
				return func() {
					gateway.storage = original
					gateway.config.EvidenceProvider = nil
				}
			},
		},
		{
			name:              "request accepted failure releases reservation",
			wantReason:        action.ReasonLedgerUnavailable,
			wantTerminalCalls: 1,
			breakSetup: func(gateway *Gateway) func() {
				original := gateway.ledger
				gateway.ledger = nil
				return func() { gateway.ledger = original }
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			markers := t.TempDir()
			t.Setenv(fakeProcessEnvironment, "1")
			t.Setenv(fakeModeEnvironment, "normal")
			t.Setenv(fakeMarkerEnvironment, filepath.Join(markers, "invoked"))
			t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markers, "cancelled"))
			plan, evaluator := testGatewayRequiredLedgerBudgetPlan(t)
			harness := newRawGatewayHarness(t, plan, evaluator)
			contract, generation, exists := harness.gateway.tool("echo")
			if !exists {
				t.Fatal("gateway echo contract is unavailable")
			}
			callID, err := actionstate.NewRandomCallID()
			if err != nil {
				t.Fatal(err)
			}
			restore := test.breakSetup(harness.gateway)
			t.Cleanup(restore)
			call, response := harness.gateway.prepareCall(
				context.Background(),
				upstreamWireCall{
					id: json.RawMessage(`1`),
					params: json.RawMessage(
						`{"name":"echo","arguments":{"value":"setup-failure"}}`,
					),
				},
				contract,
				generation,
				callID,
				json.RawMessage(`{"value":"setup-failure"}`),
				gatewayProtocolCurrent,
			)
			restore()
			if call != nil {
				t.Fatalf("failed setup returned a dispatchable call: %#v", call)
			}
			assertGatewayFailureReason(t, response, nil, test.wantReason, "setup-failure")
			status, err := harness.gateway.state.Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.LiveReservations != 0 || status.TerminalCallCount != test.wantTerminalCalls {
				t.Fatalf("failed setup state = %#v", status)
			}
		})
	}
}

func testGatewayRequiredLedgerBudgetPlan(t *testing.T) (*action.CompiledPlan, *action.Evaluator) {
	t.Helper()
	compiled, _ := testGatewayBudgetPlan(t)
	plan := compiled.Plan()
	plan.Ledger.Mode = action.LedgerRequired
	compiled, err := action.CompilePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(compiled)
	if err != nil {
		t.Fatal(err)
	}
	return compiled, evaluator
}
