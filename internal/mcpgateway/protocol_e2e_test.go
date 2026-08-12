package mcpgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/pathidentity"
)

type rawGatewayHarness struct {
	gateway   *Gateway
	input     *io.PipeWriter
	output    *bufio.Reader
	outputRaw *io.PipeReader
	cancel    context.CancelFunc
	done      chan error
	closeOnce sync.Once
	closeErr  error
}

type rawGatewayOptions struct {
	repository          string
	home                string
	approvalAuthorities string
	approvalPolicyID    string
}

func newRawGatewayHarness(
	t *testing.T,
	plan *action.CompiledPlan,
	evaluator *action.Evaluator,
) *rawGatewayHarness {
	return newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{})
}

func newRawGatewayHarnessWithOptions(
	t *testing.T,
	plan *action.CompiledPlan,
	evaluator *action.Evaluator,
	options rawGatewayOptions,
) *rawGatewayHarness {
	t.Helper()
	repository := options.repository
	if repository == "" {
		repository = t.TempDir()
	}
	repository, err := pathidentity.ResolveExisting(repository)
	if err != nil {
		t.Fatal(err)
	}
	home := options.home
	createKey := home == ""
	if createKey {
		home = t.TempDir()
	}
	home, err = pathidentity.ResolveExisting(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if createKey {
		_, err = actionstate.CreateIdentityKey(home, time.Unix(1, 0))
	}
	if err != nil {
		t.Fatal(err)
	}
	loader := staticPolicyLoader{snapshot: PolicySnapshot{
		Repository: repository, Plan: plan, Evaluator: evaluator,
		SourceDigest: strings.Repeat("a", 64), LockDigest: strings.Repeat("b", 64),
	}}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gatewayInput, clientInput := io.Pipe()
	clientOutput, gatewayOutput := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	gateway, err := startGateway(ctx, Config{
		Repository: repository, ServerLabel: "fake", Principal: "test-operator",
		PolicyAuthority: actionstate.PolicyAuthority{
			Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: strings.Repeat("b", 64),
		},
		Command: executable, Arguments: []string{"-test.run=^TestMCPGatewayFakeProcess$"},
		InheritedEnvNames: gatewayFakeInheritedEnvironment(
			fakeCancellationMarkerEnvironment, fakeMarkerEnvironment,
			fakeModeEnvironment, fakeProcessEnvironment,
		),
		ReconcHome: home, Version: "test", CallTimeout: 5 * time.Second,
		ApprovalAuthorities: options.approvalAuthorities,
		ApprovalPolicyID:    options.approvalPolicyID,
		Input:               gatewayInput, Output: gatewayOutput, Diagnostics: io.Discard,
		PolicyLoader: loader,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	harness := &rawGatewayHarness{
		gateway: gateway, input: clientInput, output: bufio.NewReader(clientOutput),
		outputRaw: clientOutput, cancel: cancel, done: make(chan error, 1),
	}
	go func() { harness.done <- gateway.serve() }()
	t.Cleanup(func() {
		if err := harness.Close(); err != nil && !t.Failed() {
			t.Errorf("close raw gateway: %v", err)
		}
	})
	return harness
}

func (h *rawGatewayHarness) Close() error {
	if h == nil {
		return nil
	}
	h.closeOnce.Do(func() {
		h.closeErr = errors.Join(h.closeErr, closeLifecycleError(h.input.Close()))
		h.cancel()
		h.closeErr = errors.Join(h.closeErr, h.gateway.Close())
		h.closeErr = errors.Join(h.closeErr, closeLifecycleError(h.outputRaw.Close()))
		select {
		case err := <-h.done:
			if !isNormalLifecycleError(err) {
				h.closeErr = errors.Join(h.closeErr, err)
			}
		case <-time.After(ShutdownTimeout):
			h.closeErr = errors.Join(h.closeErr, errors.New("raw gateway did not terminate"))
		}
	})
	return h.closeErr
}

func (h *rawGatewayHarness) exchange(t *testing.T, request string) rawRPCResponse {
	t.Helper()
	h.notify(t, request)
	return h.readResponse(t)
}

func (h *rawGatewayHarness) readResponse(t *testing.T) rawRPCResponse {
	t.Helper()
	response, err := h.readResponseValue()
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func (h *rawGatewayHarness) readResponseValue() (rawRPCResponse, error) {
	line, err := h.output.ReadBytes('\n')
	if err != nil {
		return rawRPCResponse{}, err
	}
	line = bytes.TrimSuffix(line, []byte{'\n'})
	if err := validateFrameJSON(line); err != nil {
		return rawRPCResponse{}, fmt.Errorf("invalid gateway response: %w", err)
	}
	var response rawRPCResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return rawRPCResponse{}, err
	}
	return response, nil
}

func (h *rawGatewayHarness) notify(t *testing.T, notification string) {
	t.Helper()
	if _, err := io.WriteString(h.input, notification+"\n"); err != nil {
		t.Fatal(err)
	}
}

type rawRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

func TestGatewaySupportsExactLegacyToolProtocolEndToEnd(t *testing.T) {
	markerDirectory := t.TempDir()
	marker := filepath.Join(markerDirectory, "invoked")
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, marker)
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newRawGatewayHarness(t, plan, evaluator)
	initialized := harness.exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy-test","version":"test"}}}`)
	if len(initialized.Error) != 0 {
		t.Fatalf("legacy initialize error = %s", initialized.Error)
	}
	var initialization struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(initialized.Result, &initialization); err != nil ||
		initialization.ProtocolVersion != "2025-11-25" {
		t.Fatalf("legacy initialization = %s, %v", initialized.Result, err)
	}
	harness.notify(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	listed := harness.exchange(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	var catalog struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if len(listed.Error) != 0 || json.Unmarshal(listed.Result, &catalog) != nil ||
		len(catalog.Tools) != 1 || catalog.Tools[0].Name != "echo" {
		t.Fatalf("legacy tools/list = %#v, error=%s", catalog, listed.Error)
	}
	called := harness.exchange(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"value":"legacy"}}}`)
	var result struct {
		IsError bool `json:"isError"`
	}
	if len(called.Error) != 0 || json.Unmarshal(called.Result, &result) != nil || result.IsError {
		t.Fatalf("legacy tools/call = %s, error=%s", called.Result, called.Error)
	}
	waitForRegularFile(t, marker)
}

func TestGatewayRejectsUnadvertisedLegacyProtocol(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newRawGatewayHarness(t, plan, evaluator)
	response := harness.exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"unsupported-test","version":"test"}}}`)
	if len(response.Error) == 0 || bytes.Equal(response.Error, []byte("null")) {
		t.Fatalf("unsupported initialization was accepted: %s", response.Result)
	}
}

func TestGatewayPreservesExactStructuredNumbersOnCurrentProtocol(t *testing.T) {
	markerDirectory := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "structured-precision")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newRawGatewayHarness(t, plan, evaluator)
	initialized := harness.exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"precision-test","version":"test"}}}`)
	if len(initialized.Error) != 0 {
		t.Fatalf("current initialize error = %s", initialized.Error)
	}
	harness.notify(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	called := harness.exchange(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"value":"precision"}}}`)
	if len(called.Error) != 0 {
		t.Fatalf("precision tools/call error = %s", called.Error)
	}
	result, err := action.ParseObjectJSON(called.Result)
	if err != nil {
		t.Fatal(err)
	}
	structured, ok := result.Lookup("structuredContent")
	if !ok {
		t.Fatalf("precision result omitted structuredContent: %s", called.Result)
	}
	integer, ok := structured.Lookup("integer")
	if !ok {
		t.Fatalf("precision result omitted integer: %s", called.Result)
	}
	number, ok := integer.Decimal()
	if !ok || number.String() != "9007199254740993" {
		t.Fatalf("precision integer = %v, present=%t; result=%s", number, ok, called.Result)
	}
}

func TestGatewayPreservesExactCanonicalArgumentsEndToEnd(t *testing.T) {
	markerDirectory := t.TempDir()
	argumentMarker := filepath.Join(markerDirectory, "arguments")
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markerDirectory, "invoked"))
	t.Setenv(fakeModeEnvironment, "argument-precision")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markerDirectory, "cancelled"))
	t.Setenv(fakeArgumentsMarkerEnvironment, argumentMarker)
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newRawGatewayHarness(t, plan, evaluator)
	initialized := harness.exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"argument-test","version":"test"}}}`)
	if len(initialized.Error) != 0 {
		t.Fatalf("current initialize error = %s", initialized.Error)
	}
	harness.notify(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	arguments := []byte(`{"integer":9007199254740993,"decimal":1.2300}`)
	called := harness.exchange(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":`+string(arguments)+`}}`)
	if len(called.Error) != 0 {
		t.Fatalf("argument tools/call error = %s", called.Error)
	}
	waitForRegularFile(t, argumentMarker)
	got, err := os.ReadFile(argumentMarker)
	if err != nil {
		t.Fatal(err)
	}
	value, err := action.ParseObjectJSON(arguments)
	if err != nil {
		t.Fatal(err)
	}
	want, err := value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("downstream arguments = %s, want canonical %s", got, want)
	}
}
