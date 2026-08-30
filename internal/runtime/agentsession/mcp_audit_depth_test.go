package agentsession

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestMCPAuditRoundTripIsRedactedBoundedAndDeterministic(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	envelope := &MCPPayload{
		Platform:          policy.MCPPlatformCursor,
		Tool:              "read_file",
		ServerFingerprint: "sha256:" + strings.Repeat("a", 64),
		Phase:             MCPPhaseBefore,
		BlockingPreHook:   true,
	}
	if err := recordMCPAuditResolved(root, nil, "", "", false); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil envelope error = %v", err)
	}
	if err := recordMCPAuditResolved(root, envelope, policy.MCPEffectRepositoryRead, "", true); err != nil {
		t.Fatal(err)
	}
	if err := recordMCPAuditResolved(root, envelope, policy.MCPEffectRepositoryRead, "denied", true); err != nil {
		t.Fatal(err)
	}
	failure := &MCPPayload{Platform: policy.MCPPlatformOpenCode, Tool: "execute", Phase: MCPPhaseAfter}
	if err := recordMCPAuditResolved(root, failure, "", "failure", false); err != nil {
		t.Fatal(err)
	}

	summary, err := ReadMCPAudit(repo)
	if err != nil {
		t.Fatal(err)
	}
	wantKey := "cursor/repository_read"
	if summary.Classified[wantKey] != 2 || summary.Unclassified["opencode"] != 1 ||
		summary.Denied["cursor"] != 1 || summary.Failures["opencode"] != 1 ||
		summary.StrictUnavailable["cursor"] != 0 || summary.StrictUnavailable["opencode"] != 1 {
		t.Fatalf("summary counters = %+v", summary)
	}
	if got := strings.Join(SortedMCPClassifiedCounts(summary), ","); got != wantKey+"=2" {
		t.Fatalf("sorted counts = %q", got)
	}
	body, err := os.ReadFile(mcpAuditPath(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"read_file", "execute"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("audit leaked raw selector material %q: %s", secret, body)
		}
	}
	info, err := os.Stat(mcpAuditPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("audit is not a regular file: %v", info.Mode())
	}
	if mode := info.Mode().Perm(); runtime.GOOS != "windows" && mode != 0o600 {
		t.Fatalf("audit mode = %04o, want 0600", mode)
	}
}

func TestMCPAuditDerivesStrictCapabilityFromNormalizedRoute(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	parseNative := func(event string) *MCPPayload {
		t.Helper()
		envelope := newNativeMCPEnvelope(
			policy.MCPPlatformPi,
			"deploy_preview",
			json.RawMessage(`{"target":"preview"}`),
			event,
			"pi-post-tool-use",
			"pi-post-tool-use-failure",
		)
		body, err := json.Marshal(map[string]interface{}{
			"session_id": "pi-session",
			"reconc_mcp": mcpEnvelopeToMap(*envelope),
		})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := ParsePayload(body)
		if err != nil {
			t.Fatal(err)
		}
		return payload.MCP
	}

	before := parseNative("pi-pre-tool-use")
	after := parseNative("pi-post-tool-use")
	if err := recordMCPAuditResolved(root, before, policy.MCPEffectExternal, "allowed", true); err != nil {
		t.Fatal(err)
	}
	if err := recordMCPAuditResolved(root, after, policy.MCPEffectExternal, after.Outcome, true); err != nil {
		t.Fatal(err)
	}
	nonBlocking := &MCPPayload{
		Platform: policy.MCPPlatformOpenCode,
		Tool:     "host_observation",
		Phase:    MCPPhaseAfter,
	}
	if err := recordMCPAuditResolved(root, nonBlocking, "", "observed", false); err != nil {
		t.Fatal(err)
	}

	summary, err := ReadMCPAudit(repo)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Classified["pi/external"] != 2 || summary.Failures["pi"] != 0 {
		t.Fatalf("native counters = %#v / %#v", summary.Classified, summary.Failures)
	}
	if summary.StrictUnavailable["pi"] != 0 || summary.StrictUnavailable["opencode"] != 1 {
		t.Fatalf("strict capability counters = %#v", summary.StrictUnavailable)
	}
	if before.Phase != MCPPhaseBefore || after.Phase != MCPPhaseAfter ||
		summary.Events[0].SelectorHash != summary.Events[1].SelectorHash {
		t.Fatalf("native phase/identity = %q/%q selectors=%q/%q", before.Phase, after.Phase, summary.Events[0].SelectorHash, summary.Events[1].SelectorHash)
	}
}

func TestMCPAuditReaderFailsClosedOnEveryInvalidPersistenceShape(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if summary, err := ReadMCPAudit(repo); err != nil || len(summary.Events) != 0 || summary.Classified == nil {
		t.Fatalf("missing audit = %+v, %v", summary, err)
	}
	path := mcpAuditPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{name: "malformed", body: []byte("{"), want: "invalid JSON"},
		{name: "oversized", body: make([]byte, maxMCPAuditBytes+1), want: "exceeds"},
		{name: "too many entries", body: marshalMCPAudit(t, MCPAuditSummary{Events: make([]MCPAuditEntry, maxMCPAuditEntries+1)}), want: "entries"},
		{name: "invalid platform", body: marshalMCPAudit(t, MCPAuditSummary{Events: []MCPAuditEntry{{Platform: "other", SelectorHash: strings.Repeat("a", 64), Outcome: "observed"}}}), want: "invalid entry"},
		{name: "invalid digest", body: marshalMCPAudit(t, MCPAuditSummary{Events: []MCPAuditEntry{{Platform: policy.MCPPlatformCursor, SelectorHash: strings.Repeat("z", 64), Outcome: "observed"}}}), want: "selector hash"},
		{name: "invalid phase", body: marshalMCPAudit(t, MCPAuditSummary{Events: []MCPAuditEntry{{Platform: policy.MCPPlatformCursor, SelectorHash: strings.Repeat("a", 64), Outcome: "observed", Phase: "during"}}}), want: "invalid entry"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, test.body, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadMCPAudit(repo); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReadMCPAudit() error = %v, want substring %q", err, test.want)
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMCPAudit(repo); err == nil || !strings.Contains(err.Error(), "read MCP audit") {
		t.Fatalf("directory audit error = %v", err)
	}
}

func TestMCPAuditRetentionAndSaturatingCounters(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxMCPAuditEntries+3; index++ {
		entry := MCPAuditEntry{
			Platform:     policy.MCPPlatformKilo,
			SelectorHash: strings.Repeat("a", 64),
			Effect:       policy.MCPEffectExternal,
			Outcome:      "observed",
			Classified:   true,
		}
		if err := appendMCPAuditLocked(root, entry); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := ReadMCPAudit(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Events) != maxMCPAuditEntries || summary.Classified["kilo/external"] != maxMCPAuditEntries+3 {
		t.Fatalf("bounded summary = %+v", summary)
	}
	if got := saturatingIncrement(math.MaxUint64); got != math.MaxUint64 {
		t.Fatalf("saturatingIncrement(max) = %d", got)
	}
	if got := saturatingIncrement(41); got != 42 {
		t.Fatalf("saturatingIncrement(41) = %d", got)
	}
}

func marshalMCPAudit(t *testing.T, summary MCPAuditSummary) []byte {
	t.Helper()
	body, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
