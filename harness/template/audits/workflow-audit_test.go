package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBatchAuditResultsReportsModesIndependently(t *testing.T) {
	output, hasFailures := batchAuditResults(t.TempDir(), []string{"unknown-audit-a", "unknown-audit-b"})
	if !hasFailures {
		t.Fatal("expected batch failures for unknown modes")
	}
	if len(output.Results) != 2 {
		t.Fatalf("expected two batch results, got %d", len(output.Results))
	}
	if output.Results[0].Mode != "unknown-audit-a" || !containsFailure(output.Results[0].Failures, `unknown audit mode "unknown-audit-a"`) {
		t.Fatalf("unexpected first batch result: %#v", output.Results[0])
	}
	if output.Results[1].Mode != "unknown-audit-b" || !containsFailure(output.Results[1].Failures, `unknown audit mode "unknown-audit-b"`) {
		t.Fatalf("unexpected second batch result: %#v", output.Results[1])
	}
}

func TestBatchAuditResultsRunsModesConcurrentlyAndKeepsOrder(t *testing.T) {
	start := time.Now()
	output, hasFailures := batchAuditResultsWithRunner(t.TempDir(), []string{"slow-a", "slow-b"}, func(_ string, mode string) []string {
		time.Sleep(120 * time.Millisecond)
		if mode == "slow-b" {
			return []string{"b failed"}
		}
		return nil
	})
	elapsed := time.Since(start)
	if !hasFailures {
		t.Fatal("expected slow-b failure")
	}
	if len(output.Results) != 2 || output.Results[0].Mode != "slow-a" || output.Results[1].Mode != "slow-b" {
		t.Fatalf("batch results must preserve input order, got %#v", output.Results)
	}
	if len(output.Results[0].Failures) != 0 || !containsFailure(output.Results[1].Failures, "b failed") {
		t.Fatalf("unexpected failures: %#v", output.Results)
	}
	if elapsed >= 220*time.Millisecond {
		t.Fatalf("batch modes should run concurrently, took %s", elapsed)
	}
}

func TestRunAuditFuncsRunsFullAuditChecksConcurrentlyAndKeepsFailures(t *testing.T) {
	start := time.Now()
	failures := runAuditFuncs(t.TempDir(), []auditFunc{
		func(string) []string {
			time.Sleep(120 * time.Millisecond)
			return []string{"first failure"}
		},
		func(string) []string {
			time.Sleep(120 * time.Millisecond)
			return nil
		},
		func(string) []string {
			time.Sleep(120 * time.Millisecond)
			return []string{"third failure"}
		},
	})
	elapsed := time.Since(start)
	if !containsFailure(failures, "first failure") || !containsFailure(failures, "third failure") {
		t.Fatalf("expected every failure to be preserved, got %#v", failures)
	}
	if elapsed >= 260*time.Millisecond {
		t.Fatalf("full-audit functions should run concurrently, took %s", elapsed)
	}
}

func TestAuditAgentQualityRejectsAddedTestSkip(t *testing.T) {
	root := gitInitRepoForRowsImmutable(t, minimalTasksMd())
	writeFile(t, root, "backend/project/internal/policy/gaps_test.go", `package policy

import "testing"

func TestGap(t *testing.T) {
	t.`+`Skip("hide missing behavior")
}
`)
	gitRun(t, root, "add", "backend/project/internal/policy/gaps_test.go")

	failures := auditAgentQuality(root)
	if !containsFailure(failures, "added test skip is forbidden") {
		t.Fatalf("expected added test skip failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentQualityRequiresSamePackageTestForSensitiveGo(t *testing.T) {
	root := gitInitRepoForRowsImmutable(t, minimalTasksMd())
	writeFile(t, root, "backend/project/internal/policy/path_glob.go", `package policy

func MatchPathGlob(pattern string, path string) bool {
	return pattern == path
}
`)
	gitRun(t, root, "add", "backend/project/internal/policy/path_glob.go")

	failures := auditAgentQuality(root)
	if !containsFailure(failures, "sensitive Go changes require a same-package *_test.go change") {
		t.Fatalf("expected same-package test failure, got:\n%s", strings.Join(failures, "\n"))
	}

	writeFile(t, root, "backend/project/internal/policy/path_glob_test.go", `package policy

import "testing"

func TestMatchPathGlob(t *testing.T) {
	if !MatchPathGlob("a", "a") {
		t.Fatal("expected exact match")
	}
}
`)
	gitRun(t, root, "add", "backend/project/internal/policy/path_glob_test.go")

	failures = auditAgentQuality(root)
	if containsFailure(failures, "sensitive Go changes require a same-package *_test.go change") {
		t.Fatalf("same-package test change must satisfy sensitive gate, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditRepoLayoutAllowsRootWorkflowCompleteLoop(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "codebase/.keep", "")
	writeFile(t, root, "workflow-complete-loop.md", "# workflow\n")

	failures := auditRepoLayout(root)
	if containsFailure(failures, "unexpected root entry workflow-complete-loop.md") {
		t.Fatalf("root workflow-complete loop must be allowed, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentQualityRejectsTaskPlaceholderPhrases(t *testing.T) {
	root := gitInitRepoForRowsImmutable(t, minimalTasksMd())
	writeFile(t, root, "docs/tasks/TASK-0002-New.md", `# TASK-0002-New

## Final Reality Check

- Reality Check: PASS - basic implementation for now.
`)
	gitRun(t, root, "add", "docs/tasks/TASK-0002-New.md")

	failures := auditAgentQuality(root)
	if !containsFailure(failures, `basic implementation`) || !containsFailure(failures, `for now`) {
		t.Fatalf("expected placeholder phrase failures, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditReconcBinaryFreshnessRejectsStaleLiveBinary(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "tools/reconc/dist/reconc-0.5.0-darwin-arm64", "#!/bin/sh\n")
	writeFile(t, root, "tools/reconc/internal/runtime/agentsession/handlers.go", "package agentsession\n")
	oldTime := time.Unix(100, 0)
	newTime := time.Unix(200, 0)
	binary := filepath.Join(root, "tools/reconc/dist/reconc-0.5.0-darwin-arm64")
	source := filepath.Join(root, "tools/reconc/internal/runtime/agentsession/handlers.go")
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(binary, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	failures := auditReconcBinaryFreshness(root)
	if !containsFailure(failures, "is older than tools/reconc/internal/runtime/agentsession/handlers.go") {
		t.Fatalf("expected stale binary failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateAllowsCurrentBeyondBlockedFirstOpen(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0002-Active-Work -> tasks/TASK-0002-Active-Work.md

- [ ] TASK-0001-Queued-Work - queued work -> tasks/TASK-0001-Queued-Work.md
- [ ] TASK-0002-Active-Work - active work -> tasks/TASK-0002-Active-Work.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-Queued-Work.md", taskDetail("TASK-0001-Queued-Work", "Blocked", "- [ ] Wait for dependency", ""))
	writeFile(t, root, "docs/tasks/TASK-0002-Active-Work.md", taskDetail("TASK-0002-Active-Work", "Active", "- [~] Continue here", ""))

	if failures := auditTaskState(root); len(failures) > 0 {
		t.Fatalf("expected valid task state, got failures:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateRequiresLoopFieldForStagedDoneTask(t *testing.T) {
	tasksMd := `# Tasks

Current: TASK-0002-Next-Work -> tasks/TASK-0002-Next-Work.md

- [x] TASK-0001-Done-Work - done work -> tasks/done/TASK-0001-Done-Work.md
- [ ] TASK-0002-Next-Work - next work -> tasks/TASK-0002-Next-Work.md
`
	root := gitInitRepoForRowsImmutable(t, tasksMd)
	frcNoLoop := strings.Replace(finalRealityCheck(), "- Reality Check Loop: PASS - 2 passes, nothing left\n", "", 1)
	writeFile(t, root, "docs/tasks/done/TASK-0001-Done-Work.md", taskDetail("TASK-0001-Done-Work", "Done", "- [x] done", frcNoLoop))
	writeFile(t, root, "docs/tasks/TASK-0002-Next-Work.md", taskDetail("TASK-0002-Next-Work", "Active", "- [~] continue", ""))
	gitRun(t, root, "add", "docs/tasks/done/TASK-0001-Done-Work.md", "docs/tasks/TASK-0002-Next-Work.md")

	failures := auditTaskState(root)
	if !containsFailure(failures, "missing the Reality Check Loop") {
		t.Fatalf("expected staged done task to require the loop attestation, got:\n%s", strings.Join(failures, "\n"))
	}

	writeFile(t, root, "docs/tasks/done/TASK-0001-Done-Work.md", taskDetail("TASK-0001-Done-Work", "Done", "- [x] done", finalRealityCheck()))
	gitRun(t, root, "add", "docs/tasks/done/TASK-0001-Done-Work.md")
	if failures := auditTaskState(root); containsFailure(failures, "missing the Reality Check Loop") {
		t.Fatalf("loop field present must satisfy the gate, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateExemptsUnchangedArchivedDoneTask(t *testing.T) {
	tasksMd := `# Tasks

Current: TASK-0002-Next-Work -> tasks/TASK-0002-Next-Work.md

- [x] TASK-0001-Done-Work - done work -> tasks/done/TASK-0001-Done-Work.md
- [ ] TASK-0002-Next-Work - next work -> tasks/TASK-0002-Next-Work.md
`
	root := gitInitRepoForRowsImmutable(t, tasksMd)
	frcNoLoop := strings.Replace(finalRealityCheck(), "- Reality Check Loop: PASS - 2 passes, nothing left\n", "", 1)
	writeFile(t, root, "docs/tasks/done/TASK-0001-Done-Work.md", taskDetail("TASK-0001-Done-Work", "Done", "- [x] done", frcNoLoop))
	writeFile(t, root, "docs/tasks/TASK-0002-Next-Work.md", taskDetail("TASK-0002-Next-Work", "Active", "- [~] continue", ""))
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "archive without loop field")

	if failures := auditTaskState(root); containsFailure(failures, "missing the Reality Check Loop") {
		t.Fatalf("unchanged archived task without the loop field must stay exempt, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateRejectsExecutableBeforeCurrent(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0002-Active-Work -> tasks/TASK-0002-Active-Work.md

- [ ] TASK-0001-Queued-Work - queued work -> tasks/TASK-0001-Queued-Work.md
- [ ] TASK-0002-Active-Work - active work -> tasks/TASK-0002-Active-Work.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-Queued-Work.md", taskDetail("TASK-0001-Queued-Work", "Queued", "- [ ] Start first", ""))
	writeFile(t, root, "docs/tasks/TASK-0002-Active-Work.md", taskDetail("TASK-0002-Active-Work", "Active", "- [~] Continue here", ""))

	failures := auditTaskState(root)
	if !containsFailure(failures, "executable TASK TASK-0001-Queued-Work appears before Current") {
		t.Fatalf("expected executable-before-current failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateRejectsCurrentDoneRow(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-Finished-Work -> tasks/done/TASK-0001-Finished-Work.md

- [x] TASK-0001-Finished-Work - finished work -> tasks/done/TASK-0001-Finished-Work.md
`)
	writeFile(t, root, "docs/tasks/done/TASK-0001-Finished-Work.md", taskDetail("TASK-0001-Finished-Work", "Done", "- [x] Finished", finalRealityCheck()))

	failures := auditTaskState(root)
	if !containsFailure(failures, "Current header must point to an unchecked [ ] TASK row") || !containsFailure(failures, "no open [ ] tasks") {
		t.Fatalf("expected Current/done failures, got:\n%s", strings.Join(failures, "\n"))
	}
	if !containsFailure(failures, "zero-finding Terminal Gate") || containsFailure(failures, "Project Complete Candidate") {
		t.Fatalf("expected zero-finding terminal guidance without old final-hold wording, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateRejectsMissingStatusState(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-Active-Work -> tasks/TASK-0001-Active-Work.md

- [ ] TASK-0001-Active-Work - active work -> tasks/TASK-0001-Active-Work.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-Active-Work.md", strings.Replace(taskDetail("TASK-0001-Active-Work", "Active", "- [~] Continue here", ""), "State: Active", "Working now", 1))

	failures := auditTaskState(root)
	if !containsFailure(failures, "Status must contain 'State: Active|Queued|Blocked|Paused|Done'") {
		t.Fatalf("expected missing status-state failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateRejectsDoneWithoutFinalRealityCheck(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0002-Active-Work -> tasks/TASK-0002-Active-Work.md

- [x] TASK-0001-Finished-Work - finished work -> tasks/done/TASK-0001-Finished-Work.md
- [ ] TASK-0002-Active-Work - active work -> tasks/TASK-0002-Active-Work.md
`)
	writeFile(t, root, "docs/tasks/done/TASK-0001-Finished-Work.md", taskDetail("TASK-0001-Finished-Work", "Done", "- [x] Finished", ""))
	writeFile(t, root, "docs/tasks/TASK-0002-Active-Work.md", taskDetail("TASK-0002-Active-Work", "Active", "- [~] Continue here", ""))

	failures := auditTaskState(root)
	if !containsFailure(failures, "done TASK missing ## Final Reality Check") {
		t.Fatalf("expected missing final reality check failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateRejectsCurrentAfterTaskRows(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

- [ ] TASK-0001-Active-Work - active work -> tasks/TASK-0001-Active-Work.md

Current: TASK-0001-Active-Work -> tasks/TASK-0001-Active-Work.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-Active-Work.md", taskDetail("TASK-0001-Active-Work", "Active", "- [~] Continue here", ""))

	failures := auditTaskState(root)
	if !containsFailure(failures, "Current header must be the fixed control line before TASK entries") {
		t.Fatalf("expected Current placement failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateRejectsMissingExpectedTouchSurfaces(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-Active-Work -> tasks/TASK-0001-Active-Work.md

- [ ] TASK-0001-Active-Work - active work -> tasks/TASK-0001-Active-Work.md
`)
	detail := strings.Replace(taskDetail("TASK-0001-Active-Work", "Active", "- [~] Continue here", ""), "- Expected Touch Surfaces: docs/tasks/**\n", "", 1)
	writeFile(t, root, "docs/tasks/TASK-0001-Active-Work.md", detail)

	failures := auditTaskState(root)
	if !containsFailure(failures, "Expected Touch Surfaces must list at least one repo-relative owner path/glob") {
		t.Fatalf("expected missing touch-surface failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateRejectsParallelTouchSurfaceOverlap(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-Active-Work -> tasks/TASK-0001-Active-Work.md

- [ ] TASK-0001-Active-Work - active work -> tasks/TASK-0001-Active-Work.md
- [ ] TASK-0002-Queued-Work - queued work -> tasks/TASK-0002-Queued-Work.md
`)
	first := strings.Replace(taskDetail("TASK-0001-Active-Work", "Active", "- [~] Continue here", ""), "- Parallel Group: none", "- Parallel Group: PG-Test", 1)
	second := strings.Replace(taskDetail("TASK-0002-Queued-Work", "Queued", "- [ ] Continue later", ""), "- Parallel Group: none", "- Parallel Group: PG-Test", 1)
	second = strings.Replace(second, "docs/tasks/**", "docs/tasks/subsystem/**", 1)
	writeFile(t, root, "docs/tasks/TASK-0001-Active-Work.md", first)
	writeFile(t, root, "docs/tasks/TASK-0002-Queued-Work.md", second)

	failures := auditTaskState(root)
	if !containsFailure(failures, "Parallel Group PG-Test task TASK-0002-Queued-Work overlaps TASK-0001-Active-Work") {
		t.Fatalf("expected parallel touch-surface overlap failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskStateRejectsCodeTaskWithoutTestPlan(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-Code-Work -> tasks/TASK-0001-Code-Work.md

- [ ] TASK-0001-Code-Work - code work -> tasks/TASK-0001-Code-Work.md
`)
	detail := strings.Replace(taskDetail("TASK-0001-Code-Work", "Active", "- [~] Implement behavior", ""), "docs/tasks/**", "tools/reconc/harness/template/utils/**", 1)
	writeFile(t, root, "docs/tasks/TASK-0001-Code-Work.md", detail)

	failures := auditTaskState(root)
	if !containsFailure(failures, "code TASK Technical Plan must include same-TASK tests") ||
		!containsFailure(failures, "code TASK Acceptance must include test/coverage evidence") ||
		!containsFailure(failures, "code TASK Sub-Tasks must include a test/coverage sub-task") {
		t.Fatalf("expected code-task test-plan failures, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTestCoverageRejectsGoDirectoryWithoutTests(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "tools/reconc/harness/template/utils/example.go", "package main\n\nfunc example() {}\n")

	failures := auditTestCoverage(root)
	if !containsFailure(failures, "Go code directory has no co-located *_test.go") {
		t.Fatalf("expected missing co-located test failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTestCoverageAllowsGoDirectoryWithTests(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "tools/reconc/harness/template/utils/example.go", "package main\n\nfunc example() {}\n")
	writeFile(t, root, "tools/reconc/harness/template/utils/example_test.go", "package main\n\nimport \"testing\"\n\nfunc TestExample(t *testing.T) {}\n")

	if failures := auditTestCoverage(root); len(failures) > 0 {
		t.Fatalf("expected test coverage audit to pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditBuildBaselineRequiresCanonicalFiles(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "codebase"), 0o755); err != nil {
		t.Fatalf("mkdir codebase: %v", err)
	}
	failures := auditBuildBaseline(root)
	if !containsFailure(failures, "build baseline missing go.mod") ||
		!containsFailure(failures, "build baseline missing codebase/scripts/build/build.go") ||
		!containsFailure(failures, "build baseline missing codebase/backend/project/main.go") {
		t.Fatalf("expected missing build baseline failures, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditBuildBaselineFlatRootRequiresCanonicalFiles(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	failures := auditBuildBaseline(root)
	if !containsFailure(failures, "build baseline missing go.mod") ||
		!containsFailure(failures, "build baseline missing scripts/build/build.go") ||
		!containsFailure(failures, "build baseline missing backend/project/main.go") {
		t.Fatalf("expected flat-root missing build baseline failures, got:\n%s", strings.Join(failures, "\n"))
	}
	for _, f := range failures {
		if strings.Contains(f, "codebase/") {
			t.Fatalf("flat-root audit must not reference codebase/ paths: %s", f)
		}
	}
}

func TestAuditBuildBaselineAcceptsCanonicalSkeleton(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "go.mod", "module project\n\ngo 1.24\n")
	writeFile(t, root, "codebase/scripts/build/build.go", `package main
func main(){}
const tokens = `+"`"+`case "build": case "test": case "lint": case "validate": case "clean":`+"`"+`
`)
	writeFile(t, root, "codebase/scripts/build/build_test.go", "package main\n")
	writeFile(t, root, "codebase/backend/project/main.go", "package main\nfunc main(){}\n")
	if failures := auditBuildBaseline(root); len(failures) > 0 {
		t.Fatalf("expected build baseline audit to pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditDurableStoreBaselineRequiresCanonicalFiles(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeDurableStoreStackConfig(t, root)
	if err := os.MkdirAll(filepath.Join(root, "codebase"), 0o755); err != nil {
		t.Fatalf("mkdir codebase: %v", err)
	}
	failures := auditDurableStoreBaseline(root)
	if !containsFailure(failures, "durable store baseline missing codebase/backend/project/internal/store/store.go") ||
		!containsFailure(failures, "durable store baseline missing codebase/db/migrations/project/core/001_initial.sql") {
		t.Fatalf("expected missing durable store baseline failures, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditDurableStoreBaselineFlatRootRequiresCanonicalFiles(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeDurableStoreStackConfig(t, root)
	failures := auditDurableStoreBaseline(root)
	if !containsFailure(failures, "durable store baseline missing backend/project/internal/store/store.go") ||
		!containsFailure(failures, "durable store baseline missing db/migrations/project/core/001_initial.sql") {
		t.Fatalf("expected flat-root missing durable store baseline failures, got:\n%s", strings.Join(failures, "\n"))
	}
	for _, f := range failures {
		if strings.Contains(f, "codebase/") {
			t.Fatalf("flat-root audit must not reference codebase/ paths: %s", f)
		}
	}
}

func TestAuditDurableStoreBaselineAcceptsCanonicalSkeleton(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeDurableStoreStackConfig(t, root)
	writeFile(t, root, "codebase/backend/project/internal/store/store.go", `package store
const tokens = "PRAGMA journal_mode=WAL PRAGMA auto_vacuum=INCREMENTAL migration_run_ledger project_migrations_core SnapshotCore IntegrityCheck github.com/mattn/go-sqlite3"
`)
	writeFile(t, root, "codebase/backend/project/internal/store/hash.go", "package store\n")
	writeFile(t, root, "codebase/backend/project/internal/store/store_test.go", "package store\n")
	writeFile(t, root, "codebase/db/migrations/migrations.go", "package migrations\n")
	writeFile(t, root, "codebase/db/migrations/migrations_test.go", "package migrations\n")
	writeFile(t, root, "codebase/db/migrations/project/core/001_initial.sql", "durable_store_contracts sessions messages knowledge_nodes knowledge_edges retrieval_quality skill_records CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_fts")
	if failures := auditDurableStoreBaseline(root); len(failures) > 0 {
		t.Fatalf("expected durable store baseline audit to pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditSpecTaskCoveragePassesWhenOpenTasksCoverSpec(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/spec.md", "line 1\nline 2\nline 3\n")
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-Cover-Spec -> tasks/TASK-0001-Cover-Spec.md

- [ ] TASK-0001-Cover-Spec - cover spec -> tasks/TASK-0001-Cover-Spec.md
`)
	detail := taskDetail("TASK-0001-Cover-Spec", "Active", "- [~] Cover spec", "")
	detail = strings.Replace(detail, "- Spec Lines: none", "- Spec Lines: docs/spec.md:L1-L3", 1)
	writeFile(t, root, "docs/tasks/TASK-0001-Cover-Spec.md", detail)

	if failures := auditSpecTaskCoverage(root); len(failures) > 0 {
		t.Fatalf("expected spec coverage audit to pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditSpecTaskCoverageRejectsUncoveredSpecLines(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/spec.md", "line 1\nline 2\nline 3\n")
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-Cover-Spec -> tasks/TASK-0001-Cover-Spec.md

- [ ] TASK-0001-Cover-Spec - cover spec -> tasks/TASK-0001-Cover-Spec.md
`)
	detail := taskDetail("TASK-0001-Cover-Spec", "Active", "- [~] Cover spec", "")
	detail = strings.Replace(detail, "- Spec Lines: none", "- Spec Lines: docs/spec.md:L1-L2", 1)
	writeFile(t, root, "docs/tasks/TASK-0001-Cover-Spec.md", detail)

	failures := auditSpecTaskCoverage(root)
	if !containsFailure(failures, "docs/spec.md uncovered by open TASK Spec Lines") {
		t.Fatalf("expected uncovered spec failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestRunSupportsSpecTaskCoverageMode(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/spec.md", "line 1\n")
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-Cover-Spec -> tasks/TASK-0001-Cover-Spec.md

- [ ] TASK-0001-Cover-Spec - cover spec -> tasks/TASK-0001-Cover-Spec.md
`)
	detail := taskDetail("TASK-0001-Cover-Spec", "Active", "- [~] Cover spec", "")
	detail = strings.Replace(detail, "- Spec Lines: none", "- Spec Lines: docs/spec.md:L1-L1", 1)
	writeFile(t, root, "docs/tasks/TASK-0001-Cover-Spec.md", detail)

	if failures := run(root, "spec-task-coverage"); len(failures) > 0 {
		t.Fatalf("expected spec-task-coverage mode to pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditDependencyLocalitySkipsLocalAgentStateDirs(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	for _, dir := range []string{".agents", ".cursor", ".kilo", ".gemini", ".vscode"} {
		writeFile(t, root, filepath.Join(dir, "package.json"), `{"private": true}`)
		writeFile(t, root, filepath.Join(dir, "package-lock.json"), `{"lockfileVersion": 3}`)
		writeFile(t, root, filepath.Join(dir, "node_modules", "agent-package", "package.json"), `{"name": "agent-package"}`)
	}

	if failures := auditDependencyLocality(root); len(failures) > 0 {
		t.Fatalf("expected local agent state dirs to be skipped, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditAgentHooksRequireRepoLocalReconc(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	wrapper := "tools/reconc/bin/hook"
	binaries := strings.Join(reconcDistBinaryTokens(), " ")
	writeFile(t, root, ".codex/config.toml", `hooks = true`)
	writeFile(t, root, ".codex/hooks.json", `{"hooks":{"SessionStart":[{"hooks":[{"command":"repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" codex-session-start \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" codex-session-start codex-pre-tool-use codex-post-tool-use codex-stop \"$repo\""}]}]}}`)
	writeFile(t, root, ".cursor/hooks.json", `{"version":1,"hooks":{"sessionStart":[{"command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-session-start \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" cursor-session-start \"$repo\"'","failClosed":true}],"beforeSubmitPrompt":[{"command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-user-prompt-submit \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" cursor-user-prompt-submit \"$repo\"'","failClosed":true}],"preToolUse":[{"command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-pre-tool-use \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" cursor-pre-tool-use \"$repo\"'","failClosed":true,"matcher":"Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabWrite"}],"postToolUse":[{"command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-post-tool-use \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" cursor-post-tool-use \"$repo\"'","failClosed":true,"matcher":"Read|Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabRead|TabWrite"}],"beforeShellExecution":[{"command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-before-shell-execution \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" cursor-before-shell-execution \"$repo\"'","failClosed":true}],"afterShellExecution":[{"command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-after-shell-execution \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" cursor-after-shell-execution \"$repo\"'","failClosed":true}],"afterFileEdit":[{"command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-after-file-edit \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" cursor-after-file-edit \"$repo\"'","failClosed":true}],"afterTabFileEdit":[{"command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-after-tab-file-edit \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" cursor-after-tab-file-edit \"$repo\"'","failClosed":true}],"stop":[{"command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" cursor-stop \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" cursor-stop \"$repo\"'","failClosed":true}]}}`)
	writeFile(t, root, ".claude/settings.json", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"${CLAUDE_PROJECT_DIR}/`+wrapper+`","args":["claude-session-start","$CLAUDE_PROJECT_DIR"]}]}],"PreToolUse":[{"matcher":"Edit|Write|MultiEdit|Bash","hooks":[{"type":"command","command":"${CLAUDE_PROJECT_DIR}/`+wrapper+`","args":["claude-pre-tool-use","$CLAUDE_PROJECT_DIR"]}]}],"PostToolUse":[{"matcher":"Read|Edit|Write|MultiEdit|Bash","hooks":[{"type":"command","command":"${CLAUDE_PROJECT_DIR}/`+wrapper+`","args":["claude-post-tool-use","$CLAUDE_PROJECT_DIR"]}]}],"Stop":[{"hooks":[{"type":"command","command":"${CLAUDE_PROJECT_DIR}/`+wrapper+`","args":["claude-stop","$CLAUDE_PROJECT_DIR"]}]}]}}`)
	writeFile(t, root, ".opencode/plugins/reconc.js", `// Managed by reconc.
const reconcBinaryCandidates = "`+binaries+`"
const reconcArgs = (event) => ["hook", "runtime", event, repo]
await run("opencode-session-start", payload)
await run("opencode-pre-tool-use", payload)
await run("opencode-post-tool-use", payload)
await run("opencode-stop", payload)
if (event?.type === "session.idle") await maybeAutocontinue(event, result)
if (event?.type === "session.interrupted_by_user") await disableRunloop(state, "user_interrupt")
await clearStopFile()
await run("opencode-stop", { session_id: sessionID, opencode_continuation_driver: true })
await client.session.prompt({ path: { id: sessionID }, body: { parts: [{ type: "text", text: "runloop autocontinue .reconc/runloop" }] } })
`)
	writeFile(t, root, ".agents/hooks.json", `{"reconc":{"PreInvocation":[{"type":"command","command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" antigravity-pre-invocation \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" antigravity-pre-invocation \"$repo\"'","timeout":120}],"PreToolUse":[{"matcher":"write_to_file|replace_file_content|multi_replace_file_content|run_command","hooks":[{"type":"command","command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" antigravity-pre-tool-use \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" antigravity-pre-tool-use \"$repo\"'","timeout":120}]}],"PostToolUse":[{"matcher":"view_file|write_to_file|replace_file_content|multi_replace_file_content|list_dir|find_by_name|grep_search|run_command","hooks":[{"type":"command","command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" antigravity-post-tool-use \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" antigravity-post-tool-use \"$repo\"'","timeout":120}]}],"PostInvocation":[{"type":"command","command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" antigravity-post-invocation \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" antigravity-post-invocation \"$repo\"'","timeout":120}],"Stop":[{"type":"command","command":"sh -lc 'repo=\".\"; hook=\"$repo/tools/reconc/bin/hook\"; if [ -x \"$hook\" ]; then exec \"$hook\" antigravity-stop \"$repo\"; fi; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/`+wrapper+`\" antigravity-stop \"$repo\"'","timeout":120}]}}`)

	if failures := auditAgentHooks(root); len(failures) > 0 {
		t.Fatalf("expected valid agent hooks, got failures:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditHookLauncherShapeRejectsGitFirstLauncher(t *testing.T) {
	content := `{"hooks":{"PreToolUse":[{"hooks":[{"command":"repo=\".\"; repo=\"$(git -C \"$repo\" rev-parse --show-toplevel 2>/dev/null || printf \"%s\" \"$repo\")\"; RECONC_HOOK_REPO_RESOLVED=1 exec \"$repo/tools/reconc/bin/hook\" codex-pre-tool-use \"$repo\""}]}]}}`
	failures := auditHookLauncherShape(".codex/hooks.json", content)
	if len(failures) == 0 {
		t.Fatal("expected git-first hook launcher to fail fast-path audit")
	}
	if !strings.Contains(strings.Join(failures, "\n"), "missing fast-launch token") {
		t.Fatalf("expected missing fast-path token failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditHookLauncherShapeRejectsClaudeShellLauncher(t *testing.T) {
	content := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"sh -lc 'repo=\"$CLAUDE_PROJECT_DIR\"; git -C \"$repo\" rev-parse --show-toplevel; exec \"$repo/tools/reconc/bin/hook\" claude-session-start \"$repo\""}]}]}}`
	failures := auditHookLauncherShape(".claude/settings.json", content)
	if len(failures) == 0 {
		t.Fatal("expected Claude shell launcher to fail exec-form audit")
	}
	joined := strings.Join(failures, "\n")
	if !strings.Contains(joined, "shell/git launcher") || !strings.Contains(joined, "missing exec-form args") {
		t.Fatalf("expected shell/git and args failures, got:\n%s", joined)
	}
}

func TestAuditStartEntrypointRequiresRunloopOptIn(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "start.md", `# START

AGENTS.md
docs/tasks.md
tools/reconc/dist/reconc-0.5.0-darwin-arm64 status .
tools/reconc/dist/reconc-0.5.0-darwin-arm64 session-briefing .
No file writes
_drop/
/runloop
tools/reconc/harness/template/utils/runloop.go
`)
	writeFile(t, root, "AGENTS.md", "`/runloop` is always-on and can invent a parallel process")

	failures := auditStartEntrypoint(root)
	if !containsFailure(failures, "Otherwise Runloop is off") ||
		!containsFailure(failures, "not a parallel workflow") ||
		!containsFailure(failures, "AGENTS.md missing required runloop token") {
		t.Fatalf("expected runloop opt-in failures, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditSpecAuditArtifactsNoopsBeforeWorkflowStarts(t *testing.T) {
	root := t.TempDir()
	writeSpecAuditBase(t, root, "alpha\nbeta", specAuditState("NOT_STARTED", 2, "none", ""))

	if failures := auditSpecAuditArtifacts(root); len(failures) > 0 {
		t.Fatalf("not-started spec audit must not enforce artifact rows, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditSpecAuditArtifactsPassesCompletedRange(t *testing.T) {
	root := t.TempDir()
	writeSpecAuditBase(t, root, "alpha\nbeta", specAuditState("IN_PROGRESS", 2, "docs/spec.md:L2", `| docs/spec.md:L1-L2 | codex | 2026-05-22 | 2 | 0 | 0 | MATCH=2 | docs/spec-audit/ranges/L0001-L0002.md |`))
	writeFile(t, root, "backend/project/a.go", "package project\n")
	writeFile(t, root, "backend/project/a_test.go", "package project\n")
	writeFile(t, root, "backend/project/b.go", "package project\n")
	writeFile(t, root, "backend/project/b_test.go", "package project\n")
	writeFile(t, root, "docs/spec-audit/spec-atoms.md", specAuditAtomsDoc(`| ATOM-L0001-01 | docs/spec.md:L1 | alpha requirement | workflow | none | backend/project/a.go | go test ./... | PASS | PASS | PASS | PASS | PASS | backend/project/a.go:L1; backend/project/a_test.go:L1 | MATCH | none |
| ATOM-L0001-02 | docs/spec.md:L2 | beta requirement | workflow | none | backend/project/b.go | go test ./... | PASS | PASS | PASS | PASS | PASS | backend/project/b.go:L1; backend/project/b_test.go:L1 | MATCH | none |`))
	writeFile(t, root, "docs/spec-audit/ranges/L0001-L0002.md", `# Range L0001-L0002

## Spec Lines Read
docs/spec.md:L1-L2

## Atom Table
ATOM-L0001-01
ATOM-L0001-02

## Implementation Evidence
backend/project/a.go:L1

## Gaps
none

## Range Reality Check
PASS
`)

	if failures := auditSpecAuditArtifacts(root); len(failures) > 0 {
		t.Fatalf("expected completed artifact set to pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditSpecAuditArtifactsRejectsMissingLineCoverage(t *testing.T) {
	root := t.TempDir()
	writeSpecAuditBase(t, root, "alpha\nbeta\ngamma", specAuditState("IN_PROGRESS", 3, "docs/spec.md:L3", `| docs/spec.md:L1-L3 | codex | 2026-05-22 | 2 | 0 | 0 | PARTIAL | docs/spec-audit/ranges/L0001-L0003.md |`))
	writeFile(t, root, "docs/spec-audit/spec-atoms.md", specAuditAtomsDoc(`| ATOM-L0001-01 | docs/spec.md:L1-L2 | partial coverage | workflow | none | backend/project/a.go | go test ./... | PASS | PASS | PASS | PASS | PASS | backend/project/a.go:L1; backend/project/a_test.go:L1 | MATCH | none |`))
	writeFile(t, root, "docs/spec-audit/ranges/L0001-L0003.md", `# Range L0001-L0003

## Spec Lines Read
docs/spec.md:L1-L3

## Atom Table
ATOM-L0001-01

## Implementation Evidence
backend/project/a.go:L1

## Gaps
none

## Range Reality Check
PASS
`)

	failures := auditSpecAuditArtifacts(root)
	if !containsFailure(failures, "missing atom coverage for docs/spec.md:L3") {
		t.Fatalf("expected missing line coverage failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditSpecAuditArtifactsRejectsPassingAtomWithoutEvidence(t *testing.T) {
	root := t.TempDir()
	writeSpecAuditBase(t, root, "alpha", specAuditState("IN_PROGRESS", 1, "docs/spec.md:L1", `| docs/spec.md:L1-L1 | codex | 2026-05-22 | 1 | 0 | 0 | MATCH=1 | docs/spec-audit/ranges/L0001-L0001.md |`))
	writeFile(t, root, "docs/spec-audit/spec-atoms.md", specAuditAtomsDoc(`| ATOM-L0001-01 | docs/spec.md:L1 | alpha requirement | workflow | none | backend/project/a.go | go test ./... | PASS | PASS | PASS | PASS | PASS |  | MATCH | none |`))
	writeFile(t, root, "docs/spec-audit/ranges/L0001-L0001.md", `# Range L0001-L0001

## Spec Lines Read
docs/spec.md:L1

## Atom Table
ATOM-L0001-01

## Implementation Evidence
pending

## Gaps
none

## Range Reality Check
PASS
`)

	failures := auditSpecAuditArtifacts(root)
	if !containsFailure(failures, "MATCH verdict requires implementation evidence") {
		t.Fatalf("expected missing evidence failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func newWorkflowAuditRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs/tasks/done"), 0o755); err != nil {
		t.Fatalf("mkdir task dirs: %v", err)
	}
	writeDefaultStackConfig(t, root)
	return root
}

func writeDefaultStackConfig(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, stackConfigRel, `stack: go-cli
project: project
layout: auto
build:
  enabled: true
  language: go
  require_go_mod: true
  require_build_runner: true
  require_build_runner_test: true
  backend_entrypoints:
    - project
  go_mod_tokens:
    - "module "
    - "go "
  build_runner_tokens:
    - 'case "build":'
    - 'case "test":'
    - 'case "lint":'
    - 'case "validate":'
    - 'case "clean":'
durable_store:
  enabled: false
generated_references:
  enabled: false
architecture_boundaries:
  required: false
agent_hooks:
  require_codex_config: true
  require_codex_hook_file: true
  require_claude_settings: true
  require_opencode_plugin: true
`)
}

func writeDurableStoreStackConfig(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, stackConfigRel, `stack: go-cli
project: project
layout: auto
build:
  enabled: true
  language: go
durable_store:
  enabled: true
  store_files:
    - "backend/{project}/internal/store/store.go"
    - "backend/{project}/internal/store/hash.go"
    - "backend/{project}/internal/store/store_test.go"
  migration_go_files:
    - "db/migrations/migrations.go"
    - "db/migrations/migrations_test.go"
  initial_sql: "db/migrations/{project}/core/001_initial.sql"
  store_go_tokens:
    - "PRAGMA journal_mode=WAL"
    - "PRAGMA auto_vacuum=INCREMENTAL"
    - "migration_run_ledger"
    - "project_migrations_core"
    - "SnapshotCore"
    - "IntegrityCheck"
    - "github.com/mattn/go-sqlite3"
  initial_sql_tokens:
    - "durable_store_contracts"
    - "sessions"
    - "messages"
    - "knowledge_nodes"
    - "knowledge_edges"
    - "retrieval_quality"
    - "skill_records"
    - "CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_fts"
generated_references:
  enabled: false
architecture_boundaries:
  required: false
agent_hooks:
  require_codex_config: true
  require_codex_hook_file: true
  require_claude_settings: true
  require_opencode_plugin: true
`)
}

func writeFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func gitRun(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func minimalTasksMd() string {
	return `# Tasks

Current: TASK-0001-Active-Work -> tasks/TASK-0001-Active-Work.md

- [ ] TASK-0001-Active-Work - active work -> tasks/TASK-0001-Active-Work.md
`
}

func taskDetail(name string, state string, subtasks string, final string) string {
	return "# " + name + `

## Why

Keep the workflow resumable and enforceable.

## Status

State: ` + state + `

## Scheduling

- Priority: P1
- Depends On: none
- Parallel Group: none
- Expected Touch Surfaces: docs/tasks/**
- Order Rationale: This task is ordered by dependency readiness and queue efficiency.
- Scope Type: Audit Repair
- Spec Lines: none
- Research Refs: none
- Completion Claim: Done means the declared workflow audit repair is implemented, tested if code changed, and verified by Reconc.

## Technical Plan

- Execute a concrete workflow audit step.

## Acceptance

- The workflow audit has a measurable pass/fail condition.

## Sub-Tasks

` + subtasks + `

## Notes

Concrete resume context.

## Deviations

None.
` + final
}

func finalRealityCheck() string {
	return `

## Final Reality Check

- Spec Parity: NO_SPEC_SURFACE
- Spec Scope: no spec surface touched
- Reality Check: PASS - workflow evidence is deterministic
- Reality Check Loop: PASS - 2 passes, nothing left
- Tests: NO_CODE_CHANGED
- Evidence: auditTaskState fixture
- Beyond Spec Handling: N/A
`
}

func writeSpecAuditBase(t *testing.T, root string, spec string, state string) {
	t.Helper()
	writeFile(t, root, "docs/spec.md", spec)
	writeFile(t, root, "docs/spec-audit/state.md", state)
	writeFile(t, root, "docs/spec-audit/spec-atoms.md", specAuditAtomsDoc(""))
	writeFile(t, root, "docs/spec-audit/research-floor.md", `# Research Floor Extraction

## Research Floors
| Research Floor ID | Source Ref | Linked Atom IDs | Useful Behavior | Edge Cases | Failure Handling | Data/Protocol Details | Algorithms/Performance Ideas | Test Ideas | Optional Useful Capabilities | Target Adaptation | Carry-Over Decision | Gap IDs |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
`)
	writeFile(t, root, "docs/spec-audit/gaps.md", `# Spec-Code Gap Inventory

## Gap Records
`)
}

func specAuditState(status string, specLineCount int, lastFullyVerified string, completedRows string) string {
	if lastFullyVerified == "" {
		lastFullyVerified = "none"
	}
	return fmt.Sprintf(`# Spec-Code-Parity Audit State

## Audit Status
- Status: %s
- Spec Line Count: %d
- Last Fully Verified Line: %s

## Active Claims
| Claim ID | Agent/Runtime | Session | Spec Range | Phase | Status | Started | Last Touch | Worktree Commit | Last Processed Line | Last Open Atom | Notes |
|---|---|---|---|---|---|---|---|---|---|---|---|

## Completed Ranges
| Spec Range | Completed By | Completed At | Atom Count | Research Refs Read | Gap Count | Verdict Summary | Evidence |
|---|---|---|---:|---:|---:|---|---|
%s

## Blocked Ranges
| Spec Range | Blocker | Evidence | Required Decision | Created |
|---|---|---|---|---|
`, status, specLineCount, lastFullyVerified, completedRows)
}

func specAuditAtomsDoc(rows string) string {
	return `# Spec Audit Atom Index

## Atoms
| Atom ID | Spec Lines | Requirement | Class | Research Refs | Expected Owner Paths | Required Tests/Proof | Spec Status | Research Status | Implementation Status | Test Status | Quality Bar Status | Implementation Evidence | Verdict | Gap IDs |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
` + rows + "\n"
}

// TestAuditTaskStateAllowsHTMLCommentHeader pins the parser's tolerance for
// the APPEND-ONLY HTML comment block that sits between `# Tasks` and the
// `Current:` control line. parseTaskIndex must skip the comment lines (they
// match neither `Current: `, `- [`, `## `, `# Tasks`, nor empty) and the
// current-position-before-entries check must still pass since the comment
// only pushes Current down a few lines without moving any TASK row above it.
func TestAuditTaskStateAllowsHTMLCommentHeader(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

<!--
APPEND-ONLY LOGBOOK — do not delete, reorder, or rewrite rows.
Allowed mutations only:
  - append a new row at the end
  - on Done flip [ ] to [x] AND swap tasks/ to tasks/done/
  - edit the single Current: control line
Forbidden: delete any row, multi-row delete, full-file rewrite.
-->

Current: TASK-0001-Active-Work -> tasks/TASK-0001-Active-Work.md

- [ ] TASK-0001-Active-Work - active work -> tasks/TASK-0001-Active-Work.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-Active-Work.md", taskDetail("TASK-0001-Active-Work", "Active", "- [~] Continue here", ""))

	if failures := auditTaskState(root); len(failures) > 0 {
		t.Fatalf("HTML comment header must not produce audit failures, got:\n%s", strings.Join(failures, "\n"))
	}
}

// gitInitRepoForRowsImmutable bootstraps a tempdir into a minimal git repo
// and commits an initial docs/tasks.md so auditTasksMdRowsImmutable has a
// HEAD to compare against. Returns the repo root.
func gitInitRepoForRowsImmutable(t *testing.T, initialTasksMd string) string {
	t.Helper()
	root := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-q"},
		{"git", "config", "user.email", "test@example.invalid"},
		{"git", "config", "user.name", "test"},
		{"git", "config", "commit.gpgsign", "false"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(c, " "), err, string(out))
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/tasks.md"), []byte(initialTasksMd), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := [][]string{
		{"git", "add", "docs/tasks.md"},
		{"git", "commit", "-q", "-m", "initial"},
	}
	for _, c := range commit {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(c, " "), err, string(out))
		}
	}
	return root
}

func TestAuditTasksMdRowsImmutableNoHead(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/tasks.md"), []byte("# Tasks\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if failures := auditTasksMdRowsImmutable(root); len(failures) > 0 {
		t.Fatalf("expected no failures when HEAD is absent, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTasksMdRowsImmutableDetectsRowDeletion(t *testing.T) {
	initial := `# Tasks

Current: TASK-0002-Active -> tasks/TASK-0002-Active.md

- [x] TASK-0001-Done - finished work -> tasks/done/TASK-0001-Done.md
- [ ] TASK-0002-Active - active work -> tasks/TASK-0002-Active.md
`
	root := gitInitRepoForRowsImmutable(t, initial)
	truncated := `# Tasks

Current: TASK-0002-Active -> tasks/TASK-0002-Active.md

- [ ] TASK-0002-Active - active work -> tasks/TASK-0002-Active.md
`
	if err := os.WriteFile(filepath.Join(root, "docs/tasks.md"), []byte(truncated), 0o644); err != nil {
		t.Fatal(err)
	}
	failures := auditTasksMdRowsImmutable(root)
	if !containsFailure(failures, "TASK-0001-Done") {
		t.Fatalf("expected deletion failure for TASK-0001-Done, got:\n%s", strings.Join(failures, "\n"))
	}
	if !containsFailure(failures, "missing from working tree") {
		t.Fatalf("expected 'missing from working tree' message, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTasksMdRowsImmutableDetectsReverseFlip(t *testing.T) {
	initial := `# Tasks

Current: TASK-0002-Active -> tasks/TASK-0002-Active.md

- [x] TASK-0001-Done - finished work -> tasks/done/TASK-0001-Done.md
- [ ] TASK-0002-Active - active work -> tasks/TASK-0002-Active.md
`
	root := gitInitRepoForRowsImmutable(t, initial)
	reversed := `# Tasks

Current: TASK-0002-Active -> tasks/TASK-0002-Active.md

- [ ] TASK-0001-Done - finished work -> tasks/TASK-0001-Done.md
- [ ] TASK-0002-Active - active work -> tasks/TASK-0002-Active.md
`
	if err := os.WriteFile(filepath.Join(root, "docs/tasks.md"), []byte(reversed), 0o644); err != nil {
		t.Fatal(err)
	}
	failures := auditTasksMdRowsImmutable(root)
	if !containsFailure(failures, "TASK-0001-Done") || !containsFailure(failures, "reverse-flip") {
		t.Fatalf("expected reverse-flip failure for TASK-0001-Done, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTasksMdRowsImmutableAllowsLegitimateFlip(t *testing.T) {
	initial := `# Tasks

Current: TASK-0001-Work -> tasks/TASK-0001-Work.md

- [ ] TASK-0001-Work - in progress -> tasks/TASK-0001-Work.md
- [ ] TASK-0002-Next - queued -> tasks/TASK-0002-Next.md
`
	root := gitInitRepoForRowsImmutable(t, initial)
	flipped := `# Tasks

Current: TASK-0002-Next -> tasks/TASK-0002-Next.md

- [x] TASK-0001-Work - in progress -> tasks/done/TASK-0001-Work.md
- [ ] TASK-0002-Next - queued -> tasks/TASK-0002-Next.md
`
	if err := os.WriteFile(filepath.Join(root, "docs/tasks.md"), []byte(flipped), 0o644); err != nil {
		t.Fatal(err)
	}
	if failures := auditTasksMdRowsImmutable(root); len(failures) > 0 {
		t.Fatalf("legitimate flip must pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTasksMdRowsImmutableAllowsAppend(t *testing.T) {
	initial := `# Tasks

Current: TASK-0001-Work -> tasks/TASK-0001-Work.md

- [ ] TASK-0001-Work - in progress -> tasks/TASK-0001-Work.md
`
	root := gitInitRepoForRowsImmutable(t, initial)
	appended := `# Tasks

Current: TASK-0001-Work -> tasks/TASK-0001-Work.md

- [ ] TASK-0001-Work - in progress -> tasks/TASK-0001-Work.md
- [ ] TASK-0002-New - new queued -> tasks/TASK-0002-New.md
`
	if err := os.WriteFile(filepath.Join(root, "docs/tasks.md"), []byte(appended), 0o644); err != nil {
		t.Fatal(err)
	}
	if failures := auditTasksMdRowsImmutable(root); len(failures) > 0 {
		t.Fatalf("append must pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditGitHooksFlagsMissingHook(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(out))
	}
	failures := auditGitHooks(root)
	if !containsFailure(failures, ".githooks/pre-commit missing") {
		t.Fatalf("expected missing-hook failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditGitHooksFlagsMissingHooksPath(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(out))
	}
	if err := os.MkdirAll(filepath.Join(root, ".githooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".githooks/pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	failures := auditGitHooks(root)
	if !containsFailure(failures, "core.hooksPath") {
		t.Fatalf("expected core.hooksPath failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditGitHooksPassesWhenActivated(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(out))
	}
	if err := os.MkdirAll(filepath.Join(root, ".githooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".githooks/pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "config", "core.hooksPath", ".githooks").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, string(out))
	}
	if failures := auditGitHooks(root); len(failures) > 0 {
		t.Fatalf("activated hook setup must pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

// TestParseTaskIndexIgnoresHTMLComment is the focused unit-level guard for
// the parser itself: every line of a multi-line HTML comment must fall
// through the `continue` branch at parseTaskIndex line 389 without raising
// "invalid task entry format" or "sections are forbidden".
func TestParseTaskIndexIgnoresHTMLComment(t *testing.T) {
	content := `# Tasks

<!--
multi-line
comment with - [ ] looking text and ## not a section
-->

Current: TASK-0001-X -> tasks/TASK-0001-X.md

- [ ] TASK-0001-X - desc -> tasks/TASK-0001-X.md
`
	index, failures := parseTaskIndex(content)
	if len(failures) > 0 {
		t.Fatalf("parser must ignore HTML comments, got failures:\n%s", strings.Join(failures, "\n"))
	}
	if index.currentName != "TASK-0001-X" {
		t.Fatalf("expected Current = TASK-0001-X, got %q", index.currentName)
	}
	if len(index.entries) != 1 || index.entries[0].name != "TASK-0001-X" {
		t.Fatalf("expected one entry TASK-0001-X, got %+v", index.entries)
	}
}

func containsFailure(failures []string, needle string) bool {
	for _, failure := range failures {
		if strings.Contains(failure, needle) {
			return true
		}
	}
	return false
}
