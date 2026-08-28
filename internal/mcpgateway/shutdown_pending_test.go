package mcpgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionstate"
)

type stuckShutdownDownstream struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*stuckShutdownDownstream) ProtocolVersion() string { return gatewayProtocolCurrent }

func (*stuckShutdownDownstream) ListTools(context.Context, string) (ToolPage, error) {
	return ToolPage{}, nil
}

func (d *stuckShutdownDownstream) CallTool(
	context.Context,
	string,
	json.RawMessage,
	ProgressSink,
) (CallResult, error) {
	d.once.Do(func() { close(d.started) })
	<-d.release
	return CallResult{}, errors.New("stuck downstream released")
}

func (*stuckShutdownDownstream) Close() error { return nil }
func (*stuckShutdownDownstream) Wait() error  { return nil }

func TestCallContextObservesRequestAndGatewayCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		cancel func(context.CancelFunc, context.CancelFunc)
	}{
		{name: "request", cancel: func(requestCancel, _ context.CancelFunc) { requestCancel() }},
		{name: "gateway", cancel: func(_, gatewayCancel context.CancelFunc) { gatewayCancel() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			gatewayCtx, gatewayCancel := context.WithCancel(context.Background())
			requestCtx, requestCancel := context.WithCancel(context.Background())
			gateway := &Gateway{
				ctx: gatewayCtx, cancel: gatewayCancel,
				config: Config{CallTimeout: time.Minute},
			}
			callCtx, cancelCall := gateway.callContext(requestCtx)
			t.Cleanup(cancelCall)

			test.cancel(requestCancel, gatewayCancel)
			select {
			case <-callCtx.Done():
				if !errors.Is(callCtx.Err(), context.Canceled) {
					t.Fatalf("combined call context error = %v", callCtx.Err())
				}
			case <-time.After(time.Second):
				t.Fatal("combined call context did not observe cancellation")
			}
		})
	}
}

func TestFinalizePendingAndDrainCallsReportsBothFailures(t *testing.T) {
	gateway := &Gateway{
		pending:   map[string]pendingApproval{"pending": {requestState: "pending"}},
		semaphore: make(chan struct{}, MaxConcurrentCalls),
	}
	gateway.semaphore <- struct{}{}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	err := gateway.finalizePendingAndDrainCalls(shutdownCtx)
	if err == nil || !strings.Contains(err.Error(), "finalize pending approval call") ||
		!strings.Contains(err.Error(), "gateway calls did not terminate") {
		t.Fatalf("combined shutdown error = %v", err)
	}
	if len(gateway.pending) != 0 {
		t.Fatalf("failed pending finalization retained in-memory state: %#v", gateway.pending)
	}
	for len(gateway.semaphore) != 0 {
		<-gateway.semaphore
	}
}

func TestPendingApprovalsFinalizeBeforeIndependentCallDrainTimeout(t *testing.T) {
	for _, phase := range []action.Phase{action.PhasePreCall, action.PhasePostResult} {
		t.Run(string(phase), func(t *testing.T) {
			testPendingApprovalFinalizesBeforeDrainTimeout(t, phase)
		})
	}
}

func testPendingApprovalFinalizesBeforeDrainTimeout(t *testing.T, phase action.Phase) {
	t.Helper()
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x62}, ed25519.SeedSize))
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlan(t, phase)
	elicitationStarted := make(chan struct{})
	releaseElicitation := make(chan struct{})
	options := &mcp.ClientOptions{ElicitationHandler: func(
		context.Context,
		*mcp.ElicitRequest,
	) (*mcp.ElicitResult, error) {
		close(elicitationStarted)
		<-releaseElicitation
		return &mcp.ElicitResult{Action: "cancel"}, nil
	}}
	harness := newGatewayLifecycleHarness(
		t, plan, evaluator, registry, "post-result-policy", options, 5*time.Second,
	)
	approvalDone := make(chan error, 1)
	go func() {
		_, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "echo", Arguments: json.RawMessage(`{"value":"pending"}`),
		})
		approvalDone <- err
	}()
	select {
	case <-elicitationStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("approval elicitation did not start")
	}

	harness.gateway.pendingMu.Lock()
	pendingCount := len(harness.gateway.pending)
	if pendingCount != 1 {
		harness.gateway.pendingMu.Unlock()
		t.Fatalf("pending approvals = %d, want 1", pendingCount)
	}
	harness.gateway.pendingMu.Unlock()

	stuck := &stuckShutdownDownstream{started: make(chan struct{}), release: make(chan struct{})}
	stuckCtx, cancelStuck := harness.gateway.callContext(context.Background())
	defer cancelStuck()
	harness.gateway.semaphore <- struct{}{}
	stuckDone := make(chan struct{})
	go func() {
		defer close(stuckDone)
		defer func() { <-harness.gateway.semaphore }()
		_, _ = stuck.CallTool(stuckCtx, "echo", nil, nil)
	}()
	select {
	case <-stuck.started:
	case <-time.After(2 * time.Second):
		t.Fatal("independent downstream call did not start")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 500*time.Millisecond)
	err := harness.gateway.finalizePendingAndDrainCalls(shutdownCtx)
	cancelShutdown()
	if err == nil || !strings.Contains(err.Error(), "gateway calls did not terminate") {
		t.Fatalf("call-drain timeout = %v", err)
	}
	for index := 0; index < MaxConcurrentCalls-1; index++ {
		<-harness.gateway.semaphore
	}
	status, statusErr := harness.gateway.state.Status(context.Background())
	if statusErr != nil || status.PendingApprovals != 0 || status.LiveReservations != 0 ||
		len(status.ApprovalRecords) != 1 ||
		status.ApprovalRecords[0].Status != actionapproval.StatusCancelled {
		t.Fatalf("state after pending shutdown finalization = %#v, %v", status, statusErr)
	}

	harness.gateway.cancel()
	close(stuck.release)
	close(releaseElicitation)
	select {
	case <-stuckDone:
	case <-time.After(2 * time.Second):
		t.Fatal("stuck downstream call did not terminate after release")
	}
	select {
	case <-approvalDone:
	case <-time.After(2 * time.Second):
		t.Fatal("approval call did not terminate after finalization")
	}
}

func TestShutdownPendingContinuesAfterOrderedFailures(t *testing.T) {
	tests := []struct {
		name           string
		phase          action.Phase
		failureIndexes []int
	}{
		{name: "first pre-call", phase: action.PhasePreCall, failureIndexes: []int{0}},
		{name: "middle pre-call", phase: action.PhasePreCall, failureIndexes: []int{1}},
		{name: "last pre-call", phase: action.PhasePreCall, failureIndexes: []int{2}},
		{name: "middle post-result", phase: action.PhasePostResult, failureIndexes: []int{1}},
		{name: "multiple post-result", phase: action.PhasePostResult, failureIndexes: []int{0, 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testShutdownPendingContinuesAfterOrderedFailures(
				t, test.phase, test.failureIndexes,
			)
		})
	}
}

func testShutdownPendingContinuesAfterOrderedFailures(
	t *testing.T,
	phase action.Phase,
	failureIndexes []int,
) {
	t.Helper()
	const pendingCount = 3
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x63}, ed25519.SeedSize))
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlanWithLimits(t, phase, action.BudgetLimits{
		CallCount: 8, ApprovalCount: 8, Concurrent: MaxConcurrentCalls,
	})
	harness := newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{
		approvalAuthorities: registry,
		approvalPolicyID:    "post-result-policy",
	})
	prepareDetachedPendingApprovals(t, harness.gateway, phase, pendingCount)

	harness.gateway.pendingMu.Lock()
	ordered := make([]pendingApproval, 0, len(harness.gateway.pending))
	for _, pending := range harness.gateway.pending {
		ordered = append(ordered, pending)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].callID < ordered[j].callID })
	if len(ordered) != pendingCount {
		harness.gateway.pendingMu.Unlock()
		t.Fatalf("pending approval count = %d, want %d", len(ordered), pendingCount)
	}
	failures := make(map[int]struct{}, len(failureIndexes))
	ownedBuffers := make([][]byte, 0, pendingCount*4)
	for _, index := range failureIndexes {
		failures[index] = struct{}{}
		pending := harness.gateway.pending[ordered[index].requestState]
		pending.issuanceVersion = "invalid-shutdown-state-version"
		harness.gateway.pending[ordered[index].requestState] = pending
	}
	for _, pending := range ordered {
		ownedBuffers = append(ownedBuffers,
			pending.originalRPCID,
			pending.originalParams,
			pending.canonicalArguments,
			pending.rawResult,
		)
	}
	harness.gateway.pendingMu.Unlock()

	err := harness.gateway.shutdownPending(context.Background())
	if err == nil {
		t.Fatal("ordered pending failures were discarded")
	}
	errorText := err.Error()
	lastFailureOffset := -1
	for index, pending := range ordered {
		_, failed := failures[index]
		if failed {
			offset := strings.Index(errorText, pending.callID)
			if offset < 0 || !strings.Contains(errorText, string(phase)) {
				t.Fatalf("pending failure omitted safe identity: %v", err)
			}
			if offset <= lastFailureOffset {
				t.Fatalf("pending failures are not ordered by call identity: %v", err)
			}
			lastFailureOffset = offset
			continue
		}
		if strings.Contains(errorText, pending.callID) {
			t.Fatalf("successful pending approval reported as failed: %v", err)
		}
	}
	for _, buffer := range ownedBuffers {
		for _, value := range buffer {
			if value != 0 {
				t.Fatal("detached pending approval retained sensitive bytes")
			}
		}
	}
	if len(harness.gateway.pending) != 0 {
		t.Fatalf("shutdown retained detached pending approvals: %#v", harness.gateway.pending)
	}
	status, statusErr := harness.gateway.state.Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	statusByCall := make(map[string]actionapproval.Status, len(status.ApprovalRecords))
	for _, record := range status.ApprovalRecords {
		statusByCall[record.CallID] = record.Status
	}
	for index, pending := range ordered {
		want := actionapproval.StatusCancelled
		if _, failed := failures[index]; failed {
			want = actionapproval.StatusPending
		}
		if statusByCall[pending.callID] != want {
			t.Fatalf("approval %q status = %q, want %q", pending.callID, statusByCall[pending.callID], want)
		}
	}
	if status.PendingApprovals != len(failures) || status.LiveReservations != len(failures) {
		t.Fatalf("partial shutdown state = %#v", status)
	}
}

func prepareDetachedPendingApprovals(
	t *testing.T,
	gateway *Gateway,
	phase action.Phase,
	count int,
) {
	t.Helper()
	contract, generation, exists := gateway.tool("echo")
	if !exists {
		t.Fatal("gateway echo contract is unavailable")
	}
	for index := 0; index < count; index++ {
		callID, err := actionstate.NewRandomCallID()
		if err != nil {
			t.Fatal(err)
		}
		arguments := json.RawMessage(fmt.Sprintf(`{"value":"pending-%d"}`, index))
		wire := upstreamWireCall{
			id: json.RawMessage(fmt.Sprintf("%d", index+1)),
			params: json.RawMessage(fmt.Sprintf(
				`{"name":"echo","arguments":{"value":"pending-%d"}}`,
				index,
			)),
		}
		call, response := gateway.prepareCall(
			context.Background(), wire, contract, generation, callID, arguments,
			gatewayProtocolCurrent,
		)
		if phase == action.PhasePreCall {
			if call != nil || response == nil {
				t.Fatalf("prepare detached pre-call approval = call %#v, response %#v", call, response)
			}
			continue
		}
		if call == nil || response != nil {
			t.Fatalf("prepare detached post-result approval = call %#v, response %#v", call, response)
		}
		response, err = gateway.finishCall(context.Background(), call, CallResult{
			Canonical: json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"hello"}]}`),
			Protocol:  gatewayProtocolCurrent,
		})
		if err != nil || response == nil {
			t.Fatalf("finish detached post-result approval = response %#v, error %v", response, err)
		}
	}
	gateway.pendingMu.Lock()
	pendingCount := len(gateway.pending)
	gateway.pendingMu.Unlock()
	if pendingCount != count {
		t.Fatalf("detached pending approval count = %d, want %d", pendingCount, count)
	}
}
