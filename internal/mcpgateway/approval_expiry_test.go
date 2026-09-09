package mcpgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionstate"
)

type gatewayControlledClock struct {
	mu     sync.Mutex
	now    time.Time
	source string
}

func (c *gatewayControlledClock) Snapshot() (actionstate.ClockSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return actionstate.ClockSnapshot{Time: c.now, Source: c.source}, nil
}

func (c *gatewayControlledClock) advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

func TestGatewayReconcilesExpiredPendingApprovalsBeforeAdmission(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	clock := &gatewayControlledClock{now: time.Now().UTC(), source: "gateway-test-clock"}
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{'x'}, ed25519.SeedSize))
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayPendingApprovalPlan(t)
	harness := newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{
		approvalAuthorities: registry, approvalPolicyID: "post-result-policy", clock: clock,
	})
	initialized := harness.exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"expiry-test","version":"test"}}}`)
	if len(initialized.Error) != 0 {
		t.Fatalf("initialize error = %s", initialized.Error)
	}
	harness.notify(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	var expiredRequestState string
	var expiredInputResponses map[string]any
	for index := 0; index < MaxPendingApprovals; index++ {
		response := harness.exchange(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(2+index)+`,"method":"tools/call","params":{"name":"echo","arguments":{"value":"expired"}}}`)
		if len(response.Error) != 0 || len(response.Result) == 0 {
			t.Fatalf("pending approval %d response = result %s, error %s", index, response.Result, response.Error)
		}
		if index == 0 {
			result := decodeRawToolResult(t, response)
			expiredRequestState = result.RequestState
			expiredInputResponses = make(map[string]any, len(result.InputRequests))
			for requestID, request := range result.InputRequests {
				expiredInputResponses[requestID] = map[string]any{
					"action": "accept",
					"content": map[string]any{
						"receipt": signRawApproval(t, request.Params.Message, privateKey),
					},
				}
			}
		}
	}
	harness.gateway.pendingMu.Lock()
	if len(harness.gateway.pending) != MaxPendingApprovals {
		harness.gateway.pendingMu.Unlock()
		t.Fatalf("pending approvals before expiry = %d, want %d", len(harness.gateway.pending), MaxPendingApprovals)
	}
	harness.gateway.pendingMu.Unlock()

	clock.advance(actionapproval.MaximumApprovalTTL + time.Second)
	oldRetry, err := json.Marshal(map[string]any{
		"name": "echo", "arguments": map[string]any{"value": "expired"},
		"requestState": expiredRequestState, "inputResponses": expiredInputResponses,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldResponse := decodeRawToolResult(t, harness.exchange(t,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":`+string(oldRetry)+`}`,
	))
	if !oldResponse.IsError || !strings.Contains(rawResultText(oldResponse), string(action.ReasonApprovalExpired)) {
		t.Fatalf("expired approval replay = %#v", oldResponse)
	}
	response := harness.exchange(t, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"echo","arguments":{"value":"admitted-after-expiry"}}}`)
	if len(response.Error) != 0 || len(response.Result) == 0 {
		t.Fatalf("post-expiry admission = result %s, error %s", response.Result, response.Error)
	}
	harness.gateway.pendingMu.Lock()
	pendingCount, cleanupCount := len(harness.gateway.pending), len(harness.gateway.pendingCleanup)
	harness.gateway.pendingMu.Unlock()
	if pendingCount != 1 || cleanupCount != 0 {
		t.Fatalf("post-expiry in-memory state = pending %d, cleanup %d; want 1, 0", pendingCount, cleanupCount)
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 1 {
		t.Fatalf("post-expiry durable pending approvals = %d, want 1", status.PendingApprovals)
	}
	var oldStates []string
	harness.gateway.pendingMu.Lock()
	for _, pending := range harness.gateway.pending {
		oldStates = append(oldStates, pending.requestState)
	}
	harness.gateway.pendingMu.Unlock()
	if len(oldStates) != 1 {
		t.Fatalf("new pending request state count = %d, want 1", len(oldStates))
	}
}

func TestGatewayReconcilesExpiredPostResultApprovalsBeforeAdmission(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	clock := &gatewayControlledClock{now: time.Now().UTC(), source: "gateway-post-expiry-test-clock"}
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{'y'}, ed25519.SeedSize))
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlanWithLimits(t, action.PhasePostResult, action.BudgetLimits{
		CallCount: 8, ApprovalCount: 8,
	})
	harness := newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{
		approvalAuthorities: registry, approvalPolicyID: "post-result-policy", clock: clock,
	})
	initialized := harness.exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"post-expiry-test","version":"test"}}}`)
	if len(initialized.Error) != 0 {
		t.Fatalf("initialize error = %s", initialized.Error)
	}
	harness.notify(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	for index := 0; index < MaxPendingApprovals; index++ {
		response := harness.exchange(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(2+index)+`,"method":"tools/call","params":{"name":"echo","arguments":{"value":"post-expired"}}}`)
		result := decodeRawToolResult(t, response)
		if result.ResultType != "input_required" || result.RequestState == "" {
			t.Fatalf("post-result pending approval %d = %#v", index, result)
		}
	}
	harness.gateway.pendingMu.Lock()
	if len(harness.gateway.pending) != MaxPendingApprovals {
		harness.gateway.pendingMu.Unlock()
		t.Fatalf("post-result pending approvals before expiry = %d, want %d", len(harness.gateway.pending), MaxPendingApprovals)
	}
	harness.gateway.pendingMu.Unlock()

	clock.advance(actionapproval.MaximumApprovalTTL + time.Second)
	response := decodeRawToolResult(t, harness.exchange(t, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"echo","arguments":{"value":"post-admitted-after-expiry"}}}`))
	if response.ResultType != "input_required" || response.RequestState == "" {
		t.Fatalf("post-expiry post-result admission = %#v", response)
	}
	harness.gateway.pendingMu.Lock()
	pendingCount, cleanupCount := len(harness.gateway.pending), len(harness.gateway.pendingCleanup)
	harness.gateway.pendingMu.Unlock()
	if pendingCount != 1 || cleanupCount != 0 {
		t.Fatalf("post-expiry post-result in-memory state = pending %d, cleanup %d; want 1, 0", pendingCount, cleanupCount)
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 1 || status.LiveReservations != 1 || status.TerminalCallCount != MaxPendingApprovals {
		t.Fatalf("post-expiry post-result state = %#v", status)
	}
	expired := 0
	for _, record := range status.ApprovalRecords {
		if record.Status == actionapproval.StatusExpired {
			expired++
		}
	}
	if expired != MaxPendingApprovals {
		t.Fatalf("post-expiry approval records = %#v, want %d expired", status.ApprovalRecords, MaxPendingApprovals)
	}
}
