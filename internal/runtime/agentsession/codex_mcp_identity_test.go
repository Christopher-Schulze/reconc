package agentsession

import (
	"fmt"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestNamespacedMCPPreservesDistinctCallsAndDeduplicatesRepeatedDelivery(t *testing.T) {
	for _, platform := range []policy.MCPPlatform{policy.MCPPlatformCodex, policy.MCPPlatformClaudeCode} {
		t.Run(string(platform), func(t *testing.T) {
			t.Setenv(StateRootEnv, t.TempDir())
			repo := setupNamespacedMCPAuditRepo(t)
			for index, id := range []string{"call-one", "call-one", "call-two"} {
				body := []byte(fmt.Sprintf(`{"session_id":"mcp-identity","tool_use_id":%q,"tool_name":"mcp__filesystem__write_file","tool_input":{"path":"docs/out.md"},"tool_response":{"isError":false}}`, id))
				normalized, err := NormalizeNamespacedMCPPayload(platform, false, body)
				if err != nil {
					t.Fatal(err)
				}
				if result := RunMCPAfter(repo, normalized); result.ExitCode != 0 || result.Stderr != "" {
					t.Fatalf("record MCP completion: %+v", result)
				}
				state, err := LoadSessionState(repo, "mcp-identity")
				want := uint64(1)
				if index == 2 {
					want = 2
				}
				if err != nil || state.MaterialEvents != want || state.EvidenceEpoch != want {
					t.Fatalf("delivery %d collapsed distinct calls or replayed a write: material=%d epoch=%d want=%d error=%v", index, state.MaterialEvents, state.EvidenceEpoch, want, err)
				}
			}
		})
	}
}

func TestNamespacedMCPRejectsAmbiguousCallIdentity(t *testing.T) {
	for _, field := range []string{
		`"tool_use_id":"first","tool_use_id":"second"`,
		`"tool_use_id":42`,
		`"tool_use_id":""`,
		`"tool_use_id":" padded "`,
	} {
		body := []byte(`{"session_id":"s","tool_name":"mcp__files__write","tool_input":{},` + field + `}`)
		if _, err := NormalizeNamespacedMCPPayload(policy.MCPPlatformCodex, true, body); err == nil {
			t.Fatalf("ambiguous identity accepted: %s", field)
		}
	}
}
