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
	body, _ := os.ReadFile("testdata/local-agent.json")
	f.Add(body)
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
