package customruntime

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPublishedFixturesConform(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"local-agent", "ci-bot"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			manifest := mustManifest(t, "testdata/"+name+".json")
			body, err := os.ReadFile("testdata/" + name + "-conformance.json")
			if err != nil {
				t.Fatal(err)
			}
			suite, err := DecodeConformanceSuite(body)
			if err != nil {
				t.Fatal(err)
			}
			report, err := RunConformance(manifest, suite)
			if err != nil {
				t.Fatal(err)
			}
			if !report.Passed || report.CaseCount != 3 || len(report.Capabilities) != 6 {
				t.Fatalf("unexpected report: %+v", report)
			}
		})
	}
}

func TestDecodeManifestRejectsAmbiguousOrUnsafeContracts(t *testing.T) {
	t.Parallel()
	valid, err := os.ReadFile("testdata/local-agent.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "duplicate key", body: `{"$schema":"a","$schema":"b"}`, want: "duplicate key"},
		{name: "unknown field", body: strings.Replace(string(valid), `"display_name":`, `"unexpected":true,"display_name":`, 1), want: "unknown field"},
		{name: "reserved built-in", body: strings.Replace(string(valid), `"name": "local-agent"`, `"name": "codex"`, 1), want: "reserved"},
		{name: "expression-like pointer", body: strings.Replace(string(valid), `"/context/session"`, `"$.context.session"`, 1), want: "JSON Pointer"},
		{name: "null routes", body: `{"$schema":"https://reconc.dev/schemas/custom-runtime-manifest/v1","format_version":"reconc-custom-runtime/v1","name":"x","display_name":"X","routes":null}`, want: "must not be null"},
		{name: "missing guarantee fields", body: strings.Replace(string(valid), `"guarantees": {"pre_execution": false, "synchronous_response": false, "authoritative_outcome": true, "continuation": false, "continuation_ack": false, "mcp_identity": false}`, `"guarantees": {}`, 1), want: "pre_execution"},
		{name: "excessive depth", body: `{"x":` + strings.Repeat(`{"x":`, 32) + `null` + strings.Repeat(`}`, 33), want: "exceeds 32 levels"},
		{name: "unsorted routes", body: strings.Replace(string(valid), `"host_event": "after-tool"`, `"host_event": "z-after-tool"`, 1), want: "lexically sorted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeManifest([]byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNormalizeHostPayloadUsesExactPointersAndExcludesUnselectedData(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	route.Fields.ToolName = "/tools/0/name"
	route.Fields.ToolInput = "/a~1b/~0input"
	body := []byte(`{"context":{"session":"s"},"tools":[{"name":"Read"}],"a/b":{"~input":{"path":"README.md"}},"private":"never-copy"}`)
	request, encoded, err := NormalizeHostPayload(manifest, route, body)
	if err != nil {
		t.Fatal(err)
	}
	if request.Payload["tool_name"] != "Read" || strings.Contains(string(encoded), "never-copy") {
		t.Fatalf("unexpected normalized request: %s", encoded)
	}
}

func TestNormalizeHostPayloadRejectsAmbiguousAndMalformedInput(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	tests := []struct {
		name    string
		body    []byte
		mutate  func(*Route)
		wantErr string
	}{
		{name: "duplicate key", body: []byte(`{"context":{"session":"a"},"context":{"session":"b"}}`), wantErr: "duplicate key"},
		{name: "trailing value", body: []byte(`{"context":{"session":"a"}}{"extra":true}`), wantErr: "exactly one JSON value"},
		{name: "non-object", body: []byte(`null`), wantErr: "JSON object"},
		{name: "invalid pointer", body: []byte(`{"context":{"session":"a"}}`), mutate: func(value *Route) { value.Fields.SessionID = "context/session" }, wantErr: "JSON Pointer"},
		{name: "oversized", body: make([]byte, maxHostPayloadBytes+1), wantErr: "1.."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := route
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			if _, _, err := NormalizeHostPayload(manifest, candidate, test.body); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("NormalizeHostPayload() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestNormalizeHostPayloadBuildsMCPEnvelopeAndExitCode(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	route := Route{
		HostEvent: "mcp-before",
		Event:     EventMCPBefore,
		Guarantees: HostGuarantees{
			PreExecution: true, SynchronousResponse: true, MCPIdentity: true,
		},
		Fields: FieldMappings{
			SessionID:            "/context/session",
			ToolInput:            "/tool/input",
			ExitCode:             "/tool/exit",
			MCPTool:              "/mcp/tool",
			MCPServerFingerprint: "/mcp/fingerprint",
			MCPOutcome:           "/mcp/outcome",
		},
	}
	body := []byte(`{"context":{"session":"session-1"},"tool":{"input":{"path":"src/app.go"},"exit":0},"mcp":{"tool":"read_file","fingerprint":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","outcome":"success"}}`)
	request, encoded, err := NormalizeHostPayload(manifest, route, body)
	if err != nil {
		t.Fatal(err)
	}
	if request.Payload["session_id"] != "session-1" || !json.Valid(encoded) {
		t.Fatalf("normalized payload = %#v, encoded=%s", request.Payload, encoded)
	}
	response, ok := request.Payload["tool_response"].(map[string]interface{})
	if !ok || response["exit_code"] != 0 {
		t.Fatalf("exit code mapping = %#v", request.Payload["tool_response"])
	}
	mcp, ok := request.Payload["reconc_mcp"].(map[string]interface{})
	if !ok || mcp["platform"] != manifest.Runtime() || mcp["tool"] != "read_file" || mcp["blocking_pre_hook"] != true || mcp["server_fingerprint"] == nil || mcp["outcome"] != "success" {
		t.Fatalf("MCP envelope = %#v", request.Payload["reconc_mcp"])
	}
}

func TestManifestSummaryAndRouteLookupRemainDeterministic(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	summary := manifest.Summary()
	if summary.Name != "local-agent" || summary.Runtime != "custom:local-agent" || summary.RouteCount != 4 || !validSHA256Digest(summary.ManifestDigest) {
		t.Fatalf("summary = %+v", summary)
	}
	if route, ok := manifest.Route("before-tool"); !ok || route.Event != EventPreToolUse {
		t.Fatalf("before-tool route = %+v, found=%t", route, ok)
	}
	if _, ok := manifest.Route("missing"); ok {
		t.Fatal("unknown route unexpectedly resolved")
	}
	for _, invalid := range []Summary{
		{Name: "local-agent", Runtime: "custom:other", ManifestDigest: summary.ManifestDigest, RouteCount: 1},
		{Name: "local-agent", Runtime: summary.Runtime, ManifestDigest: "sha256:bad", RouteCount: 1},
		{Name: "local-agent", Runtime: summary.Runtime, ManifestDigest: summary.ManifestDigest, RouteCount: 0},
		{Name: "local-agent", Runtime: summary.Runtime, ManifestDigest: summary.ManifestDigest, RouteCount: 1, DegradedRoutes: []string{"z", "a"}},
	} {
		if err := ValidateSummary(invalid); err == nil {
			t.Fatalf("invalid summary unexpectedly validated: %+v", invalid)
		}
	}
}

func TestUnsupportedGuaranteeNeverEnforces(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	route.Guarantees.PreExecution = false
	response := BuildResponse(manifest, route, 2, `{"decision":"block"}`, "blocked", nil, false)
	if response.Decision != DecisionUnsupported || response.ExitCode != 0 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestTimeoutUsesTimeoutPolicy(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	route.ErrorPolicy = FailureAllow
	route.TimeoutPolicy = FailureBlock
	response := BuildResponse(manifest, route, 0, "", "", nil, true)
	if response.Decision != DecisionBlock || response.ExitCode != 2 || !strings.Contains(response.Reason, "5 second") {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestHostFailurePolicyRemainsExplicit(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("after-tool")
	response := BuildResponse(manifest, route, 0, "", "", nil, true)
	if response.Decision != DecisionHost || response.ExitCode != 0 {
		t.Fatalf("unexpected host-policy response: %+v", response)
	}
}

func TestBoundResponseHonorsBudget(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	response := BuildResponse(manifest, route, 2, "", strings.Repeat("ü", 10_000), nil, false)
	body, err := BoundResponse(response, 512)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 512 || !json.Valid(body) {
		t.Fatalf("invalid bounded response: len=%d body=%q", len(body), body)
	}
}

func TestBoundResponseOmitsSuffixWhenOnlyMetadataFits(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	response := BuildResponse(manifest, route, 2, "", strings.Repeat("ü", 100), nil, false)
	response.Reason = ""
	if padding := 256 - len(MarshalResponse(response)); padding > 0 {
		response.HostEvent += strings.Repeat("x", padding)
	}
	metadataBytes := len(MarshalResponse(response))
	response.Reason = strings.Repeat("ü", 100)
	body, err := BoundResponse(response, metadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > metadataBytes || !json.Valid(body) {
		t.Fatalf("invalid metadata-only response: len=%d body=%q", len(body), body)
	}
}

func TestManifestVersionOwnsCanonicalResponseBudget(t *testing.T) {
	t.Parallel()
	legacy := mustManifest(t, "testdata/local-agent.json")
	legacy.Routes[0].MaxOutputBytes = 256
	legacyBody, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifest(legacyBody); err == nil || !strings.Contains(err.Error(), "max_output_bytes") {
		t.Fatalf("undersized legacy response budget error = %v", err)
	}

	current := legacy
	current.Schema = ManifestSchemaURL
	current.FormatVersion = ManifestFormatVersion
	for index := range current.Routes {
		current.Routes[index].MaxOutputBytes = 512
	}
	currentBody, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifest(currentBody); err != nil {
		t.Fatalf("current 512-byte response budget was rejected: %v", err)
	}
	current.Routes[0].MaxOutputBytes = 511
	currentBody, err = json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifest(currentBody); err == nil || !strings.Contains(err.Error(), "512..65536") {
		t.Fatalf("undersized current response budget error = %v", err)
	}
}

func TestConformancePrivacyIncludesResponse(t *testing.T) {
	t.Parallel()
	manifest := mustManifest(t, "testdata/local-agent.json")
	body, err := os.ReadFile("testdata/local-agent-conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	suite, err := DecodeConformanceSuite(body)
	if err != nil {
		t.Fatal(err)
	}
	suite.Cases[0].PrivateMarkers = []string{"response-secret"}
	suite.Cases[0].Result.ExitCode = 2
	suite.Cases[0].Result.Stderr = "response-secret"
	suite.Cases[0].ExpectedDecision = DecisionBlock
	if _, err := RunConformance(manifest, suite); err == nil || !strings.Contains(err.Error(), "leaked private marker") {
		t.Fatalf("response privacy error = %v", err)
	}
}

func TestLivenessKeysAreSafeAndCollisionResistantWhenTruncated(t *testing.T) {
	t.Parallel()
	left := LivenessEvent(strings.Repeat("route.", 20) + "a")
	right := LivenessEvent(strings.Repeat("route.", 20) + "b")
	if len(left) > 64 || left == right || strings.Contains(left, ".") {
		t.Fatalf("unsafe liveness keys: %q %q", left, right)
	}
	if LivenessEvent("job.completed") == LivenessEvent("job-completed") {
		t.Fatal("distinct host events collided after liveness normalization")
	}
}

func FuzzDecodeManifest(f *testing.F) {
	// A silently dropped seed leaves the target fuzzing empty input forever.
	body, err := os.ReadFile("testdata/local-agent.json")
	if err != nil {
		f.Fatalf("read manifest seed: %v", err)
	}
	f.Add(body)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"$schema":"x","format_version":"1","name":"a","display_name":"A","routes":[]}`))
	f.Fuzz(func(t *testing.T, candidate []byte) {
		_, _ = DecodeManifest(candidate)
	})
}

func FuzzNormalizeHostPayload(f *testing.F) {
	manifest := mustManifest(f, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	f.Add([]byte(`{"context":{"session":"s"},"tool":{"name":"Read","input":{}}}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _, _ = NormalizeHostPayload(manifest, route, body)
	})
}

type testingTB interface {
	Helper()
	Fatal(...interface{})
}

func mustManifest(t testingTB, path string) Manifest {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := DecodeManifest(body)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
