package agentsession

import (
	"os"
	"testing"
)

// A pre-hook that misses the decision cache verifies the sealed evidence
// chain only once: evaluation and the publication resample reuse that
// verified prefix, and a host no later event can replay skips the cache.
func TestUniquePreHookLoadsTheEvidenceChainOnce(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	const segments = 8
	state := writeEvidenceChainFixture(t, repo, "single-chain-load", segments, 64)
	if err := SaveSessionState(state); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	previous := readEvidenceSegmentSnapshot
	t.Cleanup(func() { readEvidenceSegmentSnapshot = previous })
	readEvidenceSegmentSnapshot = func(path string, maxBytes int64) ([]byte, os.FileInfo, error) {
		reads++
		return previous(path, maxBytes)
	}
	for _, test := range []struct {
		event     string
		id        string
		publishes bool
	}{
		// Kimi's PermissionRequest repeats tool_call_id, so its decision is
		// cached; Claude's carries no tool_use_id, so nothing is sampled.
		{event: "kimi-pre-tool-use", id: "call-1", publishes: true},
		{event: "kimi-pre-tool-use", id: "call-2", publishes: true},
		{event: "claude-pre-tool-use", id: "call-3"},
	} {
		reads = 0
		payload := []byte(`{"session_id":"single-chain-load","tool_use_id":"` + test.id + `","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`)
		cachePath := preDecisionCachePath(root.Path(), payload)
		if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if result := RunHookRequest(root, HookHandlerPreToolUse, test.event, payload); result.ExitCode != 0 {
			t.Fatalf("%s %s failed: %+v", test.event, test.id, result)
		}
		if reads != segments {
			t.Fatalf("%s %s read %d evidence segments, want one chain load of %d", test.event, test.id, reads, segments)
		}
		if _, err := os.Stat(cachePath); (err == nil) != test.publishes {
			t.Fatalf("%s %s published a decision cache = %v, want %v", test.event, test.id, err == nil, test.publishes)
		}
	}
}
