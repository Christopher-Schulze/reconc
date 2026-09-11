package mcpgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionstate"
)

func TestShutdownFinalizationCompletesWhenDenialCapacityIsExhausted(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x65}, ed25519.SeedSize))
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlanWithLimits(t, action.PhasePreCall, action.BudgetLimits{
		CallCount: 8, ApprovalCount: 8, DeniedCount: 1, Concurrent: MaxConcurrentCalls,
	})
	var diagnostics bytes.Buffer
	harness := newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{
		approvalAuthorities: registry,
		approvalPolicyID:    "post-result-policy",
		diagnostics:         &diagnostics,
	})
	prepareDetachedPendingApprovals(t, harness.gateway, action.PhasePreCall, 2)

	if err := harness.gateway.shutdownPending(context.Background()); err != nil {
		t.Fatalf("shutdown pending with denial exhaustion: %v", err)
	}
	if remaining := len(harness.gateway.pendingShutdownCleanup); remaining != 0 {
		t.Fatalf("stranded shutdown cleanup = %d", remaining)
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil || status.PendingApprovals != 0 || status.LiveReservations != 0 ||
		len(status.ApprovalRecords) != 2 {
		t.Fatalf("state after exhausted shutdown = %#v, %v", status, err)
	}
	for _, record := range status.ApprovalRecords {
		if record.Status != actionapproval.StatusCancelled {
			t.Fatalf("approval %q status = %q", record.CallID, record.Status)
		}
	}
	if !strings.Contains(diagnostics.String(), string(action.ReasonBudgetExhausted)) {
		t.Fatalf("missing denial-capacity diagnostic: %q", diagnostics.String())
	}

	if err := harness.gateway.shutdownPending(context.Background()); err != nil {
		t.Fatalf("idempotent shutdown retry: %v", err)
	}
	if err := harness.gateway.Close(); err != nil {
		t.Fatalf("close after exhausted shutdown: %v", err)
	}
	if err := harness.gateway.lease.Close(); err != nil {
		t.Fatalf("lease after exhausted shutdown: %v", err)
	}
}

func TestShutdownFinalizationRecoversPersistedResultAfterDroppedAck(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x66}, ed25519.SeedSize))
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlan(t, action.PhasePreCall)
	harness := newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{
		approvalAuthorities: registry,
		approvalPolicyID:    "post-result-policy",
	})
	prepareDetachedPendingApprovals(t, harness.gateway, action.PhasePreCall, 1)

	harness.gateway.pendingMu.Lock()
	var cleanupState string
	for state, pending := range harness.gateway.pending {
		cleanupState = state
		outcome, finalizeErr := harness.gateway.state.FinalizeApproval(context.Background(), actionstate.ApprovalFinalizeRequest{
			RequestState:         pending.requestState,
			ExpectedStateVersion: pending.issuanceVersion,
			Status:               actionapproval.StatusCancelled,
		})
		if finalizeErr != nil {
			harness.gateway.pendingMu.Unlock()
			t.Fatal(finalizeErr)
		}
		if _, ok := outcome.TerminalResult(); !ok {
			harness.gateway.pendingMu.Unlock()
			t.Fatalf("direct finalization = %#v", outcome)
		}
	}
	harness.gateway.pendingMu.Unlock()

	if err := harness.gateway.shutdownPending(context.Background()); err != nil {
		t.Fatalf("shutdown recovery after dropped ack: %v", err)
	}
	if remaining := len(harness.gateway.pendingShutdownCleanup); remaining != 0 {
		t.Fatalf("shutdown cleanup remained after recovery: %d", remaining)
	}
	if cleanupState == "" {
		t.Fatal("pending approval state was missing")
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil || status.PendingApprovals != 0 || status.LiveReservations != 0 {
		t.Fatalf("recovered shutdown state = %#v, %v", status, err)
	}
}

func TestShutdownFinalizationKeepsCleanupWhenLedgerDeliveryFails(t *testing.T) {
	cleanup := &pendingApprovalCleanup{
		pending: pendingApproval{
			callID: "act_aaaaaaaaaaaaaaaaaaaaaaaaaa",
			phase:  action.PhasePreCall,
			ledger: nil,
		},
		result: actionstate.ApprovalConsumeResult{
			StateVersion: "state",
			Evidence:     actionstate.ApprovalEvidence{RequestID: "apr_aaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
		finalized: true,
	}
	err := (&Gateway{}).finishTerminalizedApproval(context.Background(), cleanup)
	if err == nil || !strings.Contains(err.Error(), "ledger is unavailable") {
		t.Fatalf("missing ledger error = %v", err)
	}
	if !cleanup.finalized || cleanup.result.StateVersion != "state" {
		t.Fatalf("delivery failure cleared persisted finalization: %#v", cleanup)
	}
}
