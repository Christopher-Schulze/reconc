package mcpgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/pathidentity"
)

type gatewayLifecycleHarness struct {
	gateway     *Gateway
	session     *mcp.ClientSession
	cancel      context.CancelFunc
	serveDone   chan error
	diagnostics *bytes.Buffer
	home        string
	repository  string
	closeOnce   sync.Once
	closeErr    error
}

func newGatewayLifecycleHarness(
	t testing.TB,
	plan *action.CompiledPlan,
	evaluator *action.Evaluator,
	approvalAuthorities string,
	approvalPolicyID string,
	clientOptions *mcp.ClientOptions,
	callTimeout time.Duration,
) *gatewayLifecycleHarness {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return newGatewayLifecycleHarnessWithCommand(
		t, plan, evaluator, approvalAuthorities, approvalPolicyID,
		clientOptions, callTimeout, executable,
	)
}

func newGatewayLifecycleHarnessWithCommand(
	t testing.TB,
	plan *action.CompiledPlan,
	evaluator *action.Evaluator,
	approvalAuthorities string,
	approvalPolicyID string,
	clientOptions *mcp.ClientOptions,
	callTimeout time.Duration,
	executable string,
) *gatewayLifecycleHarness {
	t.Helper()
	return newGatewayLifecycleHarnessWithAuthority(
		t, plan, evaluator, approvalAuthorities, approvalPolicyID, clientOptions,
		callTimeout, executable, actionstate.PolicyAuthority{
			Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: strings.Repeat("b", 64),
		},
	)
}

func newGatewayLifecycleHarnessWithAuthority(
	t testing.TB,
	plan *action.CompiledPlan,
	evaluator *action.Evaluator,
	approvalAuthorities string,
	approvalPolicyID string,
	clientOptions *mcp.ClientOptions,
	callTimeout time.Duration,
	executable string,
	authority actionstate.PolicyAuthority,
) *gatewayLifecycleHarness {
	t.Helper()
	repository, err := pathidentity.ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := newPrivateGatewayHome(t)
	loader := staticPolicyLoader{snapshot: PolicySnapshot{
		Repository: repository, Evaluator: evaluator, Plan: plan,
		SourceDigest: strings.Repeat("a", 64), LockDigest: strings.Repeat("b", 64),
	}}
	clientReader, gatewayWriter := io.Pipe()
	gatewayReader, clientWriter := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	diagnostics := &bytes.Buffer{}
	inheritedEnvironment := gatewayFakeInheritedEnvironment(
		fakeCancellationMarkerEnvironment, fakeMarkerEnvironment,
		fakeModeEnvironment, fakeProcessEnvironment,
	)
	gateway, err := startGateway(ctx, Config{
		Repository: repository, ServerLabel: "fake", Principal: "test-operator",
		PolicyAuthority: authority,
		Command:         executable, Arguments: []string{"-test.run=^TestMCPGatewayFakeProcess$"},
		InheritedEnvNames: inheritedEnvironment,
		ReconcHome:        home, Version: "test", CallTimeout: callTimeout,
		ApprovalAuthorities: approvalAuthorities, ApprovalPolicyID: approvalPolicyID,
		Input: gatewayReader, Output: gatewayWriter, Diagnostics: diagnostics,
		PolicyLoader: loader,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	harness := &gatewayLifecycleHarness{
		gateway: gateway, cancel: cancel, serveDone: make(chan error, 1), diagnostics: diagnostics,
		home: home, repository: repository,
	}
	go func() {
		serveErr := gateway.serve()
		_ = gatewayReader.CloseWithError(serveErr)
		_ = gatewayWriter.CloseWithError(serveErr)
		harness.serveDone <- serveErr
	}()
	client := mcp.NewClient(
		&mcp.Implementation{Name: "gateway-lifecycle-test", Version: "test"},
		clientOptions,
	)
	harness.session, err = client.Connect(
		ctx, &mcp.IOTransport{Reader: clientReader, Writer: clientWriter}, nil,
	)
	if err != nil {
		_ = harness.Close()
		t.Fatalf("connect lifecycle client: %v; diagnostics=%s", err, diagnostics.String())
	}
	t.Cleanup(func() {
		if err := harness.Close(); err != nil && !t.Failed() {
			t.Errorf("close lifecycle harness: %v; diagnostics=%s", err, diagnostics.String())
		}
	})
	return harness
}

func (h *gatewayLifecycleHarness) Close() error {
	if h == nil {
		return nil
	}
	h.closeOnce.Do(func() {
		h.cancel()
		h.closeErr = errors.Join(h.closeErr, h.gateway.Close())
		if h.session != nil {
			h.closeErr = errors.Join(h.closeErr, closeLifecycleError(h.session.Close()))
		}
		select {
		case err := <-h.serveDone:
			if !isNormalLifecycleError(err) {
				h.closeErr = errors.Join(h.closeErr, err)
			}
		case <-time.After(ShutdownTimeout):
			h.closeErr = errors.Join(h.closeErr, errors.New("gateway serve loop did not terminate"))
		}
	})
	return h.closeErr
}

func TestGatewayCancellationMarksDispatchedBudgetIndeterminate(t *testing.T) {
	markerDirectory := t.TempDir()
	invokedPath := filepath.Join(markerDirectory, "invoked")
	cancelledPath := filepath.Join(markerDirectory, "cancelled")
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, invokedPath)
	t.Setenv(fakeModeEnvironment, "hang")
	t.Setenv(fakeCancellationMarkerEnvironment, cancelledPath)
	plan, evaluator := testGatewayBudgetPlan(t)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	callCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callDone := make(chan error, 1)
	go func() {
		_, err := harness.session.CallTool(callCtx, &mcp.CallToolParams{
			Name: "echo", Arguments: json.RawMessage(`{"value":"hello"}`),
		})
		callDone <- err
	}()
	waitForRegularFile(t, invokedPath)
	cancel()
	select {
	case err := <-callDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CallTool() error = %v, want cancellation", err)
		}
	case <-time.After(ShutdownTimeout + CancellationGrace):
		t.Fatal("CallTool() did not observe cancellation")
	}
	waitForRegularFile(t, cancelledPath)
	deadline := time.Now().Add(2 * time.Second)
	for {
		status, statusErr := harness.gateway.state.Status(context.Background())
		if statusErr == nil && status.LiveReservations == 1 && status.Indeterminate == 1 &&
			len(status.Reservations) == 1 &&
			status.Reservations[0].Status == actionstate.ReservationIndeterminate {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("cancelled call state = %#v, %v", status, statusErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGatewayChildExitAndOversizedResultRemainUnknownAndPrivate(t *testing.T) {
	for _, mode := range []string{"exit-on-call", "oversized-result"} {
		t.Run(mode, func(t *testing.T) {
			markerDirectory := t.TempDir()
			t.Setenv(fakeProcessEnvironment, "1")
			t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
			t.Setenv(fakeModeEnvironment, mode)
			t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
			plan, evaluator := testGatewayBudgetPlan(t)
			harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 30*time.Second)
			result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "echo", Arguments: json.RawMessage(`{"value":"failure"}`),
			})
			if err != nil {
				t.Fatalf("CallTool(): %v", err)
			}
			body, err := json.Marshal(result)
			if err != nil || result == nil || !result.IsError || len(body) > 4096 ||
				!bytes.Contains(body, []byte(action.ReasonDownstreamUnknown)) ||
				bytes.Contains(body, []byte("private-oversized-result")) {
				t.Fatalf("unknown downstream result = %d bytes, %s, %v", len(body), body, err)
			}
			status, err := harness.gateway.state.Status(context.Background())
			if err != nil || status.LiveReservations != 1 || status.Indeterminate != 1 ||
				len(status.Reservations) != 1 ||
				status.Reservations[0].Status != actionstate.ReservationIndeterminate {
				t.Fatalf("unknown downstream state = %#v, %v", status, err)
			}
			closeErr := harness.Close()
			if closeErr == nil || !strings.Contains(closeErr.Error(), "downstream MCP") ||
				strings.Contains(closeErr.Error(), "private-oversized-result") {
				t.Fatalf("unknown downstream lifecycle error = %v", closeErr)
			}
			harness.closeErr = nil
		})
	}
}

func TestGatewayBoundsAndRedactsRealChildStderrFlood(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "stderr-flood-exit")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"failure"}`),
	})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("stderr-flood call = %#v, %v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || bytes.Contains(encoded, []byte(fakePrivateStderrValue)) ||
		!bytes.Contains(encoded, []byte(action.ReasonDownstreamUnknown)) {
		t.Fatalf("stderr-flood response = %s, %v", encoded, err)
	}
	wantBytes := len(fakePrivateStderrValue) + MaxStderrBytes*2
	diagnostics := harness.diagnostics.String()
	fields := strings.Fields(diagnostics)
	var totalBytes, retainedBytes int
	if len(fields) == 6 {
		totalBytes, _ = strconv.Atoi(strings.TrimPrefix(fields[3], "bytes="))
		retainedBytes, _ = strconv.Atoi(strings.TrimPrefix(fields[4], "retained="))
	}
	if strings.Contains(diagnostics, fakePrivateStderrValue) ||
		totalBytes < wantBytes || retainedBytes != MaxStderrBytes || len(fields) != 6 ||
		fields[5] != "classifications=secret" {
		t.Fatalf("bounded stderr diagnostics = %q", diagnostics)
	}
	closeErr := harness.Close()
	if closeErr == nil || strings.Contains(closeErr.Error(), fakePrivateStderrValue) {
		t.Fatalf("stderr-flood lifecycle error = %v", closeErr)
	}
	harness.closeErr = nil
}

func TestGatewayEnforcesConcurrentBudgetBeforeDispatch(t *testing.T) {
	markerDirectory := t.TempDir()
	invokedPath := filepath.Join(markerDirectory, "invoked")
	countPath := filepath.Join(markerDirectory, "count")
	cancelledPath := filepath.Join(markerDirectory, "cancelled")
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, invokedPath)
	t.Setenv(fakeModeEnvironment, "hang")
	t.Setenv(fakeCancellationMarkerEnvironment, cancelledPath)
	t.Setenv(fakeInvocationCountEnvironment, countPath)
	plan, evaluator := testGatewayBudgetPlan(t)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	type callOutcome struct {
		result *mcp.CallToolResult
		err    error
	}
	firstDone := make(chan callOutcome, 1)
	go func() {
		result, err := harness.session.CallTool(firstCtx, &mcp.CallToolParams{
			Name: "echo", Arguments: json.RawMessage(`{"value":"first"}`),
		})
		firstDone <- callOutcome{result: result, err: err}
	}()
	waitForRegularFile(t, countPath)
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSecond()
	second, err := harness.session.CallTool(secondCtx, &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"second"}`),
	})
	if err != nil {
		t.Fatalf("second CallTool(): %v", err)
	}
	if second == nil || !second.IsError {
		t.Fatalf("concurrent budget result = %#v", second)
	}
	count, err := os.ReadFile(countPath)
	if err != nil || string(count) != "1\n" {
		t.Fatalf("downstream invocation count = %q, %v", count, err)
	}
	cancelFirst()
	select {
	case outcome := <-firstDone:
		if outcome.err == nil && (outcome.result == nil || !outcome.result.IsError) {
			t.Fatalf("cancelled first call = %#v, %v", outcome.result, outcome.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first call did not observe cancellation")
	}
}

func TestGatewayShutdownCancelsActiveDownstreamCall(t *testing.T) {
	markerDirectory := t.TempDir()
	invokedPath := filepath.Join(markerDirectory, "invoked")
	cancelledPath := filepath.Join(markerDirectory, "cancelled")
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, invokedPath)
	t.Setenv(fakeModeEnvironment, "hang")
	t.Setenv(fakeCancellationMarkerEnvironment, cancelledPath)
	plan, evaluator := testGatewayBudgetPlan(t)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	callDone := make(chan error, 1)
	go func() {
		_, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "echo", Arguments: json.RawMessage(`{"value":"shutdown"}`),
		})
		callDone <- err
	}()
	waitForRegularFile(t, invokedPath)
	closeDone := make(chan error, 1)
	go func() { closeDone <- harness.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("gateway shutdown: %v", err)
		}
	case <-time.After(ShutdownTimeout + CancellationGrace):
		t.Fatal("gateway shutdown exceeded its lifecycle bound")
	}
	waitForRegularFile(t, cancelledPath)
	select {
	case <-callDone:
	case <-time.After(CancellationGrace):
		t.Fatal("active call did not terminate during gateway shutdown")
	}
}

func TestGatewayRejectsExecutableReplacementBeforeDispatch(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	extension := filepath.Ext(source)
	executable := filepath.Join(markerDirectory, "fake-server"+extension)
	copyExecutable(t, source, executable)
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newGatewayLifecycleHarnessWithCommand(
		t, plan, evaluator, "", "", nil, 5*time.Second, executable,
	)
	contract, generation, exists := harness.gateway.tool("echo")
	if !exists {
		t.Fatal("echo tool contract is unavailable")
	}
	replacement := filepath.Join(markerDirectory, "replacement"+extension)
	copyExecutable(t, source, replacement)
	file, err := os.OpenFile(replacement, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("replacement")); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, executable); err != nil {
		if runtime.GOOS != "windows" || !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		if err := harness.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, executable); err != nil {
			t.Fatalf("replace stopped Windows executable: %v", err)
		}
		if err := harness.gateway.resampleCallBoundary(
			context.Background(), harness.gateway.snapshot, contract, generation, nil,
		); err == nil || !strings.Contains(err.Error(), "server identity changed") {
			t.Fatalf("stopped Windows executable replacement boundary = %v", err)
		}
		if _, err := os.Stat(filepath.Join(markerDirectory, "invoked")); !os.IsNotExist(err) {
			t.Fatalf("replaced stopped Windows executable reached downstream: %v", err)
		}
		return
	}
	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"blocked"}`),
	})
	if err != nil {
		t.Fatalf("CallTool(): %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("executable replacement result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(markerDirectory, "invoked")); !os.IsNotExist(err) {
		t.Fatalf("replaced executable path reached downstream: %v", err)
	}
}

func TestGatewayRejectsPinnedPolicySourceDriftBeforeDispatch(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	drifted := harness.gateway.snapshot
	drifted.SourceDigest = strings.Repeat("c", 64)
	harness.gateway.config.PolicyLoader = staticPolicyLoader{snapshot: drifted}
	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"blocked"}`),
	})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("pinned policy drift result = %#v, %v", result, err)
	}
	body, err := json.Marshal(result.StructuredContent)
	if err != nil || !bytes.Contains(body, []byte(action.ReasonPolicyStale)) {
		t.Fatalf("pinned policy drift evidence = %s, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(markerDirectory, "invoked")); !os.IsNotExist(err) {
		t.Fatalf("pinned policy drift reached downstream: %v", err)
	}
}

func TestGatewayRepositoryManagedModeAcceptsFreshPolicyIdentity(t *testing.T) {
	markerDirectory := t.TempDir()
	marker := filepath.Join(markerDirectory, "invoked")
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, marker)
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	harness := newGatewayLifecycleHarnessWithAuthority(
		t, plan, evaluator, "", "", nil, 5*time.Second, executable,
		actionstate.PolicyAuthority{Mode: action.AuthorityRepositoryManaged},
	)
	if diagnostics := harness.diagnostics.String(); diagnostics != repositoryManagedAuthorityDiagnostic+"\n" {
		t.Fatalf("repository-managed authority diagnostic = %q", diagnostics)
	}
	fresh := harness.gateway.snapshot
	fresh.SourceDigest = strings.Repeat("c", 64)
	fresh.LockDigest = strings.Repeat("d", 64)
	harness.gateway.config.PolicyLoader = staticPolicyLoader{snapshot: fresh}
	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"fresh"}`),
	})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("repository-managed fresh-policy result = %#v, %v", result, err)
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "invoked\n" {
		t.Fatalf("repository-managed downstream marker = %q, %v", body, err)
	}
}

func TestGatewayRecordsAndVerifiesRequiredLedgerLifecycleEndToEnd(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	plan, evaluator := testGatewayLedgerPlan(t)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"recorded"}`),
	})
	if err != nil || result == nil || result.IsError {
		recorded, report, ledgerErr := harness.gateway.ledger.Snapshot(context.Background())
		events := make([]actionledger.EventType, len(recorded))
		for index := range recorded {
			events[index] = recorded[index].Event
		}
		t.Fatalf("required-ledger CallTool() = %#v, %v; events=%v; report=%#v; ledger=%v; diagnostics=%s", result, err, events, report, ledgerErr, harness.diagnostics.String())
	}
	records, report, err := harness.gateway.ledger.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("verify required ledger: %v", err)
	}
	wantEvents := []actionledger.EventType{
		actionledger.EventRequestAccepted,
		actionledger.EventPreDecision,
		actionledger.EventDownstreamDispatch,
		actionledger.EventDownstreamOutcome,
		actionledger.EventResultInspection,
		actionledger.EventFinalDelivery,
	}
	if report.Integrity != actionledger.StatusVerified || !report.EventsEvaluated ||
		!report.EventsComplete || !report.CallsEvaluated || !report.CallsComplete ||
		report.RecordCount != uint64(len(wantEvents)) || len(records) != len(wantEvents) {
		t.Fatalf("required ledger report = %#v; records=%d", report, len(records))
	}
	callID := records[0].Call.CallID
	for index, want := range wantEvents {
		if records[index].Event != want || records[index].Call.CallID != callID ||
			records[index].Call.ToolContractDigest == "" || records[index].Call.LockDigest == "" {
			t.Fatalf("required ledger record %d = %#v, want event %q and one bound call", index, records[index], want)
		}
	}
	if records[len(records)-1].Delivery == nil ||
		records[len(records)-1].Delivery.Status != actionledger.DeliveryForwarded {
		t.Fatalf("required ledger terminal delivery = %#v", records[len(records)-1])
	}
}

func TestGatewayTerminalizesApprovedReservationWhenRequiredLedgerFails(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	seed := bytes.Repeat([]byte{0x43}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, _ := testGatewayApprovalPlan(t, action.PhasePreCall)
	source := plan.Plan()
	source.Ledger.Mode = action.LedgerRequired
	plan, err := action.CompilePlan(source)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(plan)
	if err != nil {
		t.Fatal(err)
	}
	elicitationStarted := make(chan struct{})
	releaseElicitation := make(chan struct{})
	originalHandler := approvalClientOptions(privateKey).ElicitationHandler
	options := &mcp.ClientOptions{ElicitationHandler: func(
		ctx context.Context,
		request *mcp.ElicitRequest,
	) (*mcp.ElicitResult, error) {
		close(elicitationStarted)
		<-releaseElicitation
		return originalHandler(ctx, request)
	}}
	harness := newGatewayLifecycleHarness(
		t, plan, evaluator, registry, "post-result-policy", options, 5*time.Second,
	)
	type callOutcome struct {
		result *mcp.CallToolResult
		err    error
	}
	done := make(chan callOutcome, 1)
	go func() {
		result, callErr := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "echo", Arguments: json.RawMessage(`{"value":"ledger-failure"}`),
		})
		done <- callOutcome{result: result, err: callErr}
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
	for _, pending := range harness.gateway.pending {
		pending.ledger.store = nil
	}
	harness.gateway.pendingMu.Unlock()
	close(releaseElicitation)
	select {
	case outcome := <-done:
		body, marshalErr := json.Marshal(outcome.result)
		if outcome.err != nil || outcome.result == nil || !outcome.result.IsError ||
			marshalErr != nil || !bytes.Contains(body, []byte(action.ReasonLedgerUnavailable)) {
			t.Fatalf("required-ledger failure result = %#v, %v; body=%s, marshal=%v", outcome.result, outcome.err, body, marshalErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("required-ledger failure call did not terminate")
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil || status.LiveReservations != 0 || status.PendingApprovals != 0 ||
		status.TerminalCallCount != 1 || len(status.ApprovalRecords) != 1 ||
		status.ApprovalRecords[0].Status != actionapproval.StatusApproved {
		t.Fatalf("required-ledger failure state = %#v, %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(markerDirectory, "invoked")); !os.IsNotExist(err) {
		t.Fatalf("required-ledger failure reached downstream: %v", err)
	}
}

func TestGatewayBoundsPendingApprovalsAndFinalizesEveryRequest(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	seed := bytes.Repeat([]byte{0x42}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayPendingApprovalPlan(t)
	elicitationStarted := make(chan struct{}, MaxPendingApprovals)
	options := &mcp.ClientOptions{
		ElicitationHandler: func(ctx context.Context, _ *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			elicitationStarted <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	harness := newGatewayLifecycleHarness(
		t, plan, evaluator, registry, "post-result-policy", options, 5*time.Second,
	)
	callsCtx, cancelCalls := context.WithCancel(context.Background())
	type approvalOutcome struct {
		result *mcp.CallToolResult
		err    error
	}
	outcomes := make(chan approvalOutcome, MaxPendingApprovals+1)
	for index := 0; index < MaxPendingApprovals+1; index++ {
		go func(call int) {
			result, err := harness.session.CallTool(callsCtx, &mcp.CallToolParams{
				Name: "echo", Arguments: json.RawMessage(`{"value":"pending"}`),
			})
			outcomes <- approvalOutcome{result: result, err: err}
		}(index)
	}
	for index := 0; index < MaxPendingApprovals; index++ {
		select {
		case <-elicitationStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("pending approval capacity was not reached")
		}
	}
	select {
	case outcome := <-outcomes:
		if outcome.err != nil || outcome.result == nil || !outcome.result.IsError {
			t.Fatalf("overflow approval call = %#v, %v", outcome.result, outcome.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overflow approval call did not fail closed")
	}
	if _, err := os.Stat(filepath.Join(markerDirectory, "invoked")); !os.IsNotExist(err) {
		t.Fatalf("approval overflow reached downstream: %v", err)
	}
	cancelCalls()
	if err := harness.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := harness.persistedStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 0 || status.LiveReservations != 0 {
		t.Fatalf("approval shutdown state = pending %d, live %d", status.PendingApprovals, status.LiveReservations)
	}
}

func TestGatewayPersistsExactClientApprovalRefusal(t *testing.T) {
	for _, test := range []struct {
		name   string
		action string
		status actionapproval.Status
		reason action.ReasonCode
	}{
		{name: "decline", action: "decline", status: actionapproval.StatusCancelled, reason: action.ReasonApprovalRejected},
		{name: "cancel", action: "cancel", status: actionapproval.StatusCancelled, reason: action.ReasonCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			markerDirectory := t.TempDir()
			t.Setenv(fakeProcessEnvironment, "1")
			t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
			t.Setenv(fakeModeEnvironment, "normal")
			t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
			seed := bytes.Repeat([]byte{0x41}, ed25519.SeedSize)
			privateKey := ed25519.NewKeyFromSeed(seed)
			registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
			plan, evaluator := testGatewayApprovalPlan(t, action.PhasePreCall)
			options := &mcp.ClientOptions{ElicitationHandler: func(
				context.Context,
				*mcp.ElicitRequest,
			) (*mcp.ElicitResult, error) {
				return &mcp.ElicitResult{Action: test.action}, nil
			}}
			harness := newGatewayLifecycleHarness(
				t, plan, evaluator, registry, "post-result-policy", options, 5*time.Second,
			)
			result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "echo", Arguments: json.RawMessage(`{"value":"refused"}`),
			})
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("refused approval result = %#v, %v", result, err)
			}
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil || !bytes.Contains(encoded, []byte(`"reason_code":"`+test.reason+`"`)) {
				t.Fatalf("refused approval response = %s, %v", encoded, err)
			}
			if _, err := os.Stat(filepath.Join(markerDirectory, "invoked")); !os.IsNotExist(err) {
				t.Fatalf("refused approval reached downstream: %v", err)
			}
			status, err := harness.gateway.state.Status(context.Background())
			if err != nil || status.LiveReservations != 0 || status.PendingApprovals != 0 ||
				len(status.ApprovalRecords) != 1 || status.ApprovalRecords[0].Status != test.status {
				t.Fatalf("refused approval state = %#v, %v", status, err)
			}
		})
	}
}

func TestGatewayPersistsSignedAuthorityRejection(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	seed := bytes.Repeat([]byte{0x42}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlan(t, action.PhasePreCall)
	harness := newGatewayLifecycleHarness(
		t, plan, evaluator, registry, "post-result-policy",
		approvalClientOptionsForDecision(privateKey, actionapproval.DecisionReject),
		5*time.Second,
	)
	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"reject"}`),
	})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("signed rejection result = %#v, %v", result, err)
	}
	body, err := json.Marshal(result.StructuredContent)
	if err != nil || !bytes.Contains(body, []byte(action.ReasonApprovalRejected)) {
		t.Fatalf("signed rejection response = %s, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(markerDirectory, "invoked")); !os.IsNotExist(err) {
		t.Fatalf("signed rejection reached downstream: %v", err)
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil || status.LiveReservations != 0 || status.PendingApprovals != 0 ||
		len(status.ApprovalRecords) != 1 ||
		status.ApprovalRecords[0].Status != actionapproval.StatusRejected ||
		status.ApprovalRecords[0].ReceiptID == "" {
		t.Fatalf("signed rejection state = %#v, %v", status, err)
	}
}

func TestGatewayRefreshesChangedToolContractEndToEnd(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "tool-change")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	changed := make(chan struct{}, 4)
	options := &mcp.ClientOptions{ToolListChangedHandler: func(
		context.Context,
		*mcp.ToolListChangedRequest,
	) {
		changed <- struct{}{}
	}}
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", options, 5*time.Second)
	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"first"}`),
	})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("first CallTool() = %#v, %v", result, err)
	}
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream tool-list change notification was not forwarded")
	}
	listed, err := harness.session.ListTools(context.Background(), nil)
	if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "echo" ||
		listed.Tools[0].Description != "Changed echo tool." {
		t.Fatalf("refreshed tools = %#v, %v", listed, err)
	}
	result, err = harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"second"}`),
	})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("second CallTool() = %#v, %v", result, err)
	}
}

func TestGatewayToolRefreshCannotResetCumulativeBudget(t *testing.T) {
	markerDirectory := t.TempDir()
	countPath := filepath.Join(markerDirectory, "count")
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "tool-change")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	t.Setenv(fakeInvocationCountEnvironment, countPath)
	changed := make(chan struct{}, 1)
	options := &mcp.ClientOptions{ToolListChangedHandler: func(
		context.Context,
		*mcp.ToolListChangedRequest,
	) {
		changed <- struct{}{}
	}}
	plan, evaluator := testGatewayBudgetPlanWithLimits(
		t, action.BudgetLimits{CallCount: 1},
	)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", options, 5*time.Second)
	first, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"first"}`),
	})
	if err != nil || first == nil || first.IsError {
		t.Fatalf("first budgeted call = %#v, %v", first, err)
	}
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("tool refresh did not reach the upstream client")
	}
	second, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"second"}`),
	})
	if err != nil || second == nil || !second.IsError {
		t.Fatalf("second budgeted call = %#v, %v", second, err)
	}
	body, err := json.Marshal(second.StructuredContent)
	if err != nil || !bytes.Contains(body, []byte(action.ReasonBudgetExhausted)) {
		t.Fatalf("exhausted budget response = %s, %v", body, err)
	}
	if count, err := os.ReadFile(countPath); err != nil || string(count) != "1\n" {
		t.Fatalf("downstream invocation count after refresh = %q, %v", count, err)
	}
}

func TestGatewayToolRefreshInvalidatesCachedContractDecision(t *testing.T) {
	markerDirectory := t.TempDir()
	countPath := filepath.Join(markerDirectory, "count")
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "tool-change")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	t.Setenv(fakeInvocationCountEnvironment, countPath)
	initialContract := testEchoContract(t, "Echo one value.")
	changedContract := testEchoContract(t, "Changed echo tool.")
	plan, evaluator := testGatewayContractBlockPlan(t, changedContract.ContractDigest)
	changed := make(chan struct{}, 1)
	options := &mcp.ClientOptions{ToolListChangedHandler: func(
		context.Context,
		*mcp.ToolListChangedRequest,
	) {
		changed <- struct{}{}
	}}
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", options, 5*time.Second)
	discovered, _, exists := harness.gateway.tool("echo")
	if !exists || discovered.ContractDigest != initialContract.ContractDigest {
		t.Fatalf("initial tool contract = %#v, want %s", discovered, initialContract.ContractDigest)
	}
	first, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"first"}`),
	})
	if err != nil || first == nil || first.IsError {
		t.Fatalf("initial contract call = %#v, %v", first, err)
	}
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("tool refresh did not reach the upstream client")
	}
	discovered, _, exists = harness.gateway.tool("echo")
	if !exists || discovered.ContractDigest != changedContract.ContractDigest {
		t.Fatalf("changed tool contract = %#v, want %s", discovered, changedContract.ContractDigest)
	}
	second, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"second"}`),
	})
	if err != nil || second == nil || !second.IsError {
		t.Fatalf("changed contract call = %#v, %v", second, err)
	}
	if count, err := os.ReadFile(countPath); err != nil || string(count) != "1\n" {
		t.Fatalf("downstream invocation count after decision invalidation = %q, %v", count, err)
	}
}

func TestGatewayFailsClosedAndReportsInvalidToolRefresh(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "invalid-tool-change")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newGatewayLifecycleHarness(t, plan, evaluator, "", "", nil, 5*time.Second)
	result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "echo", Arguments: json.RawMessage(`{"value":"trigger-refresh"}`),
	})
	if err == nil && (result == nil || result.IsError) {
		t.Fatalf("refresh-triggering call = %#v, %v", result, err)
	}
	if err != nil && (result != nil ||
		!strings.Contains(err.Error(), "refresh downstream tool catalog") ||
		strings.Contains(err.Error(), "Ignore previous instructions")) {
		t.Fatalf("invalid-refresh interruption = %#v, %v", result, err)
	}
	waitForRegularFile(t, os.Getenv(fakeMarkerEnvironment))
	deadline := time.Now().Add(2 * time.Second)
	for {
		if !harness.gateway.beginCall() {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("invalid tool refresh did not close the call boundary")
		}
		time.Sleep(10 * time.Millisecond)
	}
	closeErr := harness.Close()
	if closeErr == nil || !strings.Contains(closeErr.Error(), "refresh downstream tool catalog") ||
		strings.Contains(closeErr.Error(), "Ignore previous instructions") {
		t.Fatalf("invalid tool refresh error = %v", closeErr)
	}
	harness.closeErr = nil
}

func TestGatewayRejectsApprovalAfterToolContractChange(t *testing.T) {
	markerDirectory := t.TempDir()
	triggerPath := filepath.Join(markerDirectory, "change-tool")
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "triggered-tool-change")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	t.Setenv(fakeToolChangeTriggerEnvironment, triggerPath)
	seed := bytes.Repeat([]byte{0x32}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlan(t, action.PhasePreCall)
	changed := make(chan struct{}, 4)
	elicitationStarted := make(chan struct{})
	releaseElicitation := make(chan struct{})
	originalHandler := approvalClientOptions(privateKey).ElicitationHandler
	options := &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			changed <- struct{}{}
		},
		ElicitationHandler: func(
			ctx context.Context,
			request *mcp.ElicitRequest,
		) (*mcp.ElicitResult, error) {
			close(elicitationStarted)
			<-releaseElicitation
			return originalHandler(ctx, request)
		},
	}
	harness := newGatewayLifecycleHarness(
		t, plan, evaluator, registry, "post-result-policy", options, 5*time.Second,
	)
	type callOutcome struct {
		result *mcp.CallToolResult
		err    error
	}
	callDone := make(chan callOutcome, 1)
	go func() {
		result, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "echo", Arguments: json.RawMessage(`{"value":"hello"}`),
		})
		callDone <- callOutcome{result: result, err: err}
	}()
	select {
	case <-elicitationStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("approval elicitation did not start")
	}
	if err := os.WriteFile(triggerPath, []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("changed tool contract was not published upstream")
	}
	close(releaseElicitation)
	select {
	case outcome := <-callDone:
		if outcome.err != nil || outcome.result == nil || !outcome.result.IsError {
			t.Fatalf("stale approval result = %#v, %v", outcome.result, outcome.err)
		}
		body, err := json.Marshal(outcome.result.StructuredContent)
		if err != nil || !bytes.Contains(body, []byte(action.ReasonToolContractStale)) {
			t.Fatalf("stale approval evidence = %s, %v", body, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stale approval call did not terminate")
	}
	if _, err := os.Stat(os.Getenv(fakeMarkerEnvironment)); !os.IsNotExist(err) {
		t.Fatalf("stale approval reached downstream: %v", err)
	}
}

func TestGatewayShutdownFinalizesPendingApproval(t *testing.T) {
	for _, phase := range []action.Phase{action.PhasePreCall, action.PhasePostResult} {
		t.Run(string(phase), func(t *testing.T) {
			testGatewayShutdownFinalizesPendingApprovalPhase(t, phase)
		})
	}
}

func testGatewayShutdownFinalizesPendingApprovalPhase(t *testing.T, phase action.Phase) {
	t.Helper()
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	seed := bytes.Repeat([]byte{0x31}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
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
	callDone := make(chan error, 1)
	go func() {
		_, err := harness.session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "echo", Arguments: json.RawMessage(`{"value":"hello"}`),
		})
		callDone <- err
	}()
	select {
	case <-elicitationStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("approval elicitation did not start")
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil || status.PendingApprovals != 1 || status.LiveReservations != 1 {
		t.Fatalf("pending approval state = %#v, %v", status, err)
	}
	if err := harness.Close(); err != nil {
		t.Fatalf("gateway shutdown: %v", err)
	}
	status, err = harness.persistedStatus()
	if err != nil || status.PendingApprovals != 0 || status.LiveReservations != 0 ||
		len(status.ApprovalRecords) != 1 ||
		status.ApprovalRecords[0].Status != actionapproval.StatusCancelled {
		t.Fatalf("shutdown approval state = %#v, %v", status, err)
	}
	close(releaseElicitation)
	select {
	case <-callDone:
	case <-time.After(2 * time.Second):
		t.Fatal("approval call did not terminate after shutdown")
	}
}

func (h *gatewayLifecycleHarness) persistedStatus() (actionstate.StateStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lease, err := actionstate.AcquireExistingIdentityKey(ctx, h.home)
	if err != nil {
		return actionstate.StateStatus{}, err
	}
	defer lease.Close()
	store, err := actionstate.OpenStore(actionstate.StoreOptions{
		Home: h.home, Repository: h.repository, KeyLease: lease,
	})
	if err != nil {
		return actionstate.StateStatus{}, err
	}
	return store.Status(ctx)
}

func waitForRegularFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(ShutdownTimeout + CancellationGrace)
	for {
		info, err := os.Stat(path)
		if err == nil {
			if !info.Mode().IsRegular() {
				t.Fatalf("%s is not a regular file", path)
			}
			return
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func copyExecutable(t *testing.T, source, destination string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func testGatewayBudgetPlan(t *testing.T) (*action.CompiledPlan, *action.Evaluator) {
	return testGatewayBudgetPlanWithLimits(
		t, action.BudgetLimits{CallCount: 2, Concurrent: 1},
	)
}

func testGatewayBudgetPlanWithLimits(
	t *testing.T,
	limits action.BudgetLimits,
) (*action.CompiledPlan, *action.Evaluator) {
	t.Helper()
	compiled, err := action.CompilePlan(action.Plan{
		Tools: []action.Tool{{
			ID: "echo-tool", Transport: action.TransportMCPStdio,
			ServerLabel: "fake", Tool: "echo", Effect: action.Effect{Kind: action.EffectExternal},
			Origin: action.OriginActions, SourceIdentity: "test-policy",
		}},
		Budgets: []action.Budget{{
			ID: "echo-calls", Selector: action.Selector{ToolIDs: []string{"echo-tool"}},
			Limits: limits,
			Reset:  action.BudgetResetNever, OnExhaustion: action.DecisionBlock,
			SourceIdentity: "test-policy",
		}},
		Rules: []action.Rule{}, Approvals: []action.ApprovalDisclosure{},
		Detectors: []action.DetectorPolicy{}, Defaults: action.FrozenDefaults(),
		Ledger: &action.LedgerPolicy{
			Mode: action.LedgerOff, ToolIdentity: action.LedgerDeclarationID,
			SelectedFields: []action.LedgerField{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(compiled)
	if err != nil {
		t.Fatal(err)
	}
	return compiled, evaluator
}

func testGatewayLedgerPlan(t *testing.T) (*action.CompiledPlan, *action.Evaluator) {
	t.Helper()
	compiled, err := action.CompilePlan(action.Plan{
		Tools: []action.Tool{{
			ID: "echo-tool", Transport: action.TransportMCPStdio,
			ServerLabel: "fake", Tool: "echo", Effect: action.Effect{Kind: action.EffectExternal},
			Origin: action.OriginActions, SourceIdentity: "test-policy",
		}},
		Rules: []action.Rule{}, Budgets: []action.Budget{},
		Approvals: []action.ApprovalDisclosure{}, Detectors: []action.DetectorPolicy{},
		Defaults: action.FrozenDefaults(),
		Ledger: &action.LedgerPolicy{
			Mode: action.LedgerRequired, ToolIdentity: action.LedgerDeclarationID,
			SelectedFields: []action.LedgerField{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(compiled)
	if err != nil {
		t.Fatal(err)
	}
	return compiled, evaluator
}

func testGatewayContractBlockPlan(
	t *testing.T,
	blockedDigest string,
) (*action.CompiledPlan, *action.Evaluator) {
	t.Helper()
	compiled, err := action.CompilePlan(action.Plan{
		Tools: []action.Tool{{
			ID: "echo-tool", Transport: action.TransportMCPStdio,
			ServerLabel: "fake", Tool: "echo", Effect: action.Effect{Kind: action.EffectExternal},
			Origin: action.OriginActions, SourceIdentity: "test-policy",
		}},
		Rules: []action.Rule{{
			ID: "block-changed-contract", Selector: action.Selector{
				ToolIDs: []string{"echo-tool"}, ToolContractDigests: []string{blockedDigest},
				Phases: []action.Phase{action.PhasePreCall},
			},
			Decision: action.DecisionBlock, OnIndeterminate: action.DecisionBlock,
			Cache: action.CacheExact, SourceIdentity: "test-policy",
		}},
		Budgets: []action.Budget{}, Approvals: []action.ApprovalDisclosure{},
		Detectors: []action.DetectorPolicy{}, Defaults: action.FrozenDefaults(),
		Ledger: &action.LedgerPolicy{
			Mode: action.LedgerOff, ToolIdentity: action.LedgerDeclarationID,
			SelectedFields: []action.LedgerField{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(compiled)
	if err != nil {
		t.Fatal(err)
	}
	return compiled, evaluator
}

func testEchoContract(t *testing.T, description string) ToolContract {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name": "echo", "description": description,
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
			"required":   []string{"value"}, "additionalProperties": false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := validateToolContract(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func testGatewayPendingApprovalPlan(t *testing.T) (*action.CompiledPlan, *action.Evaluator) {
	t.Helper()
	compiled, err := action.CompilePlan(action.Plan{
		Tools: []action.Tool{{
			ID: "echo-tool", Transport: action.TransportMCPStdio,
			ServerLabel: "fake", Tool: "echo", Effect: action.Effect{Kind: action.EffectExternal},
			Origin: action.OriginActions, SourceIdentity: "test-policy",
		}},
		Rules: []action.Rule{{
			ID: "approve-echo", Selector: action.Selector{
				ToolIDs: []string{"echo-tool"}, Phases: []action.Phase{action.PhasePreCall},
			},
			Decision: action.DecisionRequireApproval, OnIndeterminate: action.DecisionBlock,
			Cache: action.CacheExact, SourceIdentity: "test-policy",
		}},
		Budgets: []action.Budget{}, Approvals: []action.ApprovalDisclosure{},
		Detectors: []action.DetectorPolicy{}, Defaults: action.FrozenDefaults(),
		Ledger: &action.LedgerPolicy{
			Mode: action.LedgerOff, ToolIdentity: action.LedgerDeclarationID,
			SelectedFields: []action.LedgerField{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := action.NewEvaluator(compiled)
	if err != nil {
		t.Fatal(err)
	}
	return compiled, evaluator
}
