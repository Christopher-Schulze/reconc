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
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionstate"
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
	t.Run("fresh-session-call-budget", testRawFreshSessionCallBudget)
	t.Run("cancellation", testRawCancellation)
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
	home, err := pathidentity.ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := actionstate.CreateIdentityKey(home, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
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
	binary.BigEndian.PutUint64(nonce[8:], 1)
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
