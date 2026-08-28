package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

type gatedEvidenceProvider struct {
	mu           sync.Mutex
	entered      int
	firstEntered chan struct{}
	bothEntered  chan struct{}
	release      chan struct{}
}

func newGatedEvidenceProvider() *gatedEvidenceProvider {
	return &gatedEvidenceProvider{
		firstEntered: make(chan struct{}),
		bothEntered:  make(chan struct{}),
		release:      make(chan struct{}),
	}
}

func (p *gatedEvidenceProvider) Observe(
	ctx context.Context,
	_ PolicySnapshot,
	_ action.Request,
	_ action.Tool,
) (EvidenceSnapshot, error) {
	p.mu.Lock()
	p.entered++
	switch p.entered {
	case 1:
		close(p.firstEntered)
	case 2:
		close(p.bothEntered)
	}
	p.mu.Unlock()
	select {
	case <-p.release:
		return cleanEvidenceSnapshot(), nil
	case <-ctx.Done():
		return EvidenceSnapshot{}, ctx.Err()
	}
}

type stateMutatingEvidenceProvider struct {
	gateway  *Gateway
	once     sync.Once
	preCalls atomic.Int32
	err      error
}

func (p *stateMutatingEvidenceProvider) Observe(
	ctx context.Context,
	snapshot PolicySnapshot,
	request action.Request,
	_ action.Tool,
) (EvidenceSnapshot, error) {
	if request.Phase != action.PhasePreCall {
		return cleanEvidenceSnapshot(), nil
	}
	p.preCalls.Add(1)
	p.once.Do(func() {
		if p.gateway == nil {
			p.err = fmt.Errorf("gateway is unavailable")
			return
		}
		request.CallID, p.err = actionstate.NewRandomCallID()
		if p.err != nil {
			return
		}
		reserved, err := p.gateway.state.Reserve(ctx, actionstate.ReserveRequest{
			Plan: snapshot.Plan, Request: request, Context: p.gateway.boundContext,
			Authority: p.gateway.config.PolicyAuthority, Server: p.gateway.server,
		})
		if err != nil {
			p.err = err
			return
		}
		if reserved.Reservation == nil {
			p.err = fmt.Errorf("state mutation did not create a reservation")
			return
		}
		_, p.err = p.gateway.state.Release(
			ctx,
			reserved.Reservation.Identity,
			reserved.Snapshot.StateVersion,
		)
	})
	if p.err != nil {
		return EvidenceSnapshot{}, p.err
	}
	return cleanEvidenceSnapshot(), nil
}

func cleanEvidenceSnapshot() EvidenceSnapshot {
	return EvidenceSnapshot{
		Taint: action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
	}
}

func TestGatewayPreDispatchEvidenceProgressesIndependently(t *testing.T) {
	markers := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markers, "invoked"))
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markers, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	provider := newGatedEvidenceProvider()
	harness.gateway.config.EvidenceProvider = provider

	type outcome struct {
		result *mcp.CallToolResult
		err    error
	}
	call := func(value string) <-chan outcome {
		done := make(chan outcome, 1)
		go func() {
			result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "echo", Arguments: json.RawMessage(fmt.Sprintf(`{"value":%q}`, value)),
			})
			done <- outcome{result: result, err: err}
		}()
		return done
	}

	firstDone := call("first")
	select {
	case <-provider.firstEntered:
	case <-time.After(time.Second):
		close(provider.release)
		t.Fatal("first call did not enter pre-dispatch evidence")
	}
	secondDone := call("second")
	releaseOnce := sync.Once{}
	release := func() { releaseOnce.Do(func() { close(provider.release) }) }
	defer release()
	select {
	case <-provider.bothEntered:
		release()
	case <-time.After(time.Second):
		release()
		t.Fatal("independent pre-dispatch evidence remained globally serialized")
	}
	for index, done := range []<-chan outcome{firstDone, secondDone} {
		select {
		case got := <-done:
			if got.err != nil || got.result == nil || got.result.IsError {
				t.Fatalf("call %d = %#v, %v", index+1, got.result, got.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("call %d did not finish", index+1)
		}
	}
}

func TestGatewayReservationConflictRetriesOnlyStateBoundInput(t *testing.T) {
	markers := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markers, "invoked"))
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markers, "cancelled"))
	plan, evaluator := testGatewayBudgetPlanWithLimits(
		t,
		action.BudgetLimits{CallCount: 4, Concurrent: 1},
	)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	provider := &stateMutatingEvidenceProvider{gateway: harness.gateway}
	harness.gateway.config.EvidenceProvider = provider

	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"retry"}`),
	})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("conflicted call = %#v, %v", result, err)
	}
	if calls := provider.preCalls.Load(); calls != 2 {
		t.Fatalf("pre-call evidence observations across prepare and result boundaries = %d, want 2", calls)
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LiveReservations != 0 {
		t.Fatalf("live reservations after conflicted call = %d", status.LiveReservations)
	}
}
