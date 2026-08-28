package mcpgateway

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
)

func TestMarkIndeterminateAfterFailureContract(t *testing.T) {
	tests := []struct {
		name       string
		breakState bool
	}{
		{name: "stale version retry commits", breakState: false},
		{name: "state failure preserves both errors", breakState: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness, call := preparedBudgetGatewayCall(t)
			cause := errors.New("required ledger write failed")
			var diagnostics bytes.Buffer
			harness.gateway.config.Diagnostics = &diagnostics
			originalState := harness.gateway.state
			if test.breakState {
				harness.gateway.state = nil
			} else {
				call.stateVersion = "stale-state-version"
			}

			version, err := harness.gateway.markIndeterminateAfterFailure(
				context.Background(), call, cause,
			)
			harness.gateway.state = originalState
			if test.breakState {
				if version != "" || !errors.Is(err, cause) {
					t.Fatalf("failed transition = version %q, error %v", version, err)
				}
				if !strings.Contains(err.Error(), call.reservation.Identity) ||
					!strings.Contains(err.Error(), call.stateVersion) {
					t.Fatalf("unresolved state identity missing from error: %v", err)
				}
				written := diagnostics.String()
				if !strings.Contains(written, "indeterminate reservation transition failed") ||
					len(written) > MaxDiagnosticBytes+1 {
					t.Fatalf("unresolved transition diagnostic = %q", written)
				}
				return
			}
			if err != nil || version == "" || call.stateVersion != version {
				t.Fatalf("retried transition = version %q, call version %q, error %v", version, call.stateVersion, err)
			}
			assertIndeterminateReservation(t, originalState)
		})
	}
}

func TestCommitDispatchPreservesBudgetLedgerFailureState(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*gatewayCall)
	}{
		{name: "dispatch ledger", mutate: func(*gatewayCall) {}},
		{name: "approved pre-call dispatch ledger", mutate: func(call *gatewayCall) {
			call.approvalReserved = true
			call.approvalCommitted = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness, call := preparedRequiredLedgerBudgetGatewayCall(t)
			test.mutate(call)
			originalLedger := call.ledger.store
			call.ledger.store = nil
			t.Cleanup(func() { call.ledger.store = originalLedger })

			if err := harness.gateway.commitDispatch(context.Background(), call); err == nil {
				t.Fatal("dispatch ledger failure was discarded")
			}
			assertIndeterminateReservation(t, harness.gateway.state)
		})
	}
}

func TestApprovalLedgerFailuresMarkReservationsIndeterminate(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*gatewayCall)
	}{
		{name: "post-result approval reservation", mutate: func(call *gatewayCall) { call.postApprovalReserved = true }},
		{name: "post-result approval consumption", mutate: func(call *gatewayCall) { call.postApprovalCommitted = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness, call := preparedBudgetGatewayCall(t)
			test.mutate(call)
			result, err := harness.gateway.blockApprovalLedgerFailure(
				context.Background(), call, errors.New("approval ledger write failed"),
			)
			assertGatewayFailureReason(
				t, result, err, action.ReasonLedgerUnavailable, "approval ledger write failed",
			)
			assertIndeterminateReservation(t, harness.gateway.state)
		})
	}
}

func TestUnresolvedTransitionCannotCreateIndeterminateLedgerRecord(t *testing.T) {
	for _, test := range []struct {
		name string
		fail func(*Gateway, *gatewayCall) (*mcp.CallToolResult, error)
	}{
		{
			name: "unknown downstream",
			fail: func(gateway *Gateway, call *gatewayCall) (*mcp.CallToolResult, error) {
				return gateway.failUnknownDownstream(
					context.Background(), call, errors.New("private downstream failure"),
				)
			},
		},
		{
			name: "malformed downstream",
			fail: func(gateway *Gateway, call *gatewayCall) (*mcp.CallToolResult, error) {
				return gateway.failMalformedDownstream(
					context.Background(), call, []byte(`{"private":"malformed"}`),
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness, call := preparedRequiredLedgerBudgetGatewayCall(t)
			before, _, err := call.ledger.store.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			originalState := harness.gateway.state
			harness.gateway.state = nil
			result, resultErr := test.fail(harness.gateway, call)
			harness.gateway.state = originalState
			assertGatewayFailureReason(
				t, result, resultErr, action.ReasonReservationIndeterminate, "private",
			)
			after, _, err := call.ledger.store.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if countBudgetTransitions(before, actionledger.BudgetIndeterminate) !=
				countBudgetTransitions(after, actionledger.BudgetIndeterminate) {
				t.Fatal("unresolved state transition produced a false indeterminate ledger version")
			}
		})
	}
}

func preparedBudgetGatewayCall(t *testing.T) (*rawGatewayHarness, *gatewayCall) {
	t.Helper()
	plan, evaluator := testGatewayBudgetPlan(t)
	return preparedGatewayCallWithPlan(t, plan, evaluator)
}

func preparedRequiredLedgerBudgetGatewayCall(t *testing.T) (*rawGatewayHarness, *gatewayCall) {
	t.Helper()
	plan, evaluator := testGatewayRequiredLedgerBudgetPlan(t)
	return preparedGatewayCallWithPlan(t, plan, evaluator)
}

func assertIndeterminateReservation(t *testing.T, store *actionstate.Store) {
	t.Helper()
	status, err := store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LiveReservations != 1 || status.Indeterminate != 1 {
		t.Fatalf("indeterminate reservation state = %#v", status)
	}
}

func countBudgetTransitions(records []actionledger.Record, kind actionledger.BudgetTransitionKind) int {
	count := 0
	for _, record := range records {
		if record.Budget != nil && record.Budget.Kind == kind {
			count++
		}
	}
	return count
}
