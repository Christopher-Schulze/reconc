package mcpgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
)

func TestApprovalRecoveryRetriesActualLedgerFailureWithoutRecharging(t *testing.T) {
	for _, phase := range []action.Phase{action.PhasePreCall, action.PhasePostResult} {
		t.Run(string(phase), func(t *testing.T) {
			harness, diagnostics := newDenialRecoveryHarness(t, phase, action.DecisionRequireApproval)
			gateway := harness.gateway
			prepareDetachedPendingApprovals(t, gateway, phase, 2)
			gateway.transitionMu.Lock()
			gateway.pendingMu.Lock()
			pending := make([]pendingApproval, 0, len(gateway.pending))
			for _, item := range gateway.pending {
				pending = append(pending, item)
			}
			gateway.pendingMu.Unlock()
			for _, item := range pending {
				outcome, err := gateway.state.FinalizeApproval(context.Background(), actionstate.ApprovalFinalizeRequest{
					RequestState: item.requestState, ExpectedStateVersion: item.issuanceVersion,
					Status: actionapproval.StatusCancelled,
				})
				if _, ok := outcome.TerminalResult(); !ok || err != nil && !actionstate.DenialCountCapacityExhausted(err) {
					gateway.transitionMu.Unlock()
					t.Fatalf("durable finalization: %+v, %v", outcome, err)
				}
				// Deliberately drop the acknowledgement before gateway recovery.
			}
			gateway.transitionMu.Unlock()
			restore := obstructRecoveryLedger(t, gateway)
			if err := gateway.shutdownPending(context.Background()); err == nil {
				t.Fatal("directory replacing the real ledger did not fail delivery")
			}
			if len(gateway.pendingShutdownCleanup) != 2 {
				t.Fatalf("failed ledger delivery lost cleanup: %d", len(gateway.pendingShutdownCleanup))
			}
			before, err := gateway.state.Status(context.Background())
			if err != nil || before.PendingApprovals != 0 {
				t.Fatalf("durable state after ledger failure: %+v, %v", before, err)
			}
			restore()
			if err := gateway.shutdownPending(context.Background()); err != nil {
				t.Fatal(err)
			}
			after, err := gateway.state.Status(context.Background())
			if err != nil || after.PendingApprovals != 0 || after.LiveReservations != 0 || len(gateway.pendingShutdownCleanup) != 0 {
				t.Fatalf("recovered state: %+v, %v", after, err)
			}
			wantDenials := uint64(0)
			if phase == action.PhasePreCall {
				wantDenials = 1
				if after.StateVersion != before.StateVersion {
					t.Fatal("ledger retry rewrote terminal approval state")
				}
				if strings.Count(diagnostics.String(), string(action.ReasonBudgetExhausted)) != 1 {
					t.Fatalf("recovered diagnostic was lost or repeated: %q", diagnostics.String())
				}
			}
			if after.Budgets[0].Consumed.DeniedCount != wantDenials || after.Budgets[0].Consumed.ApprovalCount != 0 {
				t.Fatalf("recovery charged budgets again: %+v", after.Budgets)
			}
			records, report, err := gateway.ledger.Snapshot(context.Background())
			if err != nil || report.Integrity != actionledger.StatusVerified || !report.CallsComplete {
				t.Fatalf("recovery did not finish a valid ledger: %+v, %v", report, err)
			}
			var charged int64
			terminalApprovals := 0
			for _, record := range records {
				if record.Budget != nil {
					charged += record.Budget.ConsumedDelta.DeniedCount
				}
				if record.Approval != nil && record.Approval.Status == actionapproval.StatusCancelled {
					terminalApprovals++
				}
			}
			if charged != int64(wantDenials) || terminalApprovals != 2 {
				t.Fatalf("ledger recovery accounting: charged=%d approvals=%d", charged, terminalApprovals)
			}
			diagnosticBefore := diagnostics.String()
			if err := gateway.shutdownPending(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, repeated, err := gateway.ledger.Snapshot(context.Background())
			if err != nil || repeated.RecordCount != report.RecordCount || diagnostics.String() != diagnosticBefore {
				t.Fatalf("repeated cleanup emitted duplicate evidence: %+v, %v", repeated, err)
			}
		})
	}
}

func TestOrdinaryDenialLedgerUsesActualCommittedConsumption(t *testing.T) {
	harness, _ := newDenialRecoveryHarness(t, action.PhasePreCall, action.DecisionBlock)
	gateway := harness.gateway
	contract, generation, exists := gateway.tool("echo")
	if !exists {
		t.Fatal("echo contract missing")
	}
	for index := 0; index < 3; index++ {
		callID, err := actionstate.NewRandomCallID()
		if err != nil {
			t.Fatal(err)
		}
		wire := upstreamWireCall{id: json.RawMessage(fmt.Sprint(index + 1)), params: json.RawMessage(`{"name":"echo","arguments":{"value":"denied"}}`)}
		call, response := gateway.prepareCall(context.Background(), wire, contract, generation, callID, json.RawMessage(`{"value":"denied"}`), gatewayProtocolCurrent)
		if call != nil || response == nil {
			t.Fatal("denied tool call became dispatchable")
		}
		assertGatewayFailureReason(t, response, nil, action.ReasonRuleMatched, "")
	}
	state, err := gateway.state.Status(context.Background())
	if err != nil || state.TerminalCallCount != 3 || state.Budgets[0].Consumed.DeniedCount != 1 {
		t.Fatalf("ordinary denial state: %+v, %v", state, err)
	}
	records, report, err := gateway.ledger.Snapshot(context.Background())
	if err != nil || !report.CallsComplete {
		t.Fatalf("ordinary denial ledger: %+v, %v", report, err)
	}
	var charged int64
	denials := 0
	for _, record := range records {
		if record.Budget != nil && record.Budget.Kind == actionledger.BudgetDenied {
			charged += record.Budget.ConsumedDelta.DeniedCount
			denials++
		}
	}
	if charged != 1 || denials != 3 {
		t.Fatalf("ordinary denial ledger charged=%d transitions=%d", charged, denials)
	}
}

func newDenialRecoveryHarness(t *testing.T, phase action.Phase, decision action.Decision) (*rawGatewayHarness, *bytes.Buffer) {
	t.Helper()
	directory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(directory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(directory, "cancelled"))
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x67}, ed25519.SeedSize))
	registry := writeGatewayApprovalRegistry(t, key.Public().(ed25519.PublicKey))
	plan, _ := testGatewayApprovalPlanWithLimits(t, phase, action.BudgetLimits{CallCount: 8, ApprovalCount: 8, DeniedCount: 1, Concurrent: MaxConcurrentCalls})
	source := plan.Plan()
	source.Ledger.Mode = action.LedgerRequired
	source.Rules[0].Decision = decision
	plan, err := action.CompilePlan(source)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(plan)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := &bytes.Buffer{}
	return newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{
		approvalAuthorities: registry, approvalPolicyID: "post-result-policy", diagnostics: diagnostics,
	}), diagnostics
}

func obstructRecoveryLedger(t *testing.T, gateway *Gateway) func() {
	t.Helper()
	directory := t.TempDir()
	ledgerPath := filepath.Join(gateway.storage.ActionDirectory(), "ledger.jsonl")
	backup := filepath.Join(directory, "ledger-backup")
	if err := os.Rename(ledgerPath, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(ledgerPath, 0o700); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		if err := os.Rename(ledgerPath, filepath.Join(directory, "obstruction")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(backup, ledgerPath); err != nil {
			t.Fatal(err)
		}
		restored = true
	}
	t.Cleanup(restore)
	return restore
}
