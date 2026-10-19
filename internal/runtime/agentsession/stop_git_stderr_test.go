//go:build !windows

package agentsession

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitStatusDiagnosticsNeverMergeIntoPorcelainRecords(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read the locked directory")
	}
	repo := setupPolicyRepo(t)
	gitInitHelper(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "initial"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(repo, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	t.Setenv(stopPolicyUntrackedModeEnv, "all")

	snapshot := stopPolicyGitSnapshotFor(repo)
	if snapshot.StatusOK || !strings.HasPrefix(snapshot.Status, "error:") {
		t.Fatalf("an observation with Git diagnostics was treated as complete: %#v", snapshot)
	}
	// The diagnostic stays on the error line; the porcelain records after it
	// must be exactly Git's stdout.
	_, records, _ := strings.Cut(snapshot.Status, "\n")
	if strings.Contains(records, "warning:") || !strings.HasPrefix(records, " M initial\x00") {
		t.Fatalf("Git diagnostics merged into porcelain records: %q", records)
	}
}
