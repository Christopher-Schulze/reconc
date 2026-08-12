package mcpgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionstate"
)

func TestGatewayFinalizeIssuedApprovalPersistsTerminalState(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x52}, ed25519.SeedSize))
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlan(t, action.PhasePreCall)
	harness := newGatewayLifecycleHarness(
		t, plan, evaluator, registry, "post-result-policy", nil, 5*time.Second,
	)
	contract, generation, exists := harness.gateway.tool("echo")
	if !exists {
		t.Fatal("gateway echo contract is unavailable")
	}
	callID, err := actionstate.NewRandomCallID()
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"value":"finalize-helper"}`)
	params := json.RawMessage(`{"name":"echo","arguments":{"value":"finalize-helper"}}`)
	call, response := harness.gateway.prepareCall(
		context.Background(), upstreamWireCall{id: json.RawMessage(`1`), params: params},
		contract, generation, callID, arguments, actionapproval.MCPProtocolVersion,
	)
	if call != nil || response == nil {
		t.Fatalf("approval preparation = call %#v, response %#v", call, response)
	}
	harness.gateway.pendingMu.Lock()
	if len(harness.gateway.pending) != 1 {
		harness.gateway.pendingMu.Unlock()
		t.Fatalf("pending approvals = %d, want 1", len(harness.gateway.pending))
	}
	var pending pendingApproval
	for _, value := range harness.gateway.pending {
		pending = value
	}
	harness.gateway.pendingMu.Unlock()
	result := harness.gateway.finalizeIssuedApproval(context.Background(), actionstate.ApprovalIssueResult{
		Request: pending.approvalRequest, RequestState: pending.requestState,
		StateVersion: pending.issuanceVersion,
	}, actionapproval.StatusCancelled)
	harness.gateway.removePending(pending.requestState)
	if result.Status != actionapproval.StatusCancelled || result.StateVersion == "" ||
		result.Evidence.RequestID != pending.approvalRequest.RequestID {
		t.Fatalf("finalized approval = %#v", result)
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil || status.PendingApprovals != 0 || status.LiveReservations != 0 ||
		len(status.ApprovalRecords) != 1 || status.ApprovalRecords[0].Status != actionapproval.StatusCancelled {
		t.Fatalf("finalized approval state = %#v, %v", status, err)
	}
}

func TestGatewayReleaseReservationRetriesCurrentStateVersion(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	plan, evaluator := testGatewayBudgetPlan(t)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	contract, _, exists := harness.gateway.tool("echo")
	if !exists {
		t.Fatal("gateway echo contract is unavailable")
	}
	staleVersion, err := harness.gateway.state.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	callID, err := actionstate.NewRandomCallID()
	if err != nil {
		t.Fatal(err)
	}
	request, err := harness.gateway.normalizedRequest(
		harness.gateway.snapshot, contract, callID, staleVersion,
		action.PhasePreCall, json.RawMessage(`{"value":"release-helper"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := harness.gateway.state.Reserve(context.Background(), actionstate.ReserveRequest{
		Plan: harness.gateway.snapshot.Plan, Request: request, Context: harness.gateway.boundContext,
		Authority: harness.gateway.config.PolicyAuthority, Server: harness.gateway.server,
	})
	if err != nil || reserved.Reservation == nil {
		t.Fatalf("reserve helper state = %#v, %v", reserved, err)
	}
	releasedVersion := harness.gateway.releaseReservation(
		context.Background(), reserved.Reservation, staleVersion,
	)
	if releasedVersion == "" || releasedVersion == staleVersion || releasedVersion == reserved.Snapshot.StateVersion {
		t.Fatalf("released state version = %q", releasedVersion)
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil || status.LiveReservations != 0 || status.TerminalCallCount != 1 {
		t.Fatalf("released reservation state = %#v, %v", status, err)
	}
}
