package mcpgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/pathidentity"
)

type rawFixturePaths struct {
	marker       string
	cancellation string
	invocations  string
}

type rawToolResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	IsError           bool                       `json:"isError"`
	StructuredContent json.RawMessage            `json:"structuredContent"`
	ResultType        string                     `json:"resultType"`
	RequestState      string                     `json:"requestState"`
	InputRequests     map[string]rawInputRequest `json:"inputRequests"`
}

type rawInputRequest struct {
	Method string `json:"method"`
	Params struct {
		Message string `json:"message"`
	} `json:"params"`
}

func TestGatewayRawMCPInteroperabilityMatrix(t *testing.T) {
	for _, protocol := range supportedGatewayProtocols {
		t.Run("discovery-and-allow-"+protocol, func(t *testing.T) {
			testRawDiscoveryAndAllow(t, protocol)
		})
		t.Run("structured-result-"+protocol, func(t *testing.T) {
			testRawStructuredResult(t, protocol)
		})
	}
	t.Run("policy-and-error-categories", testRawPolicyAndErrorCategories)
	t.Run("current-approval-and-fresh-session-budget", testRawApprovalAndFreshSessionBudget)
	t.Run("legacy-form-elicitation-approval", testRawLegacyFormApproval)
	t.Run("legacy-two-phase-form-elicitation", testRawLegacyTwoPhaseFormApproval)
	t.Run("legacy-approval-without-elicitation", testRawLegacyApprovalWithoutElicitation)
	t.Run("fresh-session-call-budget", testRawFreshSessionCallBudget)
	t.Run("cancellation", testRawCancellation)
}

func testRawLegacyFormApproval(t *testing.T) {
	paths := prepareRawFixture(t, "normal", true)
	seed := bytes.Repeat([]byte{0x59}, ed25519.SeedSize)
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
	harness := newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{
		approvalAuthorities: registry, approvalPolicyID: "post-result-policy",
	})
	initializeRawGateway(t, harness, gatewayProtocolLegacy)
	harness.notify(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"value":"legacy-approved"}}}`)
	elicitation := harness.readResponse(t)
	if elicitation.Method != "elicitation/create" || len(elicitation.ID) == 0 {
		t.Fatalf("legacy approval elicitation = %#v", elicitation)
	}
	var params struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(elicitation.Params, &params); err != nil || params.Message == "" {
		t.Fatalf("legacy approval params = %s, %v", elicitation.Params, err)
	}
	response, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  struct {
			Action  string            `json:"action"`
			Content map[string]string `json:"content"`
		} `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      elicitation.ID,
		Result: struct {
			Action  string            `json:"action"`
			Content map[string]string `json:"content"`
		}{
			Action: "accept",
			Content: map[string]string{
				"receipt": signRawApproval(t, params.Message, privateKey),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.notify(t, string(response))
	result := decodeRawToolResult(t, harness.readResponse(t))
	if result.IsError || rawResultText(result) != "hello" {
		records, report, ledgerErr := harness.gateway.ledger.Snapshot(context.Background())
		t.Fatalf("legacy approved result = %#v; records=%#v; report=%#v; ledger=%v", result, records, report, ledgerErr)
	}
	assertApprovedLedgerLifecycle(t, harness)
	assertInvocationCount(t, paths.invocations, "1\n")
}

func assertApprovedLedgerLifecycle(t *testing.T, harness *rawGatewayHarness) {
	t.Helper()
	records, report, err := harness.gateway.ledger.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []actionledger.EventType{
		actionledger.EventRequestAccepted,
		actionledger.EventPreDecision,
		actionledger.EventBudgetTransition,
		actionledger.EventApprovalTransition,
		actionledger.EventApprovalTransition,
		actionledger.EventBudgetTransition,
		actionledger.EventDownstreamDispatch,
		actionledger.EventDownstreamOutcome,
		actionledger.EventResultInspection,
		actionledger.EventBudgetTransition,
		actionledger.EventFinalDelivery,
	}
	if report.Integrity != actionledger.StatusVerified || !report.EventsEvaluated ||
		!report.EventsComplete || !report.CallsEvaluated || !report.CallsComplete ||
		report.RecordCount != uint64(len(wantEvents)) || len(records) != len(wantEvents) {
		t.Fatalf("approved required-ledger report = %#v; records=%#v", report, records)
	}
	callID := records[0].Call.CallID
	for index, want := range wantEvents {
		if records[index].Event != want || records[index].Call.CallID != callID {
			t.Fatalf("approved required-ledger event %d = %#v, want %q", index, records[index], want)
		}
	}
}

func testRawLegacyApprovalWithoutElicitation(t *testing.T) {
	paths := prepareRawFixture(t, "normal", false)
	seed := bytes.Repeat([]byte{0x5a}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlan(t, action.PhasePreCall)
	harness := newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{
		approvalAuthorities: registry, approvalPolicyID: "post-result-policy",
	})
	initialized := harness.exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"raw-no-elicitation","version":"1"}}}`)
	if len(initialized.Error) != 0 {
		t.Fatalf("legacy initialization failed: %s", initialized.Error)
	}
	harness.notify(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	result := callRawTool(t, harness, 2, "echo", "unapproved")
	if !result.IsError || !strings.Contains(rawResultText(result), string(action.ReasonApprovalRequired)) {
		t.Fatalf("legacy unsupported approval result = %#v", result)
	}
	assertRawInvocation(t, paths.marker, false)
}

func testRawLegacyTwoPhaseFormApproval(t *testing.T) {
	paths := prepareRawFixture(t, "normal", true)
	seed := bytes.Repeat([]byte{0x5b}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayDoubleApprovalPlan(t)
	harness := newRawGatewayHarnessWithOptions(t, plan, evaluator, rawGatewayOptions{
		approvalAuthorities: registry, approvalPolicyID: "post-result-policy",
	})
	initializeRawGateway(t, harness, gatewayProtocolLegacy)
	harness.notify(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"value":"legacy-two-phase"}}}`)
	for sequence := uint64(1); sequence <= 2; sequence++ {
		elicitation := harness.readResponse(t)
		if elicitation.Method != "elicitation/create" || len(elicitation.ID) == 0 {
			t.Fatalf("legacy approval elicitation %d = %#v", sequence, elicitation)
		}
		var params struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(elicitation.Params, &params); err != nil || params.Message == "" {
			t.Fatalf("legacy approval params %d = %s, %v", sequence, elicitation.Params, err)
		}
		receipt := signRawApprovalSequence(t, params.Message, privateKey, sequence)
		response := fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%s,"result":{"action":"accept","content":{"receipt":%s}}}`,
			elicitation.ID,
			strconv.Quote(receipt),
		)
		harness.notify(t, response)
	}
	result := decodeRawToolResult(t, harness.readResponse(t))
	if result.IsError || rawResultText(result) != "hello" {
		t.Fatalf("legacy two-phase approved result = %#v", result)
	}
	assertInvocationCount(t, paths.invocations, "1\n")
}

func testRawDiscoveryAndAllow(t *testing.T, protocol string) {
	paths := prepareRawFixture(t, "normal", false)
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newRawGatewayHarness(t, plan, evaluator)
	initializeRawGateway(t, harness, protocol)
	response := harness.exchange(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	assertRawToolCatalog(t, response)
	result := callRawTool(t, harness, 3, "echo", "allowed")
	if result.IsError || rawResultText(result) != "hello" {
		t.Fatalf("allowed result = %#v", result)
	}
	waitForRegularFile(t, paths.marker)
}

func testRawStructuredResult(t *testing.T, protocol string) {
	prepareRawFixture(t, "structured-precision", false)
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newRawGatewayHarness(t, plan, evaluator)
	initializeRawGateway(t, harness, protocol)
	result := callRawTool(t, harness, 2, "echo", "structured")
	value, err := action.ParseObjectJSON(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	integer, ok := value.Lookup("integer")
	number, numberOK := integer.Decimal()
	if result.IsError || !ok || !numberOK || number.String() != "9007199254740993" {
		t.Fatalf("structured result = %s", result.StructuredContent)
	}
}

func testRawPolicyAndErrorCategories(t *testing.T) {
	tests := []struct {
		name, mode string
		decision   action.Decision
		postBlock  bool
		wantError  bool
		wantText   string
		invoked    bool
	}{
		{name: "warn", mode: "normal", decision: action.DecisionWarn, wantText: "hello", invoked: true},
		{name: "block", mode: "normal", decision: action.DecisionBlock, wantError: true, wantText: "rule_matched"},
		{name: "tool-error", mode: "tool-error", decision: action.DecisionAllow, wantError: true, wantText: "downstream-tool-error", invoked: true},
		{name: "withheld", mode: "sensitive-result", decision: action.DecisionAllow, postBlock: true, wantError: true, wantText: "withheld", invoked: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { runRawPolicyCase(t, test) })
	}
}

func runRawPolicyCase(t *testing.T, test struct {
	name, mode string
	decision   action.Decision
	postBlock  bool
	wantError  bool
	wantText   string
	invoked    bool
}) {
	paths := prepareRawFixture(t, test.mode, false)
	plan, evaluator := testGatewayPlan(t, test.decision)
	if test.postBlock {
		plan, evaluator = testGatewayPostBlockPlan(t)
	}
	harness := newRawGatewayHarness(t, plan, evaluator)
	initializeRawGateway(t, harness, gatewayProtocolCurrent)
	result := callRawTool(t, harness, 2, "echo", test.name)
	text := rawResultText(result)
	if result.IsError != test.wantError || !strings.Contains(text, test.wantText) ||
		strings.Contains(text, "private-result-value") {
		t.Fatalf("%s result = %#v", test.name, result)
	}
	assertRawInvocation(t, paths.marker, test.invoked)
}

func testRawApprovalAndFreshSessionBudget(t *testing.T) {
	t.Run("receipt-replay", testRawFreshSessionApprovalReplay)
	t.Run("budget", testRawFreshSessionApprovalBudget)
}

func testRawFreshSessionApprovalReplay(t *testing.T) {
	paths := prepareRawFixture(t, "normal", true)
	repository, home := newRawPersistentState(t)
	seed := bytes.Repeat([]byte{0x57}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlanWithLimits(
		t,
		action.PhasePreCall,
		action.BudgetLimits{CallCount: 2, ApprovalCount: 2},
	)
	options := rawGatewayOptions{
		repository: repository, home: home,
		approvalAuthorities: registry, approvalPolicyID: "post-result-policy",
	}
	first := newRawGatewayHarnessWithOptions(t, plan, evaluator, options)
	initializeRawGateway(t, first, gatewayProtocolCurrent)
	approveRawTool(t, first, privateKey)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := newRawGatewayHarnessWithOptions(t, plan, evaluator, options)
	initializeRawGateway(t, second, gatewayProtocolCurrent)
	retryParams := rawApprovalRetryParams(t, second, privateKey)
	replayRequest := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":%s}`, retryParams)
	replayed := decodeRawToolResult(t, second.exchange(t, replayRequest))
	if !replayed.IsError || !strings.Contains(rawResultText(replayed), string(action.ReasonApprovalReplayed)) {
		t.Fatalf("fresh-session approval replay result = %#v", replayed)
	}
	assertInvocationCount(t, paths.invocations, "1\n")
}

func testRawFreshSessionApprovalBudget(t *testing.T) {
	paths := prepareRawFixture(t, "normal", true)
	repository, home := newRawPersistentState(t)
	seed := bytes.Repeat([]byte{0x58}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	registry := writeGatewayApprovalRegistry(t, privateKey.Public().(ed25519.PublicKey))
	plan, evaluator := testGatewayApprovalPlan(t, action.PhasePreCall)
	options := rawGatewayOptions{
		repository: repository, home: home,
		approvalAuthorities: registry, approvalPolicyID: "post-result-policy",
	}
	first := newRawGatewayHarnessWithOptions(t, plan, evaluator, options)
	initializeRawGateway(t, first, gatewayProtocolCurrent)
	approveRawTool(t, first, privateKey)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := newRawGatewayHarnessWithOptions(t, plan, evaluator, options)
	initializeRawGateway(t, second, gatewayProtocolCurrent)
	blocked := callRawTool(t, second, 3, "echo", "second-session")
	if !blocked.IsError || !strings.Contains(rawResultText(blocked), string(action.ReasonBudgetExhausted)) {
		t.Fatalf("fresh-session approval budget result = %#v", blocked)
	}
	assertInvocationCount(t, paths.invocations, "1\n")
}

func testRawFreshSessionCallBudget(t *testing.T) {
	paths := prepareRawFixture(t, "normal", true)
	repository, home := newRawPersistentState(t)
	plan, evaluator := testGatewayBudgetPlanWithLimits(t, action.BudgetLimits{CallCount: 1})
	options := rawGatewayOptions{repository: repository, home: home}
	first := newRawGatewayHarnessWithOptions(t, plan, evaluator, options)
	initializeRawGateway(t, first, gatewayProtocolLegacy)
	if result := callRawTool(t, first, 2, "echo", "first"); result.IsError {
		t.Fatalf("first budgeted call = %#v", result)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := newRawGatewayHarnessWithOptions(t, plan, evaluator, options)
	initializeRawGateway(t, second, gatewayProtocolLegacy)
	blocked := callRawTool(t, second, 2, "echo", "second")
	if !blocked.IsError || !strings.Contains(rawResultText(blocked), string(action.ReasonBudgetExhausted)) {
		t.Fatalf("fresh-session call budget result = %#v", blocked)
	}
	assertInvocationCount(t, paths.invocations, "1\n")
}

func testRawCancellation(t *testing.T) {
	paths := prepareRawFixture(t, "hang", false)
	plan, evaluator := testGatewayBudgetPlan(t)
	harness := newRawGatewayHarness(t, plan, evaluator)
	initializeRawGateway(t, harness, gatewayProtocolCurrent)
	responseDone := make(chan struct {
		response rawRPCResponse
		err      error
	}, 1)
	go func() {
		response, err := harness.readResponseValue()
		responseDone <- struct {
			response rawRPCResponse
			err      error
		}{response: response, err: err}
	}()
	harness.notify(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"value":"cancel"}}}`)
	waitForRegularFile(t, paths.marker)
	harness.notify(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2,"reason":"interoperability-test"}}`)
	waitForRegularFile(t, paths.cancellation)
	select {
	case outcome := <-responseDone:
		if outcome.err == nil && len(outcome.response.Error) == 0 {
			result := decodeRawToolResult(t, outcome.response)
			if !result.IsError || !strings.Contains(rawResultText(result), string(action.ReasonCancelled)) {
				t.Fatalf("cancelled raw result = %#v", result)
			}
		}
	case <-time.After(CancellationGrace):
		t.Fatal("cancelled raw request did not release the upstream response")
	}
	status, err := harness.gateway.state.Status(context.Background())
	if err != nil || status.Indeterminate != 1 || status.LiveReservations != 1 {
		t.Fatalf("cancelled state = %#v, %v", status, err)
	}
}

func prepareRawFixture(t *testing.T, mode string, countInvocations bool) rawFixturePaths {
	t.Helper()
	directory := t.TempDir()
	paths := rawFixturePaths{
		marker:       filepath.Join(directory, "invoked"),
		cancellation: filepath.Join(directory, "cancelled"),
		invocations:  filepath.Join(directory, "invocations"),
	}
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeModeEnvironment, mode)
	t.Setenv(fakeMarkerEnvironment, paths.marker)
	t.Setenv(fakeCancellationMarkerEnvironment, paths.cancellation)
	if countInvocations {
		t.Setenv(fakeInvocationCountEnvironment, paths.invocations)
	}
	return paths
}

func newRawPersistentState(t *testing.T) (string, string) {
	t.Helper()
	repository, err := pathidentity.ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := newPrivateGatewayHome(t)
	return repository, home
}

func initializeRawGateway(t *testing.T, harness *rawGatewayHarness, protocol string) {
	t.Helper()
	request := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{"elicitation":{}},"clientInfo":{"name":"raw-interoperability","version":"1"}}}`,
		protocol,
	)
	response := harness.exchange(t, request)
	if len(response.Error) != 0 {
		t.Fatalf("initialize %s error = %s", protocol, response.Error)
	}
	harness.notify(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
}

func assertRawToolCatalog(t *testing.T, response rawRPCResponse) {
	t.Helper()
	var catalog struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if len(response.Error) != 0 || json.Unmarshal(response.Result, &catalog) != nil || len(catalog.Tools) != 1 {
		t.Fatalf("raw catalog = %s, error=%s", response.Result, response.Error)
	}
	tool := catalog.Tools[0]
	if tool.Name != "echo" {
		t.Fatalf("raw tool contract = %#v", tool)
	}
	want := `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`
	if !canonicalJSONEqual(t, tool.InputSchema, []byte(want)) {
		t.Fatalf("raw input schema = %s", tool.InputSchema)
	}
}

func callRawTool(
	t *testing.T,
	harness *rawGatewayHarness,
	id int,
	name string,
	value string,
) rawToolResult {
	t.Helper()
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": map[string]any{"value": value}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return decodeRawToolResult(t, harness.exchange(t, string(request)))
}

func approveRawTool(t *testing.T, harness *rawGatewayHarness, key ed25519.PrivateKey) {
	t.Helper()
	paramsBody := rawApprovalRetryParams(t, harness, key)
	request := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":%s}`, paramsBody)
	result := decodeRawToolResult(t, harness.exchange(t, request))
	if result.IsError || result.ResultType != "complete" || rawResultText(result) != "hello" {
		t.Fatalf("approved result = %#v", result)
	}
}

func rawApprovalRetryParams(t *testing.T, harness *rawGatewayHarness, key ed25519.PrivateKey) []byte {
	t.Helper()
	first := callRawTool(t, harness, 2, "echo", "approve")
	if first.ResultType != "input_required" || first.RequestState == "" || len(first.InputRequests) != 1 {
		t.Fatalf("approval input-required result = %#v", first)
	}
	responses := make(map[string]any, 1)
	for id, request := range first.InputRequests {
		if request.Method != "elicitation/create" {
			t.Fatalf("approval input method = %q", request.Method)
		}
		responses[id] = map[string]any{
			"action":  "accept",
			"content": map[string]any{"receipt": signRawApproval(t, request.Params.Message, key)},
		}
	}
	params := map[string]any{
		"name": "echo", "arguments": map[string]any{"value": "approve"},
		"requestState": first.RequestState, "inputResponses": responses,
	}
	paramsBody, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return paramsBody
}

func signRawApproval(t *testing.T, message string, key ed25519.PrivateKey) string {
	return signRawApprovalSequence(t, message, key, 1)
}

func signRawApprovalSequence(
	t *testing.T,
	message string,
	key ed25519.PrivateKey,
	sequence uint64,
) string {
	t.Helper()
	start := strings.Index(message, "{")
	if start < 0 {
		t.Fatalf("approval message has no JSON body: %q", message)
	}
	request, err := actionapproval.DecodeRequest([]byte(message[start:]))
	if err != nil {
		t.Fatal(err)
	}
	signedAt, err := time.Parse(time.RFC3339Nano, request.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 16)
	binary.BigEndian.PutUint64(nonce[8:], sequence)
	_, receipt, err := actionapproval.SignReceipt(
		request, "gateway-authority", key, actionapproval.DecisionApprove,
		signedAt, bytes.NewReader(nonce),
	)
	if err != nil {
		t.Fatal(err)
	}
	return string(receipt)
}

func decodeRawToolResult(t *testing.T, response rawRPCResponse) rawToolResult {
	t.Helper()
	if len(response.Error) != 0 {
		t.Fatalf("raw tool RPC error = %s", response.Error)
	}
	var result rawToolResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func rawResultText(result rawToolResult) string {
	parts := make([]string, len(result.Content))
	for index, content := range result.Content {
		parts[index] = content.Text
	}
	return strings.Join(parts, "\n")
}

func assertRawInvocation(t *testing.T, marker string, want bool) {
	t.Helper()
	_, err := os.Stat(marker)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if (err == nil) != want {
		t.Fatalf("downstream invoked = %t, want %t", err == nil, want)
	}
}

func assertInvocationCount(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil || string(body) != want {
		t.Fatalf("invocation count = %q, want %q: %v", body, want, err)
	}
}

func canonicalJSONEqual(t *testing.T, left, right []byte) bool {
	t.Helper()
	leftValue, err := action.ParseJSON(left)
	if err != nil {
		t.Fatal(err)
	}
	rightValue, err := action.ParseJSON(right)
	if err != nil {
		t.Fatal(err)
	}
	leftBody, err := leftValue.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	rightBody, err := rightValue.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(leftBody, rightBody)
}
