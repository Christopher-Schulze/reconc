package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

type stdoutDecisionRouteFixture struct {
	name     string
	route    hooks.RuntimeRoute
	wantKey  string
	wantJSON string
}

func TestHookFailClosedEnvelopeIncludesTerminalFrameAtRouteLimit(t *testing.T) {
	for _, fixture := range stdoutDecisionRouteFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			envelope := failClosedOversizeEnvelope(fixture.route)
			exactLimit := framedHookOutputBytes(envelope)
			for _, delta := range []int{-1, 0, 1} {
				t.Run(map[int]string{-1: "limit-minus-one", 0: "exact-limit", 1: "limit-plus-one"}[delta], func(t *testing.T) {
					route := fixture.route
					route.MaxOutputBytes = exactLimit + delta
					result := boundHookResult(agentsession.Result{Stdout: strings.Repeat("x", exactLimit*2)}, route)
					stdout, stderr := newHookOutputCapture(t, route.MaxOutputBytes)
					emitHookRuntimeResult(result, stdout, stderr)
					if stdout.Truncated() || stderr.Truncated() {
						t.Fatalf("framed output truncated at limit %d: stdout=%q stderr=%q", route.MaxOutputBytes, stdout.String(), stderr.String())
					}
					if delta < 0 {
						if result.Stdout != "" || result.ExitCode != 2 {
							t.Fatalf("undersized budget emitted partial decision: result=%+v", result)
						}
						return
					}
					if result.ExitCode != 0 || result.Stdout != envelope || stdout.String() != envelope+"\n" || len(stdout.Bytes()) > route.MaxOutputBytes {
						t.Fatalf("exact envelope result=%+v captured=%q limit=%d", result, stdout.String(), route.MaxOutputBytes)
					}
					assertHookDecisionJSON(t, stdout.Bytes(), fixture.wantKey, fixture.wantJSON)
				})
			}
		})
	}
}

func TestHookOutputBodyBoundaryPreservesCompleteJSON(t *testing.T) {
	const limit = 512
	route := hooks.RuntimeRoute{
		PlatformKind:   hooks.KindGrok,
		Event:          hooks.EventPreToolUse,
		ErrorPolicy:    hooks.FailureBlock,
		MaxOutputBytes: limit,
	}
	bodyLimit := hookOutputBodyLimit(limit - limit/2)
	for _, delta := range []int{-1, 0, 1} {
		name := map[int]string{-1: "limit-minus-one", 0: "exact-limit", 1: "limit-plus-one"}[delta]
		t.Run(name, func(t *testing.T) {
			input := paddedHookDecisionJSON(t, route, bodyLimit+delta, "")
			result := boundHookResult(agentsession.Result{Stdout: input}, route)
			stdout, stderr := newHookOutputCapture(t, limit)
			emitHookRuntimeResult(result, stdout, stderr)
			if stdout.Truncated() || stderr.Truncated() {
				t.Fatalf("body boundary truncated: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			assertHookDecisionJSON(t, stdout.Bytes(), "decision", `"deny"`)
			if delta <= 0 && result.Stdout != input {
				t.Fatalf("admitted JSON changed at delta %d", delta)
			}
			if delta > 0 && result.Stdout == input {
				t.Fatal("over-limit JSON was admitted")
			}
		})
	}
}

func TestHookOutputBoundaryCountsUTF8AndExistingNewlineBytes(t *testing.T) {
	const limit = 512
	route := hooks.RuntimeRoute{
		PlatformKind:   hooks.KindGrok,
		Event:          hooks.EventPreToolUse,
		ErrorPolicy:    hooks.FailureBlock,
		MaxOutputBytes: limit,
	}
	bodyLimit := hookOutputBodyLimit(limit - limit/2)
	tests := []struct {
		name string
		body string
	}{
		{name: "multi-byte UTF-8", body: paddedHookDecisionJSON(t, route, bodyLimit, "grün")},
		{name: "existing newline", body: paddedHookDecisionJSON(t, route, bodyLimit-1, "") + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := boundHookResult(agentsession.Result{Stdout: test.body}, route)
			stdout, stderr := newHookOutputCapture(t, limit)
			emitHookRuntimeResult(result, stdout, stderr)
			if result.Stdout != test.body || stdout.Truncated() || stderr.Truncated() || framedHookOutputBytes(result.Stdout) != limit/2 {
				t.Fatalf("framed boundary result=%+v stdout-bytes=%d truncated=%t", result, len(stdout.Bytes()), stdout.Truncated())
			}
			assertHookDecisionJSON(t, stdout.Bytes(), "decision", `"deny"`)
		})
	}
}

func stdoutDecisionRouteFixtures() []stdoutDecisionRouteFixture {
	return []stdoutDecisionRouteFixture{
		{name: "cursor pre-tool", route: hooks.RuntimeRoute{PlatformKind: hooks.KindCursor, Event: hooks.EventPreToolUse, ErrorPolicy: hooks.FailureBlock}, wantKey: "permission", wantJSON: `"deny"`},
		{name: "cursor stop", route: hooks.RuntimeRoute{PlatformKind: hooks.KindCursor, Event: hooks.EventStop, ErrorPolicy: hooks.FailureBlock}, wantKey: "continue", wantJSON: "false"},
		{name: "cursor subagent stop", route: hooks.RuntimeRoute{PlatformKind: hooks.KindCursor, Event: hooks.EventSubagentStop, ErrorPolicy: hooks.FailureBlock}, wantKey: "continue", wantJSON: "false"},
		{name: "copilot pre-tool", route: hooks.RuntimeRoute{PlatformKind: hooks.KindGitHubCopilot, Event: hooks.EventPreToolUse, ErrorPolicy: hooks.FailureBlock}, wantKey: "permissionDecision", wantJSON: `"deny"`},
		{name: "copilot permission", route: hooks.RuntimeRoute{PlatformKind: hooks.KindGitHubCopilot, Event: hooks.EventPermissionRequest, ErrorPolicy: hooks.FailureBlock}, wantKey: "behavior", wantJSON: `"deny"`},
		{name: "copilot stop", route: hooks.RuntimeRoute{PlatformKind: hooks.KindGitHubCopilot, Event: hooks.EventStop, ErrorPolicy: hooks.FailureBlock}, wantKey: "decision", wantJSON: `"block"`},
		{name: "copilot subagent stop", route: hooks.RuntimeRoute{PlatformKind: hooks.KindGitHubCopilot, Event: hooks.EventSubagentStop, ErrorPolicy: hooks.FailureBlock}, wantKey: "decision", wantJSON: `"block"`},
		{name: "grok pre-tool", route: hooks.RuntimeRoute{PlatformKind: hooks.KindGrok, Event: hooks.EventPreToolUse, ErrorPolicy: hooks.FailureBlock}, wantKey: "decision", wantJSON: `"deny"`},
		{name: "grok stop", route: hooks.RuntimeRoute{PlatformKind: hooks.KindGrok, Event: hooks.EventStop, ErrorPolicy: hooks.FailureBlock}, wantKey: "decision", wantJSON: `"block"`},
	}
}

func newHookOutputCapture(t *testing.T, limit int) (*boundedexec.Buffer, *boundedexec.Buffer) {
	t.Helper()
	stdout, err := boundedexec.NewBuffer(limit)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := boundedexec.NewBuffer(limit)
	if err != nil {
		t.Fatal(err)
	}
	return stdout, stderr
}

func paddedHookDecisionJSON(t *testing.T, route hooks.RuntimeRoute, targetBytes int, detail string) string {
	t.Helper()
	body := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(failClosedOversizeEnvelope(route)), &body); err != nil {
		t.Fatal(err)
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	body["detail"] = detailJSON
	body["padding"] = json.RawMessage(`""`)
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > targetBytes {
		t.Fatalf("minimum decision JSON is %d bytes, target is %d", len(encoded), targetBytes)
	}
	paddingJSON, err := json.Marshal(strings.Repeat("x", targetBytes-len(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	body["padding"] = paddingJSON
	encoded, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != targetBytes {
		t.Fatalf("padded decision JSON is %d bytes, want %d", len(encoded), targetBytes)
	}
	return string(encoded)
}

func assertHookDecisionJSON(t *testing.T, body []byte, key, wantJSON string) {
	t.Helper()
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode complete decision JSON: %v: %q", err, body)
	}
	if string(decoded[key]) != wantJSON {
		t.Fatalf("decision %s=%s, want %s: %#v", key, decoded[key], wantJSON, decoded)
	}
}
