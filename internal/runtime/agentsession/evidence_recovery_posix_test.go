//go:build !windows

package agentsession

import (
	"os"
	"testing"
)

func TestEvidenceRecoveryCanRetryAfterPartiallyCompletedCleanup(t *testing.T) {
	fixture, state, _ := damagedCommandRecoveryFixture(t, true)
	directory := evidenceSegmentsDir(fixture.repo, state.SessionID)
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(directory, 0o700); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
	status, err := ReadEvidenceTaintStatus(fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEvidenceTaint(fixture.repo, status.Token, "reviewed cleanup"); err == nil {
		t.Fatal("cleanup unexpectedly removed files from a read-only directory")
	}
	raw, err := readSessionStateResolved(fixture.repo, fixture.sessionID)
	if err != nil || len(raw.Commands) != 0 || raw.EvidenceSegmentCount != 0 {
		t.Fatalf("fresh window was not published before owned cleanup: %+v %v", raw, err)
	}
	if taint, err := ReadEvidenceTaintStatus(fixture.repo); err != nil || !taint.Present || taint.Token != status.Token {
		t.Fatalf("partial cleanup unsealed the project: %+v %v", taint, err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEvidenceTaint(fixture.repo, status.Token, "retry completed cleanup"); err != nil {
		t.Fatal(err)
	}
}
