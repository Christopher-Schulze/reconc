package agentsession

import "testing"

func TestStopPolicyAttemptSnapshotCapturesSourceIdentityOncePerPhase(t *testing.T) {
	t.Parallel()
	loads := 0
	loadSourceIdentity := func(string) (string, int, error) {
		loads++
		if loads == 1 {
			return "before-digest", 2, nil
		}
		return "after-digest", 3, nil
	}
	state := emptyState("", "snapshot-count")
	scanCache := &stopPolicyScanCache{}
	before := captureStopPolicyAttemptSnapshotWithSourceIdentity(
		"", state, "revision-before", stopTaskSnapshot{}, stopPolicyGitSnapshot{}, scanCache,
		loadSourceIdentity,
	)
	if loads != 1 {
		t.Fatalf("before phase loaded policy source identity %d times, want 1", loads)
	}
	for range 3 {
		input := stopPolicyFingerprintInputForSnapshotWithScan(
			"", state, before.Git, before.Task, before.generationCapture(), scanCache,
		)
		if input.PolicySourceDigest != "before-digest" || input.PolicySourceCount != 2 {
			t.Fatalf("before snapshot identity drifted: %+v", input)
		}
	}
	if loads != 1 {
		t.Fatalf("before phase consumers recaptured policy source identity: loads=%d", loads)
	}

	after := captureStopPolicyAttemptSnapshotWithSourceIdentity(
		"", state, "revision-after", stopTaskSnapshot{}, stopPolicyGitSnapshot{}, scanCache,
		loadSourceIdentity,
	)
	if loads != 2 {
		t.Fatalf("after phase cumulative source identity loads=%d, want 2", loads)
	}
	if before.PolicyDigest == after.PolicyDigest || before.PolicyCount == after.PolicyCount {
		t.Fatalf("phase mutation was not represented: before=%+v after=%+v", before, after)
	}
}
