package retention

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRunEnforcesClassesAndPreservesLiveSession(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	policy := DefaultPolicy()
	policy.Sessions = ClassPolicy{MaxFiles: 2, MaxBytes: 64, MaxAge: 24 * time.Hour}
	policy.Reports = ClassPolicy{MaxFiles: 2, MaxBytes: 64, MaxAge: 24 * time.Hour}
	policy.Locks = ClassPolicy{MaxFiles: 2, MaxBytes: 64, MaxAge: 24 * time.Hour}
	policy.StateTotalBytes = 128
	active := "active-session"
	for _, class := range []string{"sessions", "reports"} {
		writeTimed(t, filepath.Join(project, class, active+".json"), []byte("active"), now.Add(-48*time.Hour))
		for index := 0; index < 5; index++ {
			writeTimed(t, filepath.Join(project, class, fmt.Sprintf("old-%d.json", index)), []byte("01234567890123456789"), now.Add(time.Duration(index-10)*time.Hour))
		}
	}
	writeTimed(t, filepath.Join(project, "locks", active+".lock"), nil, now)
	for index := 0; index < 5; index++ {
		writeTimed(t, filepath.Join(project, "locks", fmt.Sprintf("old-%d.lock", index)), []byte("lock"), now.Add(-48*time.Hour))
	}
	// A recent session file makes the explicit active id live even though the
	// active report itself is deliberately old.
	if err := os.Chtimes(filepath.Join(project, "sessions", active+".json"), now, now); err != nil {
		t.Fatal(err)
	}

	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, ActiveSession: active, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if !report.Ran || len(report.Errors) != 0 {
		t.Fatalf("retention report: %+v", report)
	}
	for _, path := range []string{
		filepath.Join(project, "sessions", active+".json"),
		filepath.Join(project, "reports", active+".json"),
		filepath.Join(project, "locks", active+".lock"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("active file removed: %s: %v", path, err)
		}
	}
	if report.StateBytesAfter > policy.StateTotalBytes {
		t.Fatalf("state total not enforced: %d > %d", report.StateBytesAfter, policy.StateTotalBytes)
	}
}

func TestRunCoversLogsBinariesAndOwnedTemp(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	tempRoot := t.TempDir()
	now := time.Now().UTC()
	policy := DefaultPolicy()
	policy.AuditFileBytes = 256
	policy.AuditArchives = 1
	policy.RunLoopFileBytes = 256
	policy.RunLoopArchives = 1
	policy.GeneratedBinaries = ClassPolicy{MaxFiles: 1, MaxBytes: 64, MaxAge: 24 * time.Hour}
	policy.AbandonedTempAge = time.Hour
	policy.RepoRuntimeBytes = 1024
	for _, base := range []string{
		filepath.Join(repo, ".reconc", "audit.jsonl"),
		filepath.Join(repo, ".reconc", "runloop", "decisions.jsonl"),
	} {
		for index := 0; index <= 3; index++ {
			path := base
			if index > 0 {
				path = fmt.Sprintf("%s.%d", base, index)
			}
			writeTimed(t, path, repeatedJSONL(80), now.Add(-2*time.Hour))
		}
	}
	cache := filepath.Join(repo, ".reconc", "cache")
	for index := 0; index < 3; index++ {
		writeTimed(t, filepath.Join(cache, fmt.Sprintf("workflow-audit-%d", index)), []byte("generated-binary-contents"), now.Add(time.Duration(index-3)*time.Hour))
	}
	writeTimed(t, filepath.Join(cache, "workflow-audit-template.tmp.123"), []byte("tmp"), now.Add(-2*time.Hour))
	ownedTemp := filepath.Join(tempRoot, "reconc-proof-neg-stale")
	writeTimed(t, filepath.Join(ownedTemp, "large"), make([]byte, 4096), now.Add(-2*time.Hour))
	if err := os.Chtimes(ownedTemp, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: tempRoot})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors: %v", report.Errors)
	}
	if _, err := os.Stat(filepath.Join(tempRoot, "reconc-proof-neg-stale")); !os.IsNotExist(err) {
		t.Fatalf("stale owned temp survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "workflow-audit-template.tmp.123")); !os.IsNotExist(err) {
		t.Fatalf("repo temp survived: %v", err)
	}
	for _, base := range []string{
		filepath.Join(repo, ".reconc", "audit.jsonl"),
		filepath.Join(repo, ".reconc", "runloop", "decisions.jsonl"),
	} {
		for _, path := range []string{base, base + ".1"} {
			info, err := os.Stat(path)
			if err != nil || info.Size() > 256 {
				t.Fatalf("JSONL bound failed for %s: info=%v err=%v", path, info, err)
			}
		}
		if _, err := os.Stat(base + ".2"); !os.IsNotExist(err) {
			t.Fatalf("extra archive survived: %s", base+".2")
		}
	}
}

func TestRunIfDueWritesOnceAndSkipsWithoutMutation(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	tempRoot := t.TempDir()
	now := time.Now().UTC()
	options := Options{RepoRoot: repo, StateRoot: stateRoot, Policy: DefaultPolicy(), Now: now, TempRoot: tempRoot}
	first := RunIfDue(options)
	if !first.Ran {
		t.Fatal("first lifecycle pass did not run")
	}
	marker := filepath.Join(ProjectDir(stateRoot, repo), ".last-retention")
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(ProjectDir(stateRoot, repo), "locks", "stale.lock")
	writeTimed(t, stale, nil, now.Add(-48*time.Hour))
	second := RunIfDue(options)
	if second.Ran {
		t.Fatal("not-due lifecycle pass ran")
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("not-due pass mutated state: %v", err)
	}
	after, err := os.Stat(marker)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("not-due pass rewrote marker: before=%v after=%v err=%v", before.ModTime(), after.ModTime(), err)
	}
}

func TestRunIfDueSerializesConcurrentLifecycleCalls(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	options := Options{RepoRoot: repo, StateRoot: stateRoot, Policy: DefaultPolicy(), Now: time.Now().UTC(), TempRoot: t.TempDir()}
	const workers = 20
	var wait sync.WaitGroup
	runs := make(chan bool, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runs <- RunIfDue(options).Ran
		}()
	}
	wait.Wait()
	close(runs)
	count := 0
	for ran := range runs {
		if ran {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one due pass, got %d", count)
	}
}

func TestRunDryRunDoesNotDelete(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	path := filepath.Join(ProjectDir(stateRoot, repo), "sessions", "stale.json")
	writeTimed(t, path, []byte("stale"), now.Add(-30*24*time.Hour))
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: DefaultPolicy(), Now: now, TempRoot: t.TempDir(), DryRun: true})
	if !report.DryRun {
		t.Fatal("dry-run flag missing from report")
	}
	if report.StateBytesAfter >= int64(len("stale")) {
		t.Fatalf("dry run did not report projected cleanup: %+v", report)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dry run deleted file: %v", err)
	}
	for _, candidate := range []string{
		filepath.Join(ProjectDir(stateRoot, repo), ".retention.lock"),
		filepath.Join(stateRoot, ".owned-temp-retention.lock"),
		filepath.Join(stateRoot, ".last-owned-temp-retention"),
	} {
		if _, err := os.Stat(candidate); !os.IsNotExist(err) {
			t.Fatalf("dry run created runtime state %s: %v", candidate, err)
		}
	}
}

func TestRunIfDueDryRunDoesNotCreateRuntimeState(t *testing.T) {
	repo := t.TempDir()
	stateRoot := filepath.Join(t.TempDir(), "missing-state-root")
	report := RunIfDue(Options{
		RepoRoot:  repo,
		StateRoot: stateRoot,
		TempRoot:  t.TempDir(),
		Policy:    DefaultPolicy(),
		Now:       time.Now().UTC(),
		DryRun:    true,
	})
	if !report.Ran || !report.DryRun || len(report.Errors) != 0 {
		t.Fatalf("dry due report: %+v", report)
	}
	if _, err := os.Stat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("dry due run created state root: %v", err)
	}
}

func writeTimed(t *testing.T, path string, body []byte, modTime time.Time) {
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

func repeatedJSONL(lines int) []byte {
	var body []byte
	for index := 0; index < lines; index++ {
		body = append(body, fmt.Sprintf("{\"index\":%d}\n", index)...)
	}
	return body
}

func BenchmarkLifecycleRetentionNotDue(b *testing.B) {
	options := Options{
		RepoRoot:  b.TempDir(),
		StateRoot: b.TempDir(),
		TempRoot:  b.TempDir(),
		Policy:    DefaultPolicy(),
		Now:       time.Now().UTC(),
	}
	if report := RunIfDue(options); !report.Ran || len(report.Errors) != 0 {
		b.Fatalf("initial retention: %+v", report)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if report := RunIfDue(options); report.Ran || len(report.Errors) != 0 {
			b.Fatalf("not-due retention: %+v", report)
		}
	}
}
