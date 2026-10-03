package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
)

type dispatchConflictLoader struct {
	base          PolicyLoader
	gateway       *Gateway
	loads         int
	mutations     int
	maximum       int
	beforeLoad    func(int, *PolicySnapshot)
	afterMutation func()
}

func (l *dispatchConflictLoader) Load(ctx context.Context, repository string) (PolicySnapshot, error) {
	snapshot, err := l.base.Load(ctx, repository)
	l.loads++
	if l.beforeLoad != nil {
		l.beforeLoad(l.loads, &snapshot)
	}
	if err != nil || l.loads < 2 || l.mutations >= l.maximum {
		return snapshot, err
	}
	version, err := l.gateway.state.CurrentStateVersion(ctx)
	if err != nil {
		return PolicySnapshot{}, err
	}
	contract, _, exists := l.gateway.tool("echo")
	if !exists {
		return PolicySnapshot{}, fmt.Errorf("echo contract missing")
	}
	callID, err := actionstate.NewRandomCallID()
	if err != nil {
		return PolicySnapshot{}, err
	}
	request, err := l.gateway.normalizedRequest(snapshot, contract, callID, version, action.PhasePreCall, json.RawMessage(`{"value":"competitor"}`))
	if err != nil {
		return PolicySnapshot{}, err
	}
	reserved, err := l.gateway.state.Reserve(ctx, actionstate.ReserveRequest{Plan: snapshot.Plan, Request: request, Context: l.gateway.boundContext, Authority: l.gateway.config.PolicyAuthority, Server: l.gateway.server})
	if err != nil {
		return PolicySnapshot{}, err
	}
	if reserved.Reservation == nil {
		return PolicySnapshot{}, fmt.Errorf("competitor reservation missing")
	}
	_, err = l.gateway.state.Release(ctx, reserved.Reservation.Identity, reserved.Snapshot.StateVersion)
	if err == nil {
		l.mutations++
		if l.afterMutation != nil {
			l.afterMutation()
		}
	}
	return snapshot, err
}

func newDispatchConflictHarness(t *testing.T) (*gatewayLifecycleHarness, *dispatchConflictLoader) {
	t.Helper()
	markers := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markers, "invoked"))
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markers, "cancelled"))
	plan, _ := testGatewayBudgetPlanWithLimits(t, action.BudgetLimits{CallCount: 4, Concurrent: 2})
	definition := plan.Plan()
	definition.Ledger.Mode = action.LedgerRequired
	plan, err := action.CompilePlan(definition)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(plan)
	if err != nil {
		t.Fatal(err)
	}
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	loader := &dispatchConflictLoader{base: harness.gateway.config.PolicyLoader, gateway: harness.gateway, maximum: 1}
	harness.gateway.config.PolicyLoader = loader
	return harness, loader
}

func TestGatewayDispatchConflictRecoversWithoutDoubleCharge(t *testing.T) {
	harness, loader := newDispatchConflictHarness(t)
	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{Name: "echo", Arguments: json.RawMessage(`{"value":"retry"}`)})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("dispatch conflict call: %#v, %v", result, err)
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil || loader.mutations != 1 || status.LiveReservations != 0 || len(status.Budgets) != 1 || status.Budgets[0].Consumed.CallCount != 1 {
		t.Fatalf("dispatch accounting: %+v, mutations=%d, err=%v", status, loader.mutations, err)
	}
	records, verification, err := harness.gateway.ledger.Snapshot(context.Background())
	if err != nil || verification.Integrity != actionledger.StatusVerified || !verification.CallsComplete {
		t.Fatalf("dispatch ledger: %+v, %v", verification, err)
	}
	dispatches, decisions := 0, 0
	for _, record := range records {
		if record.Event == actionledger.EventDownstreamDispatch {
			dispatches++
		}
		if record.Event == actionledger.EventPreDecision && record.Decision.Phase == action.PhasePreCall {
			decisions++
		}
	}
	if dispatches != 1 || decisions != 1 {
		t.Fatalf("dispatches=%d, pre-call decisions=%d", dispatches, decisions)
	}
}

func TestGatewayDispatchConflictFailuresReleaseWithoutDispatch(t *testing.T) {
	for _, name := range []string{"exhaustion", "policy-change", "cancellation"} {
		t.Run(name, func(t *testing.T) {
			harness, loader := newDispatchConflictHarness(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "exhaustion":
				loader.maximum = MaxReservationConflictRetries + 1
			case "policy-change":
				loader.beforeLoad = func(load int, snapshot *PolicySnapshot) {
					if load >= 3 {
						snapshot.SourceDigest = "changed-policy"
					}
				}
			case "cancellation":
				loader.afterMutation = cancel
			}
			gateway := harness.gateway
			contract, generation, exists := gateway.tool("echo")
			if !exists {
				t.Fatal("echo contract missing")
			}
			callID, err := actionstate.NewRandomCallID()
			if err != nil {
				t.Fatal(err)
			}
			call, response := gateway.prepareCall(ctx, upstreamWireCall{id: json.RawMessage(`1`), params: json.RawMessage(`{"name":"echo","arguments":{"value":"blocked"}}`)}, contract, generation, callID, json.RawMessage(`{"value":"blocked"}`), gatewayProtocolCurrent)
			if call != nil || response == nil || !response.IsError {
				t.Fatalf("unsafe retry became dispatchable: %+v, %+v", call, response)
			}
			wantMutations := 1
			if name == "exhaustion" {
				wantMutations = MaxReservationConflictRetries + 1
			}
			status, err := gateway.state.Status(context.Background())
			if err != nil || loader.mutations != wantMutations || status.LiveReservations != 0 || len(status.Budgets) != 1 || status.Budgets[0].Consumed.CallCount != 0 {
				t.Fatalf("failed retry leaked/charged: %+v mutations=%d err=%v", status, loader.mutations, err)
			}
			records, verification, err := gateway.ledger.Snapshot(context.Background())
			if err != nil || !verification.CallsComplete || verification.Integrity != actionledger.StatusVerified {
				t.Fatalf("failed retry ledger: %+v, %v", verification, err)
			}
			for _, record := range records {
				if record.Event == actionledger.EventDownstreamDispatch {
					t.Fatal("failed retry recorded dispatch")
				}
			}
		})
	}
}

func TestGatewayDispatchConflictNeverRebindsPendingApproval(t *testing.T) {
	harness, _ := newDenialRecoveryHarness(t, action.PhasePreCall, action.DecisionRequireApproval)
	gateway := harness.gateway
	prepareDetachedPendingApprovals(t, gateway, action.PhasePreCall, 1)
	var call *gatewayCall
	gateway.pendingMu.Lock()
	for _, pending := range gateway.pending {
		call = callFromPending(pending)
	}
	gateway.pendingMu.Unlock()
	if call == nil || call.reservation == nil {
		t.Fatal("real pending approval reservation missing")
	}
	call.stateVersion = "stale-approval-state"
	loader := &dispatchConflictLoader{base: gateway.config.PolicyLoader, gateway: gateway}
	gateway.config.PolicyLoader = loader
	_, err := gateway.markDispatchWithRetry(context.Background(), call)
	if !errors.Is(err, actionstate.ErrStateVersionChanged) || loader.loads != 1 || call.stateVersion != "stale-approval-state" {
		t.Fatalf("approval state was rebound: %v loads=%d version=%s", err, loader.loads, call.stateVersion)
	}
	status, err := gateway.state.Status(context.Background())
	if err != nil || status.PendingApprovals != 1 || status.LiveReservations != 1 || status.Budgets[0].Consumed.CallCount != 0 {
		t.Fatalf("approval retry altered accounting: %+v, %v", status, err)
	}
}
