package mcpgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
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
	if err == nil || !strings.Contains(err.Error(), "finalize pending approval during shutdown") ||
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
