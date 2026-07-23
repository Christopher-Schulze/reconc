package agentsession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestMCPRuntimeEnforcesTypedEffectsAndEvidence(t *testing.T) {
	repo := setupMCPPolicyRepo(t)
	if result := RunSessionStart(repo, []byte(`{"session_id":"mcp-session"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}

	read := mcpTestPayload(t, "mcp-session", "cursor", "read_repo", map[string]interface{}{
		"paths": []interface{}{"docs/a.md", "docs/b.md"},
	}, "success", true)
	if result := RunMCPAfter(repo, read); result.ExitCode != 0 {
		t.Fatalf("read after: %+v", result)
	}

	protected := mcpTestPayload(t, "mcp-session", "cursor", "write_repo", map[string]interface{}{
		"path": "generated/out.go",
	}, "", true)
	if result := RunMCPBefore(repo, protected); result.ExitCode != 2 {
		t.Fatalf("protected MCP write was not denied: %+v", result)
	}

	safeWrite := mcpTestPayload(t, "mcp-session", "cursor", "write_repo", map[string]interface{}{
		"path": "src/out.go",
	}, "success", true)
	if result := RunMCPBefore(repo, safeWrite); result.ExitCode != 0 {
		t.Fatalf("safe MCP write denied: %+v", result)
	}
	if result := RunMCPAfter(repo, safeWrite); result.ExitCode != 0 {
		t.Fatalf("safe MCP write after: %+v", result)
	}
	if result := RunMCPAfter(repo, safeWrite); result.ExitCode != 0 {
		t.Fatalf("duplicate MCP write after: %+v", result)
	}

	forbidden := mcpTestPayload(t, "mcp-session", "opencode", "run_command", map[string]interface{}{
		"command": "git reset --hard",
	}, "", false)
	if result := RunPreToolUseMCPAware(repo, forbidden); result.ExitCode != 2 {
		t.Fatalf("forbidden configured MCP command was not denied: %+v", result)
	}

	command := mcpTestPayload(t, "mcp-session", "opencode", "run_command", map[string]interface{}{
		"command": "go test ./...",
	}, "success", false)
	if result := RunPreToolUseMCPAware(repo, command); result.ExitCode != 0 {
		t.Fatalf("safe configured MCP command denied: %+v", result)
	}
	if result := RunPostToolUseMCPAware(repo, command); result.ExitCode != 0 {
		t.Fatalf("configured MCP command after: %+v", result)
	}

	failedCommand := mcpTestPayload(t, "mcp-session", "opencode", "run_command", map[string]interface{}{
		"command": "go test ./broken",
	}, "failure", false)
	if result := RunPostToolUseMCPAware(repo, failedCommand); result.ExitCode != 0 {
		t.Fatalf("failed MCP command after: %+v", result)
	}

	external := mcpTestPayload(t, "mcp-session", "kilo", "external_mutation", map[string]interface{}{
		"token": "top-secret-token",
		"body":  "external-result-secret",
	}, "success", false)
	if result := RunPostToolUseMCPAware(repo, external); result.ExitCode != 0 {
		t.Fatalf("external MCP after: %+v", result)
	}

	state, err := LoadSessionState(repo, "mcp-session")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(state.ReadPaths, ",") != "docs/a.md,docs/b.md" {
		t.Fatalf("read evidence=%v", state.ReadPaths)
	}
	if strings.Join(state.WritePaths, ",") != "src/out.go" || state.EvidenceEpoch != 1 {
		t.Fatalf("write evidence or exactly-once epoch wrong: paths=%v epoch=%d", state.WritePaths, state.EvidenceEpoch)
	}
	if !containsString(state.Commands, "go test ./...") || containsString(state.Commands, "go test ./broken") {
		t.Fatalf("command evidence=%v", state.Commands)
	}
	if len(state.CommandResults) != 2 || state.CommandResults[0].Outcome != "success" || state.CommandResults[1].Outcome != "failure" {
		t.Fatalf("command results=%#v", state.CommandResults)
	}
	if state.CommandResults[1].ExitCode != nil {
		t.Fatalf("failed MCP command fabricated exit code %d", *state.CommandResults[1].ExitCode)
	}
	if strings.Contains(mustJSON(t, state), "top-secret-token") || strings.Contains(mustJSON(t, state), "external-result-secret") {
		t.Fatal("external MCP payload leaked into session evidence")
	}
	resolvedRepo, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	auditBody, err := os.ReadFile(mcpAuditPath(resolvedRepo))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"top-secret-token", "external-result-secret", "git reset --hard", "go test ./..."} {
		if strings.Contains(string(auditBody), secret) {
			t.Fatalf("MCP audit leaked %q", secret)
		}
	}
}

func TestMCPRuntimeUnclassifiedStrictAndGenericLimitations(t *testing.T) {
	repo := setupMCPPolicyRepo(t)
	mismatchedPresence := mcpTestPayloadWithFingerprint(t, "strict-session", "cursor", "read_repo", map[string]interface{}{
		"paths": []interface{}{"docs/mismatched-presence.md"},
	}, "success", true, "sha256:"+strings.Repeat("a", 64))
	if result := RunMCPAfter(repo, mismatchedPresence); result.ExitCode != 0 {
		t.Fatalf("fingerprint-presence mismatch was not handled as an observation: %+v", result)
	}
	state, err := LoadSessionState(repo, "strict-session")
	if err != nil {
		t.Fatal(err)
	}
	if containsString(state.ReadPaths, "docs/mismatched-presence.md") {
		t.Fatalf("fingerprinted call fell back to an unqualified selector: %#v", state.ReadPaths)
	}

	exactFingerprint := mcpTestPayloadWithFingerprint(t, "strict-session", "cursor", "fingerprinted_read", map[string]interface{}{
		"paths": []interface{}{"docs/fingerprinted.md"},
	}, "success", true, "sha256:"+strings.Repeat("a", 64))
	if result := RunMCPAfter(repo, exactFingerprint); result.ExitCode != 0 {
		t.Fatalf("exact fingerprint selector failed: %+v", result)
	}
	state, err = LoadSessionState(repo, "strict-session")
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(state.ReadPaths, "docs/fingerprinted.md") {
		t.Fatalf("exact fingerprint selector produced no read evidence: %#v", state.ReadPaths)
	}

	wrongFingerprint := mcpTestPayloadWithFingerprint(t, "strict-session", "cursor", "fingerprinted_read", map[string]interface{}{
		"paths": []interface{}{"docs/wrong-fingerprint.md"},
	}, "success", true, "sha256:"+strings.Repeat("b", 64))
	if result := RunMCPAfter(repo, wrongFingerprint); result.ExitCode != 0 {
		t.Fatalf("wrong fingerprint was not handled as an observation: %+v", result)
	}
	state, err = LoadSessionState(repo, "strict-session")
	if err != nil {
		t.Fatal(err)
	}
	if containsString(state.ReadPaths, "docs/wrong-fingerprint.md") {
		t.Fatalf("wrong fingerprint produced repository evidence: %#v", state.ReadPaths)
	}

	cursorUnknown := mcpTestPayload(t, "strict-session", "cursor", "unknown", map[string]interface{}{}, "", true)
	if result := RunMCPBefore(repo, cursorUnknown); result.ExitCode != 2 || !strings.Contains(result.Stderr, "strict policy") {
		t.Fatalf("Cursor unclassified deny=%+v", result)
	}

	genericUnknown := mcpTestPayload(t, "strict-session", "opencode", "unknown_custom_or_mcp", map[string]interface{}{}, "", false)
	if result := RunPreToolUseMCPAware(repo, genericUnknown); result.ExitCode != 0 {
		t.Fatalf("generic unknown tool was falsely classified as MCP: %+v", result)
	}

	wrongType := mcpTestPayload(t, "strict-session", "cursor", "write_repo", map[string]interface{}{
		"path": map[string]interface{}{"secret": true},
	}, "", true)
	if result := RunMCPBefore(repo, wrongType); result.ExitCode != 2 {
		t.Fatalf("invalid selector value did not become strict unclassified: %+v", result)
	}

	escape := mcpTestPayload(t, "strict-session", "cursor", "write_repo", map[string]interface{}{
		"path": "../escape",
	}, "", true)
	if result := RunMCPBefore(repo, escape); result.ExitCode != 2 {
		t.Fatalf("repository escape did not become strict unclassified: %+v", result)
	}
}

func TestCursorMCPNormalizationFingerprintsAndRedactsLocators(t *testing.T) {
	credentialLocator := strings.Join([]string{
		"HTTPS://", "User", ":", "pass", "@",
		"Example.COM", "/mcp?token=credential#fragment",
	}, "")
	raw, err := json.Marshal(map[string]interface{}{
		"conversation_id": "cursor-mcp",
		"tool_name":       "write_repo",
		"tool_input":      `{"path":"src/a.go","secret":"payload-secret"}`,
		"url":             credentialLocator,
	})
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := NormalizeCursorPayload("cursor-before-mcp-execution", raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(normalized)
	for _, forbidden := range []string{"User", "pass", "Example.COM", "token=credential", "fragment"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("normalized MCP payload leaked locator material %q: %s", forbidden, text)
		}
	}
	payload, err := ParsePayload(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if payload.MCP == nil || payload.MCP.ServerFingerprint == "" || !payload.MCP.Observed || !payload.MCP.BlockingPreHook {
		t.Fatalf("normalized MCP envelope=%#v", payload.MCP)
	}
	if payload.ToolInput["secret"] != "payload-secret" {
		t.Fatalf("tool input was not decoded exactly: %#v", payload.ToolInput)
	}

	for name, locator := range map[string]string{
		"invalid URL":    `"url":"://invalid"`,
		"wrong URL type": `"url":7`,
		"two locators":   `"url":"https://example.invalid/mcp","command":"server"`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := []byte(`{"conversation_id":"cursor-mcp","tool_name":"write_repo","tool_input":{"path":"src/a.go"},` + locator + `}`)
			normalized, err := NormalizeCursorPayload("cursor-before-mcp-execution", raw)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := ParsePayload(normalized)
			if err != nil {
				t.Fatal(err)
			}
			if payload.MCP == nil || payload.MCP.InputValid || payload.MCP.ServerFingerprint != "" {
				t.Fatalf("ambiguous or invalid locator remained classifiable: %#v", payload.MCP)
			}
		})
	}
}

func TestCursorMCPOutcomeRequiresExplicitSuccess(t *testing.T) {
	tests := []struct {
		name   string
		result interface{}
		want   string
		topErr string
	}{
		{name: "camel success", result: map[string]interface{}{"isError": false}, want: "success"},
		{name: "snake success", result: `{"is_error":false}`, want: "success"},
		{name: "explicit success", result: map[string]interface{}{"success": true}, want: "success"},
		{name: "camel failure", result: map[string]interface{}{"isError": true}, want: "failure"},
		{name: "explicit failure", result: map[string]interface{}{"success": false}, want: "failure"},
		{name: "conflicting fields", result: map[string]interface{}{"isError": false, "success": false}, want: "failure"},
		{name: "success plus error", result: map[string]interface{}{"isError": false, "error": "failed"}, want: "failure"},
		{name: "malformed success field", result: map[string]interface{}{"success": "true"}, want: "failure"},
		{name: "structured error", result: map[string]interface{}{"error": map[string]interface{}{"message": "failed"}}, want: "failure"},
		{name: "missing result", want: "failure"},
		{name: "unknown object", result: map[string]interface{}{"content": "not an outcome"}, want: "failure"},
		{name: "malformed result", result: "{", want: "failure"},
		{name: "top-level error", result: map[string]interface{}{"isError": false}, topErr: "host failed", want: "failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := map[string]interface{}{}
			if test.result != nil {
				raw["tool_response"] = test.result
			}
			if test.topErr != "" {
				raw["error"] = test.topErr
			}
			if got := cursorMCPOutcome(raw); got != test.want {
				t.Fatalf("cursorMCPOutcome() = %q, want %q", got, test.want)
			}
		})
	}
}

func setupMCPPolicyRepo(t *testing.T) string {
	t.Helper()
	repo := setupPolicyRepo(t)
	config := `mcp:
  unclassified: deny
  tools:
    - platform: cursor
      tool: read_repo
      effect: repository_read
      path_fields: [/paths]
    - platform: cursor
      tool: write_repo
      effect: repository_write
      path_fields: [/path]
    - platform: cursor
      server_fingerprint: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      tool: fingerprinted_read
      effect: repository_read
      path_fields: [/paths]
    - platform: opencode
      tool: run_command
      effect: command
      command_field: /command
    - platform: kilo
      tool: external_mutation
      effect: external
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile MCP repo: %v", err)
	}
	return repo
}

func mcpTestPayload(t *testing.T, session, platform, tool string, input map[string]interface{}, outcome string, observed bool) []byte {
	return mcpTestPayloadWithFingerprint(t, session, platform, tool, input, outcome, observed, "")
}

func mcpTestPayloadWithFingerprint(t *testing.T, session, platform, tool string, input map[string]interface{}, outcome string, observed bool, fingerprint string) []byte {
	t.Helper()
	mcp := map[string]interface{}{
		"platform":          platform,
		"tool":              tool,
		"observed":          observed,
		"blocking_pre_hook": true,
		"input_valid":       true,
	}
	if outcome != "" {
		mcp["outcome"] = outcome
	}
	if fingerprint != "" {
		mcp["server_fingerprint"] = fingerprint
	}
	body, err := json.Marshal(map[string]interface{}{
		"session_id":  session,
		"tool_input":  input,
		"tool_use_id": "call-" + tool,
		"reconc_mcp":  mcp,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func mustJSON(t *testing.T, value interface{}) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
