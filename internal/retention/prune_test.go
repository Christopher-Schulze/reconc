package retention

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/repositorycontrol"
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

func TestRunBoundsCommandProofs(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	policy := DefaultPolicy()
	policy.CommandProofs = ClassPolicy{MaxFiles: 1, MaxBytes: 64, MaxAge: 24 * time.Hour}
	for index := 0; index < 3; index++ {
		writeTimed(t, filepath.Join(project, "command-proofs", fmt.Sprintf("proof-%d.json", index)), []byte("proof"), now.Add(time.Duration(index-3)*time.Hour))
	}
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors: %v", report.Errors)
	}
	entries, err := os.ReadDir(filepath.Join(project, "command-proofs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "proof-2.json" {
		t.Fatalf("command-proof retention kept %+v", entries)
	}
}

func TestRunNeverPrunesUnresolvedPolicyDecision(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	path := filepath.Join(ProjectDir(stateRoot, repo), "policy-decisions", "latest.json")
	writeTimed(t, path, []byte("unresolved-block"), now.Add(-365*24*time.Hour))
	policy := DefaultPolicy()
	policy.PolicyDecisions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.StateTotalBytes = 0

	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unresolved policy decision was pruned: %v", err)
	}
	var decisions ClassReport
	for _, class := range report.Classes {
		if class.Name == "policy-decisions" {
			decisions = class
			break
		}
	}
	if decisions.FilesKept != 1 || decisions.FilesDeleted != 0 {
		t.Fatalf("policy decision retention result: %+v", decisions)
	}
	if !strings.Contains(strings.Join(report.Errors, "\n"), "protected state uses") {
		t.Fatalf("protected over-budget receipt was not reported: %+v", report)
	}
}

func TestRunCoversLogsBinariesAndOwnedTemp(t *testing.T) {
	repo := t.TempDir()
	if err := repositorycontrol.EnsureRunDirectory(repo); err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	tempRoot := t.TempDir()
	now := time.Now().UTC()
	policy := DefaultPolicy()
	policy.RunDecisionFileBytes = 256
	policy.RunDecisionArchives = 1
	policy.GeneratedBinaries = ClassPolicy{MaxFiles: 1, MaxBytes: 64, MaxAge: 24 * time.Hour}
	policy.AbandonedTempAge = time.Hour
	policy.RepoRuntimeBytes = 10 * 1024 * 1024
	for index := 0; index < 3; index++ {
		if err := audit.Append(repo, audit.Entry{
			Timestamp: now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
			Event:     fmt.Sprintf("check-%d", index),
			Decision:  "pass",
		}, 0); err != nil {
			t.Fatal(err)
		}
	}
	runDecisions := filepath.Join(repo, ".reconc", "run", "decisions.jsonl")
	for index := 0; index <= 3; index++ {
		path := runDecisions
		if index > 0 {
			path = fmt.Sprintf("%s.%d", runDecisions, index)
		}
		writePrivateTimed(t, path, repeatedJSONL(80), now.Add(-2*time.Hour))
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
	if verification, err := audit.Verify(repo); err != nil || !verification.Valid || verification.Entries != 3 {
		t.Fatalf("retention damaged chained audit evidence: report=%+v err=%v", verification, err)
	}
	for _, path := range []string{runDecisions, runDecisions + ".1"} {
		info, err := os.Stat(path)
		if err != nil || info.Size() > 256 {
			t.Fatalf("run-decision JSONL bound failed for %s: info=%v err=%v", path, info, err)
		}
	}
	if _, err := os.Stat(runDecisions + ".2"); !os.IsNotExist(err) {
		t.Fatalf("extra run-decision archive survived: %s", runDecisions+".2")
	}
}

func TestRunRefusesToCompactMalformedChainedAuditEvidence(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	path := filepath.Join(repo, audit.AuditFileRelative)
	writeTimed(t, path, []byte("{malformed}\n"), time.Now().Add(-time.Hour))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	report := Run(Options{
		RepoRoot:  repo,
		StateRoot: stateRoot,
		Policy:    DefaultPolicy(),
		Now:       time.Now(),
		TempRoot:  t.TempDir(),
	})
	if !strings.Contains(strings.Join(report.Errors, "\n"), "verify audit chain") {
		t.Fatalf("malformed audit evidence was not reported: %+v", report)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("retention rewrote malformed audit evidence: err=%v", err)
	}
}

func TestRunBoundsGlobalProjectStateAndPreservesLiveRecentAndUnknownRoots(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	policy := DefaultPolicy()
	policy.ProjectRoots = ClassPolicy{MaxFiles: 3, MaxBytes: 48, MaxAge: 30 * 24 * time.Hour}
	policy.Locks.MaxAge = 24 * time.Hour
	current := ProjectDir(stateRoot, repo)
	writeTimed(t, filepath.Join(current, "sessions", "current.json"), []byte("current"), now.Add(-60*24*time.Hour))
	live := filepath.Join(stateRoot, "projects", "1111111111111111")
	writeTimed(t, filepath.Join(live, "sessions", "live.json"), []byte("live"), now)
	writeTimed(t, filepath.Join(live, "active-session.txt"), []byte("live\n"), now)
	recent := filepath.Join(stateRoot, "projects", "2222222222222222")
	writeTimed(t, filepath.Join(recent, "recent"), []byte("recent"), now.Add(-time.Hour))
	stale := filepath.Join(stateRoot, "projects", "3333333333333333")
	writeTimed(t, filepath.Join(stale, "stale"), []byte("stale"), now.Add(-60*24*time.Hour))
	overflow := filepath.Join(stateRoot, "projects", "4444444444444444")
	writeTimed(t, filepath.Join(overflow, "overflow"), []byte("overflow"), now.Add(-48*time.Hour))
	for path, modTime := range map[string]time.Time{stale: now.Add(-60 * 24 * time.Hour), overflow: now.Add(-48 * time.Hour)} {
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	unknown := filepath.Join(stateRoot, "projects", "operator-notes")
	writeTimed(t, filepath.Join(unknown, "keep"), []byte("keep"), now.Add(-365*24*time.Hour))

	report := RunIfDue(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors: %v", report.Errors)
	}
	for _, path := range []string{current, live, recent, unknown} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected root removed: %s: %v", path, err)
		}
	}
	for _, path := range []string{stale, overflow} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unbounded project root survived: %s: %v", path, err)
		}
	}
	if report.ProjectStateBytes > policy.ProjectRoots.MaxBytes {
		t.Fatalf("global project state not bounded: %+v", report)
	}
}

func TestExplicitRunEnforcesProjectCountWithoutLifecycleGrace(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	policy := DefaultPolicy()
	policy.ProjectRoots = ClassPolicy{MaxFiles: 2, MaxBytes: 1024, MaxAge: 30 * 24 * time.Hour}
	current := ProjectDir(stateRoot, repo)
	writeTimed(t, filepath.Join(current, "current"), []byte("current"), now)
	for index, key := range []string{"1111111111111111", "2222222222222222", "3333333333333333"} {
		writeTimed(t, filepath.Join(stateRoot, "projects", key, "state"), []byte(key), now.Add(time.Duration(index)*time.Minute))
	}

	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now.Add(time.Hour), TempRoot: t.TempDir()})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors: %v", report.Errors)
	}
	if report.ProjectStateBytes > policy.ProjectRoots.MaxBytes {
		t.Fatalf("global byte limit not enforced: %+v", report)
	}
	projectClass := ClassReport{}
	for _, class := range report.Classes {
		if class.Name == "project-state-roots" {
			projectClass = class
		}
	}
	if projectClass.FilesKept != 2 || projectClass.FilesDeleted != 2 {
		t.Fatalf("explicit project count result: %+v", projectClass)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("current project was removed: %v", err)
	}
}

func TestProjectRootRetentionProtectsUnresolvedPolicyDecision(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	policy := DefaultPolicy()
	policy.ProjectRoots = ClassPolicy{MaxFiles: 1, MaxBytes: 1, MaxAge: time.Hour}
	current := ProjectDir(stateRoot, repo)
	writeTimed(t, filepath.Join(current, "current"), []byte("current"), now)
	blocked := filepath.Join(stateRoot, "projects", "1111111111111111")
	writeTimed(t, filepath.Join(blocked, "policy-decisions", "latest.json"), []byte("unresolved-block"), now.Add(-365*24*time.Hour))
	stale := filepath.Join(stateRoot, "projects", "2222222222222222")
	writeTimed(t, filepath.Join(stale, "stale"), []byte("stale"), now.Add(-365*24*time.Hour))

	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	for _, path := range []string{current, blocked} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected project root was removed: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("unprotected stale project root survived: %v", err)
	}
	if !strings.Contains(strings.Join(report.Errors, "\n"), "protected project state uses") {
		t.Fatalf("protected over-budget project roots were not reported: %+v", report)
	}
}

func TestProjectRootRetentionNeverReturnsDurableActionCapacity(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	policy := DefaultPolicy()
	policy.ProjectRoots = ClassPolicy{MaxFiles: 1, MaxBytes: 1, MaxAge: time.Hour}
	current := ProjectDir(stateRoot, repo)
	writeTimed(t, filepath.Join(current, "current"), []byte("current"), now)
	actionRoot := filepath.Join(stateRoot, "projects", "1111111111111111")
	writeTimed(
		t, filepath.Join(actionRoot, "action", "state.json"),
		[]byte(`{"schema":"reconc.action-state/v1"}`), now.Add(-365*24*time.Hour),
	)
	stale := filepath.Join(stateRoot, "projects", "2222222222222222")
	writeTimed(t, filepath.Join(stale, "stale"), []byte("stale"), now.Add(-365*24*time.Hour))

	report := Run(Options{
		RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir(),
	})
	for _, path := range []string{current, actionRoot} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("capacity-owning project root was removed: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("unprotected stale project root survived: %v", err)
	}
	if !strings.Contains(strings.Join(report.Errors, "\n"), "protected project state uses") {
		t.Fatalf("protected over-budget action state was not reported: %+v", report)
	}
}

func TestProjectRootSizingFailsClosedOnIrregularEntries(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	root := filepath.Join(stateRoot, "projects", "1111111111111111")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(stateRoot, "outside"), filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}

	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: DefaultPolicy(), Now: now, TempRoot: t.TempDir()})
	if len(report.Errors) == 0 || !strings.Contains(strings.Join(report.Errors, "\n"), "unsupported non-regular entry") {
		t.Fatalf("irregular project state must fail closed: %+v", report)
	}
	if _, err := os.Lstat(root); err != nil {
		t.Fatalf("uninspectable project root was removed: %v", err)
	}
}

func TestDefaultPolicyRemovesAbandonedOwnedTempWithoutTouchingRecentWork(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	tempRoot := t.TempDir()
	now := time.Now().UTC()
	policy := DefaultPolicy()
	if policy.AbandonedTempAge != 2*time.Hour {
		t.Fatalf("abandoned temp grace = %s, want 2h", policy.AbandonedTempAge)
	}

	cases := []struct {
		name    string
		age     time.Duration
		removed bool
	}{
		{name: "reconc-proof-neg-stale", age: 3 * time.Hour, removed: true},
		{name: "reconc-proof-gocache-recent", age: 90 * time.Minute},
	}
	for _, testCase := range cases {
		path := filepath.Join(tempRoot, testCase.name)
		modTime := now.Add(-testCase.age)
		writeTimed(t, filepath.Join(path, "payload"), make([]byte, 4096), modTime)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: tempRoot})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors: %v", report.Errors)
	}
	for _, testCase := range cases {
		_, err := os.Stat(filepath.Join(tempRoot, testCase.name))
		if testCase.removed && !os.IsNotExist(err) {
			t.Fatalf("abandoned owned temp survived default retention: %v", err)
		}
		if !testCase.removed && err != nil {
			t.Fatalf("recent owned temp was removed: %v", err)
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

func TestSuccessfulManualRunRecordsCompletionAndSkipsImmediateDueRun(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Date(2026, 8, 30, 12, 0, 0, 123, time.UTC)
	options := Options{RepoRoot: repo, StateRoot: stateRoot, Policy: DefaultPolicy(), Now: now, TempRoot: t.TempDir()}

	manual := Run(options)
	if !manual.Ran || len(manual.Errors) != 0 {
		t.Fatalf("manual retention report = %+v", manual)
	}
	marker := filepath.Join(ProjectDir(stateRoot, repo), retentionMarkerName)
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != now.Format(time.RFC3339Nano)+"\n" {
		t.Fatalf("manual completion marker = %q, err=%v", body, err)
	}
	if followUp := RunIfDue(options); followUp.Ran || len(followUp.Errors) != 0 {
		t.Fatalf("immediate due follow-up = %+v", followUp)
	}
}

func TestDryAndFailedRunsDoNotRecordCompletion(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, string, string, *Policy)
		dryRun  bool
		due     bool
	}{
		{name: "dry run", dryRun: true},
		{
			name: "failed manual run",
			prepare: func(t *testing.T, repo, stateRoot string, policy *Policy) {
				path := filepath.Join(ProjectDir(stateRoot, repo), "policy-decisions", "latest.json")
				writeTimed(t, path, []byte("unresolved-block"), time.Now().Add(-time.Hour))
				policy.StateTotalBytes = 0
			},
		},
		{
			name: "failed due run",
			due:  true,
			prepare: func(t *testing.T, repo, stateRoot string, policy *Policy) {
				path := filepath.Join(ProjectDir(stateRoot, repo), "policy-decisions", "latest.json")
				writeTimed(t, path, []byte("unresolved-block"), time.Now().Add(-time.Hour))
				policy.StateTotalBytes = 0
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			stateRoot := t.TempDir()
			policy := DefaultPolicy()
			if test.prepare != nil {
				test.prepare(t, repo, stateRoot, &policy)
			}
			options := Options{
				RepoRoot: repo, StateRoot: stateRoot, Policy: policy,
				Now: time.Now().UTC(), TempRoot: t.TempDir(), DryRun: test.dryRun,
			}
			run := Run
			if test.due {
				run = RunIfDue
			}
			report := run(options)
			if !report.Ran || (test.dryRun && len(report.Errors) != 0) || (!test.dryRun && len(report.Errors) == 0) {
				t.Fatalf("retention report = %+v", report)
			}
			marker := filepath.Join(ProjectDir(stateRoot, repo), retentionMarkerName)
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				t.Fatalf("non-successful run created completion marker: %v", err)
			}
		})
	}
}

func TestManualAndDueRunsKeepCompletionMarkerMonotonic(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	policy := DefaultPolicy()
	later := time.Date(2026, 8, 30, 18, 0, 0, 0, time.UTC)
	earlier := later.Add(-policy.Interval)
	entered := make(chan struct{})
	release := make(chan struct{})
	original := publishRetentionMarker
	publishRetentionMarker = func(path string, completedAt time.Time) error {
		if completedAt.Equal(later) {
			close(entered)
			<-release
		}
		return original(path, completedAt)
	}
	t.Cleanup(func() { publishRetentionMarker = original })

	manualTemp := t.TempDir()
	dueTemp := t.TempDir()
	manualResult := make(chan Report, 1)
	go func() {
		manualResult <- Run(Options{
			RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: later, TempRoot: manualTemp,
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("manual retention did not reach completion publication")
	}
	dueResult := make(chan Report, 1)
	go func() {
		dueResult <- RunIfDue(Options{
			RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: earlier, TempRoot: dueTemp,
		})
	}()
	close(release)
	select {
	case report := <-manualResult:
		if !report.Ran || len(report.Errors) != 0 {
			t.Fatalf("manual report = %+v", report)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manual retention did not finish")
	}
	select {
	case report := <-dueResult:
		if report.Ran || len(report.Errors) != 0 {
			t.Fatalf("concurrent due report = %+v", report)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent due retention did not finish")
	}
	marker := filepath.Join(ProjectDir(stateRoot, repo), retentionMarkerName)
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != later.Format(time.RFC3339Nano)+"\n" {
		t.Fatalf("monotonic completion marker = %q, err=%v", body, err)
	}
}

func TestMarkerPublicationFailureLeavesImmediateFollowUpDue(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	options := Options{RepoRoot: repo, StateRoot: stateRoot, Policy: DefaultPolicy(), Now: now, TempRoot: t.TempDir()}
	injected := fmt.Errorf("injected marker publication failure")
	original := publishRetentionMarker
	publishRetentionMarker = func(string, time.Time) error { return injected }
	t.Cleanup(func() { publishRetentionMarker = original })

	first := Run(options)
	if len(first.Errors) != 1 || !strings.Contains(first.Errors[0], injected.Error()) {
		t.Fatalf("failed marker report = %+v", first)
	}
	publishRetentionMarker = original
	if followUp := RunIfDue(options); !followUp.Ran || len(followUp.Errors) != 0 {
		t.Fatalf("follow-up report = %+v", followUp)
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
		filepath.Join(stateRoot, ".project-root-retention.lock"),
		filepath.Join(stateRoot, ".last-project-root-retention"),
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

func writePrivateTimed(t *testing.T, path string, body []byte, modTime time.Time) {
	t.Helper()
	if _, err := privatefs.WritePrivateIfChanged(path, body, 0o600); err != nil {
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
