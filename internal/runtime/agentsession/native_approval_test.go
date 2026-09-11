package agentsession

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/privatefs"
	productruntime "reconc.dev/reconc/internal/runtime"
)

type nativeApprovalFixture struct {
	repo       string
	sessionID  string
	registry   string
	privateKey ed25519.PrivateKey
}

func newNativeApprovalFixture(t *testing.T) nativeApprovalFixture {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := `rules:
  - id: authority-files
    template: authority-change-approval
    when_paths: ['AGENTS.md']
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	sessionID := "native-approval"
	if result := RunSessionStart(repo, []byte(fmt.Sprintf(`{"session_id":%q}`, sessionID))); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}

	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x41}, ed25519.SeedSize))
	registryDir := t.TempDir()
	if err := privatefs.RepairDirectory(registryDir); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(registryDir, "authorities.json")
	registry := actionapproval.Registry{
		Schema: actionapproval.RegistrySchema, FormatVersion: actionapproval.FormatVersion,
		Authorities: []actionapproval.Authority{{
			ID:         "authority-primary",
			PublicKey:  base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
			ActiveFrom: "2020-01-01T00:00:00Z",
		}},
		AuthorityPolicies: []actionapproval.AuthorityPolicy{{
			ID: "authority-policy", AuthorityKeyIDs: []string{"authority-primary"},
		}},
	}
	registryBody, err := canonicalNativeJSON(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := privatefs.WritePrivateIfChanged(registryPath, registryBody, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(nativeApprovalAuthoritiesEnv, registryPath)
	t.Setenv(nativeApprovalPolicyEnv, "authority-policy")
	t.Setenv(nativeApprovalPrincipalEnv, "operator-primary")
	return nativeApprovalFixture{repo: repo, sessionID: sessionID, registry: registryPath, privateKey: privateKey}
}

func canonicalNativeJSON(value interface{}) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	parsed, err := action.ParseObjectJSON(body)
	if err != nil {
		return nil, err
	}
	return parsed.MarshalJSON()
}

func nativeApprovalPayload(t *testing.T, fixture nativeApprovalFixture, toolUseID, content string, issuedAt time.Time) []byte {
	t.Helper()
	raw := map[string]interface{}{
		"session_id":  fixture.sessionID,
		"tool_use_id": toolUseID,
		"tool_name":   "Write",
		"tool_input": map[string]interface{}{
			"file_path": "AGENTS.md",
			"content":   content,
		},
	}
	return nativeApprovalPayloadForRaw(t, fixture, raw, []string{"AGENTS.md"}, issuedAt)
}

func nativeApprovalPayloadForRaw(t *testing.T, fixture nativeApprovalFixture, raw map[string]interface{}, writePaths []string, issuedAt time.Time) []byte {
	t.Helper()
	payloadBody, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload(payloadBody)
	if err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ensureSessionStateResolved(root, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := productruntime.NormalizeReplayInputs(root, productruntime.ExecutionInputs{WritePaths: writePaths})
	if err != nil {
		t.Fatal(err)
	}
	evaluator := productruntime.NewEvaluator()
	compiled, _, err := evaluator.CurrentCompiledPolicyEvaluator(root)
	if err != nil {
		t.Fatal(err)
	}
	ruleIDs, err := compiled.BoundApprovalRuleIDs(root, normalized.WritePaths)
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest, lockDigest, err := compiled.PolicyDigests()
	if err != nil {
		t.Fatal(err)
	}
	candidate := actionapproval.Request{
		Schema:        actionapproval.RequestSchema,
		FormatVersion: actionapproval.FormatVersion,
		RequestID:     "apr_" + strings.Repeat("a", 26),
		IssuedAt:      issuedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt:     issuedAt.UTC().Add(30 * time.Second).Format(time.RFC3339Nano),
		Nonce:         base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x51}, 32)),
	}
	expected, err := nativeExpectedApprovalRequest(candidate, root, payload, state, normalized.WritePaths, ruleIDs, sourceDigest, lockDigest, "operator-primary", "authority-policy")
	if err != nil {
		t.Fatal(err)
	}
	_, receiptBody, err := actionapproval.SignReceipt(expected, "authority-primary", fixture.privateKey, actionapproval.DecisionApprove, issuedAt, bytes.NewReader(bytes.Repeat([]byte{0x61}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	requestBody, err := actionapproval.EncodeRequest(expected)
	if err != nil {
		t.Fatal(err)
	}
	requestObject, err := decodeNativeJSONObject(requestBody)
	if err != nil {
		t.Fatal(err)
	}
	receiptObject, err := decodeNativeJSONObject(receiptBody)
	if err != nil {
		t.Fatal(err)
	}
	raw["reconc_approval"] = map[string]interface{}{
		"request": requestObject,
		"receipt": receiptObject,
	}
	result, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func decodeNativeJSONObject(body []byte) (map[string]interface{}, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value map[string]interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func TestNativeAuthorityApprovalRequiresExactPreActionReceipt(t *testing.T) {
	fixture := newNativeApprovalFixture(t)
	if _, err := RecordClaim(fixture.repo, productruntime.BoundApprovalClaim, fixture.sessionID); err != nil {
		t.Fatalf("record self-authored claim: %v", err)
	}

	bare := []byte(fmt.Sprintf(`{"session_id":%q,"tool_use_id":"call-bare","tool_name":"Write","tool_input":{"file_path":"AGENTS.md","content":"bare"}}`, fixture.sessionID))
	result := RunPreToolUse(fixture.repo, bare)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "signed pre-action receipt") {
		t.Fatalf("bare claim was not blocked: %+v", result)
	}
	assertNativeAuthorityContent(t, fixture.repo, "original\n")

	issuedAt := time.Now().UTC()
	approved := nativeApprovalPayload(t, fixture, "call-approved", "approved", issuedAt)
	result = RunPreToolUse(fixture.repo, approved)
	if result.ExitCode != 0 {
		t.Fatalf("exact approval was not accepted: %+v", result)
	}
	result = RunPreToolUse(fixture.repo, approved)
	if result.ExitCode != 2 {
		t.Fatalf("approval replay was accepted: %+v", result)
	}
	if err := os.WriteFile(filepath.Join(fixture.repo, "AGENTS.md"), []byte("approved"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertNativeAuthorityContent(t, fixture.repo, "approved")
}

func TestNativeAuthorityApprovalBindsEffectPolicyAndHostIdentity(t *testing.T) {
	fixture := newNativeApprovalFixture(t)
	issuedAt := time.Now().UTC()
	approved := nativeApprovalPayload(t, fixture, "call-effect", "approved", issuedAt)
	var envelope map[string]interface{}
	if err := json.Unmarshal(approved, &envelope); err != nil {
		t.Fatal(err)
	}
	input := envelope["tool_input"].(map[string]interface{})
	input["content"] = "tampered"
	changed, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	result := RunPreToolUse(fixture.repo, changed)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "different repository, policy, tool, path set, or proposed content") {
		t.Fatalf("changed proposed content was accepted: %+v", result)
	}
	assertNativeAuthorityContent(t, fixture.repo, "original\n")

	missingID := nativeApprovalPayload(t, fixture, "call-missing", "missing", issuedAt)
	var missing map[string]interface{}
	if err := json.Unmarshal(missingID, &missing); err != nil {
		t.Fatal(err)
	}
	delete(missing, "tool_use_id")
	missingBody, err := json.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	result = RunPreToolUse(fixture.repo, missingBody)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "stable pre-action tool identity") {
		t.Fatalf("missing host identity was not reported precisely: %+v", result)
	}

	stale := nativeApprovalPayload(t, fixture, "call-stale", "stale", issuedAt)
	if err := os.WriteFile(filepath.Join(fixture.repo, ".reconc.yml"), []byte(`rules:
  - id: authority-files
    template: authority-change-approval
    when_paths: ['other.md']
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(fixture.repo, "test"); err != nil {
		t.Fatalf("compile changed policy: %v", err)
	}
	result = RunPreToolUse(fixture.repo, stale)
	if result.ExitCode != 2 {
		t.Fatalf("stale policy was accepted: %+v", result)
	}
}

func TestNativeAuthorityApprovalRejectsReceiptFromAnotherRepository(t *testing.T) {
	origin := newNativeApprovalFixture(t)
	approved := nativeApprovalPayload(t, origin, "call-wrong-root", "wrong-root", time.Now().UTC())
	target := newNativeApprovalFixture(t)
	result := RunPreToolUse(target.repo, approved)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "different repository, policy, tool, path set, or proposed content") {
		t.Fatalf("wrong-root approval was accepted: %+v", result)
	}
	assertNativeAuthorityContent(t, target.repo, "original\n")
}

func TestNativeAuthorityApprovalRejectsRetargetedSymlink(t *testing.T) {
	fixture := newNativeApprovalFixture(t)
	for _, name := range []string{"authority-a.md", "authority-b.md"} {
		if err := os.WriteFile(filepath.Join(fixture.repo, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	linkPath := filepath.Join(fixture.repo, "authority-link.md")
	if err := os.Symlink("authority-a.md", linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	policy := `rules:
  - id: authority-files
    template: authority-change-approval
    when_paths: ['authority-*.md']
`
	if err := os.WriteFile(filepath.Join(fixture.repo, ".reconc.yml"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(fixture.repo, "test"); err != nil {
		t.Fatalf("compile symlink policy: %v", err)
	}
	raw := map[string]interface{}{
		"session_id":  fixture.sessionID,
		"tool_use_id": "call-symlink",
		"tool_name":   "Write",
		"tool_input": map[string]interface{}{
			"file_path": "authority-link.md",
			"content":   "retargeted",
		},
	}
	approved := nativeApprovalPayloadForRaw(t, fixture, raw, []string{"authority-link.md"}, time.Now().UTC())
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("authority-b.md", linkPath); err != nil {
		t.Fatal(err)
	}
	result := RunPreToolUse(fixture.repo, approved)
	if result.ExitCode != 2 {
		t.Fatalf("retargeted symlink approval was accepted: %+v", result)
	}
}

func TestNativeAuthorityApprovalRejectsExpiredAndUnavailableChannels(t *testing.T) {
	fixture := newNativeApprovalFixture(t)
	expired := nativeApprovalPayload(t, fixture, "call-expired", "expired", time.Now().UTC().Add(-2*time.Minute))
	result := RunPreToolUse(fixture.repo, expired)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "expired") {
		t.Fatalf("expired approval was accepted: %+v", result)
	}

	validIssued := time.Now().UTC()
	valid := nativeApprovalPayload(t, fixture, "call-channel", "channel", validIssued)
	t.Setenv(nativeApprovalAuthoritiesEnv, "")
	result = RunPreToolUse(fixture.repo, valid)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "operator approval channel is unavailable") {
		t.Fatalf("missing approval channel was not reported precisely: %+v", result)
	}
}

func TestNativeAuthorityApprovalCoversClassifiedMCPRepositoryWrites(t *testing.T) {
	fixture := newNativeApprovalFixture(t)
	policy := `rules:
  - id: authority-files
    template: authority-change-approval
    when_paths: ['AGENTS.md']
mcp:
  unclassified: deny
  tools:
    - platform: cursor
      tool: write_repo
      effect: repository_write
      path_fields: [/file_path]
`
	if err := os.WriteFile(filepath.Join(fixture.repo, ".reconc.yml"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(fixture.repo, "test"); err != nil {
		t.Fatalf("compile MCP policy: %v", err)
	}
	raw := map[string]interface{}{
		"session_id":  fixture.sessionID,
		"tool_use_id": "call-mcp-approval",
		"tool_name":   "Write",
		"tool_input": map[string]interface{}{
			"file_path": "AGENTS.md",
			"content":   "mcp-approved",
		},
		"reconc_mcp": map[string]interface{}{
			"platform":          "cursor",
			"tool":              "write_repo",
			"blocking_pre_hook": true,
			"input_valid":       true,
		},
	}
	approved := nativeApprovalPayloadForRaw(t, fixture, raw, []string{"AGENTS.md"}, time.Now().UTC())
	result := RunMCPBefore(fixture.repo, approved)
	if result.ExitCode != 0 {
		t.Fatalf("exact MCP approval was not accepted: %+v", result)
	}
	result = RunMCPBefore(fixture.repo, approved)
	if result.ExitCode != 2 {
		t.Fatalf("MCP approval replay was accepted: %+v", result)
	}
}

func TestNativeAuthorityApprovalBlocksCommandMediatedAuthorityWrites(t *testing.T) {
	fixture := newNativeApprovalFixture(t)
	if _, err := RecordClaim(fixture.repo, productruntime.BoundApprovalClaim, fixture.sessionID); err != nil {
		t.Fatalf("record self-authored claim: %v", err)
	}
	payload := []byte(fmt.Sprintf(`{"session_id":%q,"tool_use_id":"call-shell-write","tool_name":"Bash","tool_input":{"command":"printf changed > AGENTS.md"}}`, fixture.sessionID))
	result := RunPreToolUse(fixture.repo, payload)
	if result.ExitCode != 2 {
		t.Fatalf("command-mediated authority write was allowed: %+v", result)
	}
	assertNativeAuthorityContent(t, fixture.repo, "original\n")
	raw := map[string]interface{}{
		"session_id":         fixture.sessionID,
		"tool_use_id":        "call-shell-approved",
		"tool_name":          "Bash",
		"reconc_write_paths": []interface{}{"AGENTS.md"},
		"tool_input": map[string]interface{}{
			"command": "printf changed > AGENTS.md",
		},
	}
	approved := nativeApprovalPayloadForRaw(t, fixture, raw, []string{"AGENTS.md"}, time.Now().UTC())
	result = RunPreToolUse(fixture.repo, approved)
	if result.ExitCode != 0 {
		t.Fatalf("exact command approval was not accepted: %+v", result)
	}
}

func TestNativeAuthorityApprovalDoesNotBypassCompositeWriteChecks(t *testing.T) {
	fixture := newNativeApprovalFixture(t)
	policy := `rules:
  - id: authority-composite
    kind: all_of
    when_paths: ['AGENTS.md']
    checks:
      - kind: require_claim
        claims: ['authority-change-approved']
      - kind: deny_write
        paths: ['AGENTS.md']
    mode: block
    message: authority composite
`
	if err := os.WriteFile(filepath.Join(fixture.repo, ".reconc.yml"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(fixture.repo, "test"); err != nil {
		t.Fatalf("compile composite policy: %v", err)
	}
	approved := nativeApprovalPayload(t, fixture, "call-composite", "composite", time.Now().UTC())
	result := RunPreToolUse(fixture.repo, approved)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "authority-composite") {
		t.Fatalf("approval receipt bypassed composite write check: %+v", result)
	}
	assertNativeAuthorityContent(t, fixture.repo, "original\n")
}

func TestNativeAuthorityApprovalCannotUsePreDecisionCache(t *testing.T) {
	fixture := newNativeApprovalFixture(t)
	payloadBody := []byte(fmt.Sprintf(`{"session_id":%q,"tool_use_id":"call-cached-shell","tool_name":"Bash","tool_input":{"command":"printf changed > AGENTS.md"}}`, fixture.sessionID))
	payload, err := ParsePayload(payloadBody)
	if err != nil {
		t.Fatal(err)
	}
	inputs, cacheable := preDecisionInputsForPayload(fixture.repo, payload)
	if !cacheable {
		t.Fatal("command payload was unexpectedly not cacheable before the guarded decision")
	}
	if err := writePreDecisionCacheForPayload(fixture.repo, payload, inputs.key, Result{ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRootRef(fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payloadBody)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "signed pre-action receipt") {
		t.Fatalf("cached allow bypassed the authority receipt gate: %+v", result)
	}
}

func TestCommandWriteClassificationFailsClosedForExecutableOrOutputFlags(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: "cat AGENTS.md", want: false},
		{command: "git status --short", want: false},
		{command: "go env GOMOD", want: false},
		{command: "go test ./...", want: true},
		{command: "cargo check", want: true},
		{command: "git diff --output=AGENTS.md", want: true},
		{command: "rg --pre touch AGENTS.md .", want: true},
		{command: "reconc check .", want: true},
	}
	for _, test := range tests {
		if got := commandMayWriteRepository(test.command); got != test.want {
			t.Errorf("commandMayWriteRepository(%q)=%t, want %t", test.command, got, test.want)
		}
	}
}

func assertNativeAuthorityContent(t *testing.T, repo, want string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != want {
		t.Fatalf("authority file content=%q, want %q", body, want)
	}
}
