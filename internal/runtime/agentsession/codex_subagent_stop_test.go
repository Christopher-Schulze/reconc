package agentsession

import (
	"reflect"
	"strings"
	"testing"
)

func TestCodexSubagentOverflowRetainsVerifiedEvidenceChain(t *testing.T) {
	repo := setupPolicyRepo(t)
	if _, err := MutateSessionState(repo, "child", func(state SessionState) SessionState {
		return AppendReadPath(state, "README.md")
	}); err != nil {
		t.Fatal(err)
	}
	before, err := MutateSessionState(repo, "child", func(state SessionState) SessionState {
		return AppendCommand(state, strings.Repeat("x", maxCommandBytes+1))
	})
	if err != nil || !before.EvidenceOverflow || before.EvidenceSegmentCount != 1 {
		t.Fatalf("real overflow did not rotate retained evidence: %+v error=%v", before, err)
	}
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	body, err := NormalizeCodexPayload("codex-subagent-stop", []byte(`{"session_id":"parent","agent_id":"child","hook_event_name":"SubagentStop"}`))
	if err != nil {
		t.Fatal(err)
	}
	result := RunHookRequest(root, HookHandlerCodexSubagentStop, "codex-subagent-stop", body)
	if result.ExitCode != 0 || result.Stdout != "" || !strings.Contains(result.Stderr, "uncertified") {
		t.Fatalf("overflow turn did not release as uncertified: %+v", result)
	}
	after, err := LoadSessionState(repo, "child")
	if err != nil || !after.UncertifiedTermination || !after.EvidenceOverflow || after.EvidenceSegmentCount != before.EvidenceSegmentCount || after.EvidenceSegmentDigest != before.EvidenceSegmentDigest {
		t.Fatalf("overflow state or chain head changed: %+v error=%v", after, err)
	}
	complete, err := loadCompleteSessionEvidence(root.Path(), after)
	if err != nil || !reflect.DeepEqual(complete.ReadPaths, []string{"README.md"}) {
		t.Fatalf("persisted evidence chain lost its pre-overflow read: %+v error=%v", complete, err)
	}
}
