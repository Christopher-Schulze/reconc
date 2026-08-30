package retention

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/privatefs"
)

func TestEvidenceSegmentRetentionProtectsActiveAndUnresolvedChains(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	activeID := "active-evidence"
	taintedID := "tainted-evidence"
	abandonedID := "abandoned-evidence"
	activeDir := writeEvidenceDirectoryFixture(t, project, activeID, 2, 32, now.Add(-72*time.Hour))
	taintedDir := writeEvidenceDirectoryFixture(t, project, taintedID, 2, 32, now.Add(-72*time.Hour))
	abandonedDir := writeEvidenceDirectoryFixture(t, project, abandonedID, maxEvidenceSegmentsPerSession, 32, now.Add(-72*time.Hour))
	taint, _ := writeTaintResolutionFixture(t, project, repo, taintedID, now.Add(-72*time.Hour))
	writeJSONFixture(t, filepath.Join(project, "evidence-taint.json"), taint, now)

	policy := DefaultPolicy()
	policy.EvidenceSegments = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.StateTotalBytes = maxEvidenceChainBytes
	report := Run(Options{
		RepoRoot: repo, StateRoot: stateRoot, ActiveSession: activeID,
		Policy: policy, Now: now, TempRoot: t.TempDir(),
	})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors: %v", report.Errors)
	}
	for _, path := range []string{activeDir, taintedDir} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected evidence chain removed: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(abandonedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned 64-segment chain survived pressure: %v", err)
	}
	class := classReportByName(t, report, "evidence-segments")
	if class.FilesDeleted != 1 || class.FilesKept != 2 {
		t.Fatalf("evidence retention report = %+v", class)
	}
}

func TestEvidenceSegmentRetentionEnforcesCountAndBytesByWholeChain(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy ClassPolicy
	}{
		{name: "count", policy: ClassPolicy{MaxFiles: 1, MaxBytes: 1 << 20}},
		{name: "bytes", policy: ClassPolicy{MaxFiles: 8, MaxBytes: 100}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			stateRoot := t.TempDir()
			now := time.Now().UTC()
			project := ProjectDir(stateRoot, repo)
			oldDir := writeEvidenceDirectoryFixture(t, project, "old", 2, 80, now.Add(-2*time.Hour))
			newDir := writeEvidenceDirectoryFixture(t, project, "new", 2, 80, now.Add(-time.Hour))
			policy := DefaultPolicy()
			policy.EvidenceSegments = test.policy
			policy.StateTotalBytes = maxEvidenceChainBytes
			report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
			if len(report.Errors) != 0 {
				t.Fatalf("retention errors: %v", report.Errors)
			}
			if _, err := os.Stat(oldDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("old whole chain survived: %v", err)
			}
			_, newErr := os.Stat(newDir)
			if test.name == "count" && newErr != nil {
				t.Fatalf("newest chain removed under count policy: %v", newErr)
			}
			if test.name == "bytes" && !errors.Is(newErr, os.ErrNotExist) {
				t.Fatalf("over-byte chain survived: %v", newErr)
			}
		})
	}
}

func TestEvidenceSegmentRetentionPreservesMalformedDirectory(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	path := filepath.Join(project, "evidence", "malformed")
	writeTimed(t, filepath.Join(path, "00000002.json"), []byte("out-of-order"), now.Add(-72*time.Hour))
	policy := DefaultPolicy()
	policy.EvidenceSegments = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if len(report.Errors) == 0 {
		t.Fatalf("malformed evidence directory was not reported: %+v", report)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("malformed evidence directory was removed: %v", err)
	}
}

func TestEvidenceSegmentRetentionHonorsConcurrentSessionLease(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	path := writeEvidenceDirectoryFixture(t, project, "leased", 2, 32, now.Add(-72*time.Hour))
	lockPath := filepath.Join(project, "locks", SessionFileID("leased")+".lock")
	if err := privatefs.RepairDirectory(filepath.Dir(lockPath)); err != nil {
		t.Fatal(err)
	}
	lock, err := privatefs.OpenLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := filelock.TryLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unlock()
		_ = lock.Close()
	})
	policy := DefaultPolicy()
	policy.EvidenceSegments = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.StateTotalBytes = maxEvidenceChainBytes
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors: %v", report.Errors)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("leased evidence chain was removed: %v", err)
	}
}

func writeEvidenceDirectoryFixture(
	t testing.TB,
	project, sessionID string,
	segments, segmentBytes int,
	modTime time.Time,
) string {
	t.Helper()
	dir := filepath.Join(project, "evidence", SessionFileID(sessionID))
	for index := 1; index <= segments; index++ {
		body := []byte(fmt.Sprintf("%08d:%s", index, string(make([]byte, segmentBytes))))
		writeTimedTB(t, filepath.Join(dir, fmt.Sprintf("%08d.json", index)), body, modTime)
	}
	if err := os.Chtimes(dir, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeTimedTB(t testing.TB, path string, body []byte, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}
