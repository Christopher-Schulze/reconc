package retention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRepositoryRuntimeBudgetRemovesOldestInactiveOwnedArtifacts(t *testing.T) {
	repo := t.TempDir()
	now := time.Now().UTC()
	cache := filepath.Join(repo, ".reconc", "cache")
	oldBinary := filepath.Join(cache, "workflow-audit-old")
	activeBinary := filepath.Join(cache, "workflow-audit-active")
	auditArchive := filepath.Join(repo, ".reconc", "audit.jsonl.1")
	current := filepath.Join(repo, ".reconc", "audit.jsonl")
	decisionArchive := filepath.Join(repo, ".reconc", "run", "decisions.jsonl.1")
	for path, body := range map[string]string{
		oldBinary: "old-binary", activeBinary: "active-binary",
		auditArchive: "audit-archive", current: "current",
		decisionArchive: "decision-archive",
	} {
		writeTimed(t, path, []byte(body), now.Add(-4*time.Hour))
	}
	writeTimed(t, activeBinary+".build.lock", nil, now)
	policy := DefaultPolicy()
	policy.RepoRuntimeBytes = int64(len("active-binary") + len("current"))
	policy.AbandonedTempAge = time.Hour
	options := Options{RepoRoot: repo, Policy: policy, Now: now}
	report := Report{}
	class := enforceRepoTotal(options, &report)
	if len(report.Errors) != 0 {
		t.Fatalf("runtime budget errors = %v", report.Errors)
	}
	// The inactive generated binary and the plain run-decision archive are
	// removable. The chained audit archive is protected even though it is
	// old and would free space: deleting it would break the audit hash
	// chain and permanently fail every audit operation.
	wantFreed := int64(len("old-binary") + len("decision-archive"))
	if class.BytesFreed != wantFreed {
		t.Fatalf("expected %d bytes freed from the binary and decision archive, got %+v", wantFreed, class)
	}
	for _, path := range []string{oldBinary, decisionArchive} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("old removable artifact survived: %s: %v", path, err)
		}
	}
	for _, path := range []string{activeBinary, current, auditArchive} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected runtime artifact was removed: %s: %v", path, err)
		}
	}
	if active, err := generatedBinaryBuildActive(cache, "missing", now, time.Hour); err != nil || active {
		t.Fatalf("missing build lock = %t, %v", active, err)
	}
	if active, err := generatedBinaryBuildActive(cache, "workflow-audit-active", now, time.Hour); err != nil || !active {
		t.Fatalf("fresh build lock = %t, %v", active, err)
	}
	if err := os.Chtimes(activeBinary+".build.lock", now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if active, err := generatedBinaryBuildActive(cache, "workflow-audit-active", now, time.Hour); err != nil || active {
		t.Fatalf("stale build lock = %t, %v", active, err)
	}
}

func TestRepositoryRuntimeBudgetNeverRemovesAuditArchives(t *testing.T) {
	repo := t.TempDir()
	now := time.Now().UTC()
	auditArchive := filepath.Join(repo, ".reconc", "audit.jsonl.1")
	decisionArchive := filepath.Join(repo, ".reconc", "run", "decisions.jsonl.1")
	writeTimed(t, auditArchive, []byte("audit-archive"), now.Add(-4*time.Hour))
	writeTimed(t, decisionArchive, []byte("decision-archive"), now.Add(-3*time.Hour))
	policy := DefaultPolicy()
	policy.RepoRuntimeBytes = 0
	report := Report{}
	class := enforceRepoTotal(Options{RepoRoot: repo, Policy: policy, Now: now}, &report)
	if len(report.Errors) != 0 {
		t.Fatalf("runtime budget errors = %v", report.Errors)
	}
	if class.FilesDeleted != 1 || class.BytesFreed != int64(len("decision-archive")) {
		t.Fatalf("runtime budget removed the wrong artifacts: %+v", class)
	}
	if _, err := os.Stat(auditArchive); err != nil {
		t.Fatalf("audit archive was removed by generic retention: %v", err)
	}
	if _, err := os.Stat(decisionArchive); !os.IsNotExist(err) {
		t.Fatalf("run-decision archive survived: %v", err)
	}
}

func TestAbandonedTempContractsCoverFilesBuildLocksAndOwnedRoots(t *testing.T) {
	repo := t.TempDir()
	tempRoot := t.TempDir()
	now := time.Now().UTC()
	policy := DefaultPolicy()
	policy.AbandonedTempAge = time.Hour
	options := Options{RepoRoot: repo, TempRoot: tempRoot, Policy: policy, Now: now}

	oldFile := filepath.Join(repo, ".reconc", "nested", "state.tmp.1")
	recentFile := filepath.Join(repo, ".reconc", "nested", ".audit-jsonl-recent")
	unowned := filepath.Join(repo, ".reconc", "nested", "operator.txt")
	buildLock := filepath.Join(repo, ".reconc", "cache", "workflow.build.lock")
	writeTimed(t, oldFile, []byte("old"), now.Add(-2*time.Hour))
	writeTimed(t, recentFile, []byte("recent"), now.Add(-30*time.Minute))
	writeTimed(t, unowned, []byte("keep"), now.Add(-24*time.Hour))
	writeTimed(t, filepath.Join(buildLock, "owner"), []byte("lock"), now.Add(-2*time.Hour))
	if err := os.Chtimes(buildLock, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	report := Report{}
	class := pruneRepoTemps(options, &report)
	if len(report.Errors) != 0 || class.FilesDeleted != 2 || class.FilesKept != 1 {
		t.Fatalf("repo temp pruning = %+v, errors=%v", class, report.Errors)
	}
	for _, path := range []string{oldFile, buildLock} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale owned temp survived: %s: %v", path, err)
		}
	}
	for _, path := range []string{recentFile, unowned} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected temp was removed: %s: %v", path, err)
		}
	}

	oldRoot := filepath.Join(tempRoot, "reconc-proof-neg-copy-old")
	recentRoot := filepath.Join(tempRoot, "reconc-proof-gocache-recent")
	foreignRoot := filepath.Join(tempRoot, "foreign")
	for path, age := range map[string]time.Duration{oldRoot: 2 * time.Hour, recentRoot: 30 * time.Minute, foreignRoot: 24 * time.Hour} {
		writeTimed(t, filepath.Join(path, "payload"), []byte("data"), now.Add(-age))
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	class = pruneOwnedTempRoots(options, &report)
	if len(report.Errors) != 0 || class.FilesDeleted != 1 || class.FilesKept != 1 {
		t.Fatalf("owned temp pruning = %+v, errors=%v", class, report.Errors)
	}
	if _, err := os.Stat(oldRoot); !os.IsNotExist(err) {
		t.Fatalf("old owned root survived: %v", err)
	}
	for _, path := range []string{recentRoot, foreignRoot} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected root removed: %s: %v", path, err)
		}
	}
}

func TestStateBudgetPreservesActiveAndLatestReceiptsDuringDryRun(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	activeID := "active"
	for _, path := range []string{
		filepath.Join(project, "sessions", activeID+".json"),
		filepath.Join(project, "reports", activeID+".json"),
		filepath.Join(project, "locks", activeID+".lock"),
		filepath.Join(project, "locks", activeID+".stop-policy.lock"),
		filepath.Join(project, "policy-decisions", "latest.json"),
	} {
		writeTimed(t, path, []byte(strings.Repeat("x", 16)), now.Add(-24*time.Hour))
	}
	old := filepath.Join(project, "command-proofs", "old.json")
	writeTimed(t, old, []byte(strings.Repeat("y", 64)), now.Add(-48*time.Hour))
	policy := DefaultPolicy()
	policy.StateTotalBytes = 1
	options := Options{Policy: policy, Now: now, DryRun: true}
	report := Report{}
	class := enforceStateTotal(options, project, activeID, true, taintResolutionProtection{}, &report)
	if len(report.Errors) != 0 || class.FilesDeleted != 1 || class.FilesKept != 5 {
		t.Fatalf("state budget = %+v, errors=%v", class, report.Errors)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("dry run removed projected candidate: %v", err)
	}
	if class.BytesAfter >= class.BytesBefore || class.BytesFreed != 64 {
		t.Fatalf("dry-run projection = %+v", class)
	}
}

func TestTreeSizingAndCandidateExpirationAreDeterministic(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	writeTimed(t, filepath.Join(root, "a"), []byte("123"), now.Add(-2*time.Hour))
	writeTimed(t, filepath.Join(root, "nested", "b"), []byte("4567"), now.Add(-time.Hour))
	size, latest, err := treeSizeAndLatest(root, now.Add(-3*time.Hour))
	if err != nil || size != 7 || latest.Before(now.Add(-time.Hour)) {
		t.Fatalf("treeSizeAndLatest() = %d, %s, %v", size, latest, err)
	}
	report := Report{}
	path := filepath.Join(root, "a")
	class := pruneExpiredCandidates(ClassReport{Name: "test"}, []candidate{{
		path: path, name: "a", size: 3, mtime: now.Add(-2 * time.Hour),
	}}, now, 0, false, &report)
	if class.FilesDeleted != 0 || class.FilesKept != 1 {
		t.Fatalf("disabled expiration = %+v", class)
	}
}
