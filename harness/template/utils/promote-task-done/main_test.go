package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const finalRealityCheck = `

## Final Reality Check

- Spec Parity: NO_SPEC_SURFACE
- Spec Scope: no spec surface touched
- Reality Check: PASS - workflow tooling proves itself
- Reality Check Loop: PASS - 2 passes, nothing left
- Tests: NO_CODE_CHANGED
- Evidence: promote-task-done test fixture
- Beyond Spec Handling: N/A
`

func taskDetail(name string, state string, subTasks string, withFinal bool) string {
	body := "# " + name + `

## Why

Keep the workflow resumable.

## Status

State: ` + state + `

## Scheduling

- Priority: P1
- Depends On: none
- Parallel Group: none
- Expected Touch Surfaces: docs/tasks/**
- Order Rationale: This task is ordered by dependency readiness and queue efficiency.

## Technical Plan

- Execute a concrete workflow audit step.

## Acceptance

- The workflow audit has a measurable pass/fail condition.

## Sub-Tasks

` + subTasks + `

## Notes

Concrete resume context.

## Deviations

None.
`
	if withFinal {
		body += finalRealityCheck
	}
	return body
}

func writeFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relative, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func readFile(t *testing.T, root string, relative string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relative))
	bytes, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(bytes)
}

func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs/tasks/done"), 0o755); err != nil {
		t.Fatalf("mkdir docs/tasks/done: %v", err)
	}
	return root
}

func defaultOpts() options {
	return options{allowEmptyCurrent: true}
}

func TestResolveCommandRoot(t *testing.T) {
	root := t.TempDir()
	got, err := resolveCommandRoot(root)
	if err != nil {
		t.Fatalf("resolveCommandRoot: %v", err)
	}
	if got != root {
		t.Fatalf("got %q want %q", got, root)
	}
}

func TestRootLauncherCrossesNestedModuleBoundary(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	launcher := filepath.Join(workingDir, "run-promote-task-done")
	info, err := os.Stat(launcher)
	if err != nil {
		t.Fatalf("stat launcher: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		t.Fatalf("launcher is not executable: %s", launcher)
	}
	cmd := exec.Command("sh", launcher, "--help")
	cmd.Dir = t.TempDir()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "usage: promote-task-done") || !strings.Contains(string(output), "--repo-root PATH") {
		t.Fatalf("launcher did not reach the nested utility:\n%s", output)
	}
}

func TestPromoteCurrentWithNextQueued(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-Second - second work -> tasks/TASK-0002-Second.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done item", true))
	writeFile(t, root, "docs/tasks/TASK-0002-Second.md", taskDetail("TASK-0002-Second", "Queued", "- [ ] Pending item", false))

	if err := promote(root, defaultOpts()); err != nil {
		t.Fatalf("promote: %v", err)
	}
	tasks := readFile(t, root, "docs/tasks.md")
	if !strings.Contains(tasks, "Current: TASK-0002-Second -> tasks/TASK-0002-Second.md") {
		t.Fatalf("Current not advanced:\n%s", tasks)
	}
	if !strings.Contains(tasks, "- [x] TASK-0001-First - first work -> tasks/done/TASK-0001-First.md") {
		t.Fatalf("row not flipped:\n%s", tasks)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/tasks/TASK-0001-First.md")); !os.IsNotExist(err) {
		t.Fatalf("source detail still present, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/tasks/done/TASK-0001-First.md")); err != nil {
		t.Fatalf("destination detail missing: %v", err)
	}
	next := readFile(t, root, "docs/tasks/TASK-0002-Second.md")
	if !strings.Contains(next, "State: Active") {
		t.Fatalf("next state not Active:\n%s", next)
	}
	if !strings.Contains(next, "- [~] Pending item") {
		t.Fatalf("next sub-task not promoted to [~]:\n%s", next)
	}
}

func TestPromoteRejectsActiveDetail(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Active", "- [~] Working", true))

	err := promote(root, defaultOpts())
	if err == nil || !strings.Contains(err.Error(), "State: Done") {
		t.Fatalf("expected State Done error, got %v", err)
	}
}

func TestPromoteRejectsOpenSubTasks(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] One done\n- [ ] Still open\n- [ ] Also still open", true))

	err := promote(root, defaultOpts())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Sub-Task still open: - [ ] Still open") || !strings.Contains(err.Error(), "Sub-Task still open: - [ ] Also still open") {
		t.Fatalf("expected line-specific open-subtask errors, got %v", err)
	}
}

func TestPromoteRejectsMissingFinalRealityCheck(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", false))

	err := promote(root, defaultOpts())
	if err == nil || !strings.Contains(err.Error(), "Final Reality Check") {
		t.Fatalf("expected Final Reality Check error, got %v", err)
	}
}

func TestPromoteRejectsBadParity(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`)
	bad := strings.Replace(taskDetail("TASK-0001-First", "Done", "- [x] Done", true), "Spec Parity: NO_SPEC_SURFACE", "Spec Parity: WHATEVER", 1)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", bad)

	err := promote(root, defaultOpts())
	if err == nil || !strings.Contains(err.Error(), "Spec Parity") {
		t.Fatalf("expected Spec Parity error, got %v", err)
	}
}

func TestPromoteRejectsMissingPassPrefix(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`)
	bad := strings.Replace(taskDetail("TASK-0001-First", "Done", "- [x] Done", true), "Reality Check: PASS - ", "Reality Check: ", 1)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", bad)

	err := promote(root, defaultOpts())
	if err == nil || !strings.Contains(err.Error(), "PASS - ") {
		t.Fatalf("expected PASS prefix error, got %v", err)
	}
}

func TestPromoteRejectsNonCurrentTarget(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-Second - second work -> tasks/TASK-0002-Second.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Active", "- [~] Working", false))
	writeFile(t, root, "docs/tasks/TASK-0002-Second.md", taskDetail("TASK-0002-Second", "Done", "- [x] Done", true))

	opts := defaultOpts()
	opts.taskName = "TASK-0002-Second"
	err := promote(root, opts)
	if err == nil || !strings.Contains(err.Error(), "not the Current TASK") {
		t.Fatalf("expected non-current error, got %v", err)
	}
}

func TestPromoteRejectsAlreadyDoneRow(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0002-Second -> tasks/TASK-0002-Second.md

- [x] TASK-0001-First - first work -> tasks/done/TASK-0001-First.md
- [ ] TASK-0002-Second - second work -> tasks/TASK-0002-Second.md
`)
	writeFile(t, root, "docs/tasks/done/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))
	writeFile(t, root, "docs/tasks/TASK-0002-Second.md", taskDetail("TASK-0002-Second", "Active", "- [~] Working", false))

	opts := defaultOpts()
	opts.taskName = "TASK-0001-First"
	err := promote(root, opts)
	if err == nil || !strings.Contains(err.Error(), "not the Current TASK") {
		t.Fatalf("expected not-current error, got %v", err)
	}
}

func TestPromoteWithoutNextRefusesByDefault(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))

	err := promote(root, options{})
	if err == nil || !strings.Contains(err.Error(), "no next executable") {
		t.Fatalf("expected refuse-empty-current error, got %v", err)
	}
	if !strings.Contains(err.Error(), "zero-finding Terminal Gate") || strings.Contains(err.Error(), "Project Complete Candidate") {
		t.Fatalf("expected zero-finding terminal guidance without old final-hold wording, got %v", err)
	}
}

func TestPromoteWithoutNextWithAllowEmpty(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))

	if err := promote(root, options{allowEmptyCurrent: true}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	tasks := readFile(t, root, "docs/tasks.md")
	if !strings.Contains(tasks, "- [x] TASK-0001-First") {
		t.Fatalf("row not flipped:\n%s", tasks)
	}
}

func TestPromoteSkipsBlockedNext(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-Blocked - blocked work -> tasks/TASK-0002-Blocked.md
- [ ] TASK-0003-Ready - ready work -> tasks/TASK-0003-Ready.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))
	writeFile(t, root, "docs/tasks/TASK-0002-Blocked.md", taskDetail("TASK-0002-Blocked", "Blocked", "- [ ] Wait", false))
	writeFile(t, root, "docs/tasks/TASK-0003-Ready.md", taskDetail("TASK-0003-Ready", "Queued", "- [ ] Start me", false))

	if err := promote(root, defaultOpts()); err != nil {
		t.Fatalf("promote: %v", err)
	}
	tasks := readFile(t, root, "docs/tasks.md")
	if !strings.Contains(tasks, "Current: TASK-0003-Ready -> tasks/TASK-0003-Ready.md") {
		t.Fatalf("Current should skip Blocked and pick TASK-0003-Ready:\n%s", tasks)
	}
}

func TestPromoteRequiresUnsatisfiedDepsInNext(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-NeedsThird - needs third -> tasks/TASK-0002-NeedsThird.md
- [ ] TASK-0003-Ready - ready work -> tasks/TASK-0003-Ready.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))
	dep := strings.Replace(taskDetail("TASK-0002-NeedsThird", "Queued", "- [ ] Wait", false), "Depends On: none", "Depends On: TASK-0003-Ready", 1)
	writeFile(t, root, "docs/tasks/TASK-0002-NeedsThird.md", dep)
	writeFile(t, root, "docs/tasks/TASK-0003-Ready.md", taskDetail("TASK-0003-Ready", "Queued", "- [ ] Start me", false))

	if err := promote(root, defaultOpts()); err != nil {
		t.Fatalf("promote: %v", err)
	}
	tasks := readFile(t, root, "docs/tasks.md")
	if !strings.Contains(tasks, "Current: TASK-0003-Ready") {
		t.Fatalf("Current must skip task with unmet deps:\n%s", tasks)
	}
}

func TestPromoteDryRunDoesNotWrite(t *testing.T) {
	root := newRepo(t)
	original := `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`
	writeFile(t, root, "docs/tasks.md", original)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))

	opts := defaultOpts()
	opts.dryRun = true
	if err := promote(root, opts); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	tasks := readFile(t, root, "docs/tasks.md")
	if tasks != original {
		t.Fatalf("dry-run mutated tasks.md:\n%s", tasks)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/tasks/TASK-0001-First.md")); err != nil {
		t.Fatalf("dry-run moved detail file: %v", err)
	}
}

func TestPromoteRejectsExistingDestination(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))
	writeFile(t, root, "docs/tasks/done/TASK-0001-First.md", "stale duplicate")

	err := promote(root, defaultOpts())
	if err == nil || !strings.Contains(err.Error(), "destination") {
		t.Fatalf("expected destination-exists error, got %v", err)
	}
}

func TestPromoteRefusesNonCurrentByName(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-Future - future work -> tasks/TASK-0002-Future.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Active", "- [~] working", false))
	writeFile(t, root, "docs/tasks/TASK-0002-Future.md", taskDetail("TASK-0002-Future", "Done", "- [x] Done", true))

	opts := defaultOpts()
	opts.taskName = "TASK-0002-Future"
	err := promote(root, opts)
	if err == nil || !strings.Contains(err.Error(), "not the Current TASK") {
		t.Fatalf("expected non-current refusal, got %v", err)
	}
}

func TestPromoteIdempotentWhenNextSubTaskAlreadyActive(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-Second - second work -> tasks/TASK-0002-Second.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))
	writeFile(t, root, "docs/tasks/TASK-0002-Second.md", taskDetail("TASK-0002-Second", "Queued", "- [~] already active\n- [ ] later", false))

	if err := promote(root, defaultOpts()); err != nil {
		t.Fatalf("promote: %v", err)
	}
	next := readFile(t, root, "docs/tasks/TASK-0002-Second.md")
	if strings.Count(next, "- [~]") != 1 {
		t.Fatalf("expected exactly one [~], got:\n%s", next)
	}
}

func TestPromoteLockingPreventsConcurrentMutation(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))

	lockPath := filepath.Join(root, filepath.FromSlash(lockRel))
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	holder, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	defer holder.Close()
	unlock, err := tryPromoteLock(holder)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer unlock()

	err = runWithLock(root, options{allowEmptyCurrent: true})
	if err == nil || !strings.Contains(err.Error(), "promote-task-done holds") {
		t.Fatalf("expected lock-busy error, got %v", err)
	}
}

func TestPromoteParallelWithLockHasOneWinner(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-Second - second work -> tasks/TASK-0002-Second.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))
	writeFile(t, root, "docs/tasks/TASK-0002-Second.md", taskDetail("TASK-0002-Second", "Queued", "- [ ] Pending", false))

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = runWithLock(root, defaultOpts())
		}(i)
	}
	wg.Wait()
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful promotion, got %d (errs=%v)", successes, results)
	}
}

func TestVerifyFlagRollsBackOnAuditFailure(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-Second - second work -> tasks/TASK-0002-Second.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))
	writeFile(t, root, "docs/tasks/TASK-0002-Second.md", taskDetail("TASK-0002-Second", "Queued", "- [ ] Pending", false))

	stub := filepath.Join(root, filepath.FromSlash("tools/reconc/harness/template/audits/run-workflow-audit"))
	if err := os.MkdirAll(filepath.Dir(stub), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho synthetic-audit-failure\nexit 2\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	originalTasks := readFile(t, root, "docs/tasks.md")
	originalSecond := readFile(t, root, "docs/tasks/TASK-0002-Second.md")

	opts := defaultOpts()
	opts.verify = true
	err := promote(root, opts)
	if err == nil || !strings.Contains(err.Error(), "post-mutation verify failed") {
		t.Fatalf("expected verify failure, got %v", err)
	}
	if got := readFile(t, root, "docs/tasks.md"); got != originalTasks {
		t.Fatalf("tasks.md not rolled back:\n%s", got)
	}
	if got := readFile(t, root, "docs/tasks/TASK-0002-Second.md"); got != originalSecond {
		t.Fatalf("next detail not rolled back:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/tasks/TASK-0001-First.md")); err != nil {
		t.Fatalf("source detail not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/tasks/done/TASK-0001-First.md")); !os.IsNotExist(err) {
		t.Fatalf("destination detail still exists after rollback: %v", err)
	}
}

func TestVerifyFlagPassesOnAuditSuccess(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-Second - second work -> tasks/TASK-0002-Second.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", taskDetail("TASK-0001-First", "Done", "- [x] Done", true))
	writeFile(t, root, "docs/tasks/TASK-0002-Second.md", taskDetail("TASK-0002-Second", "Queued", "- [ ] Pending", false))

	stub := filepath.Join(root, filepath.FromSlash("tools/reconc/harness/template/audits/run-workflow-audit"))
	if err := os.MkdirAll(filepath.Dir(stub), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	opts := defaultOpts()
	opts.verify = true
	if err := promote(root, opts); err != nil {
		t.Fatalf("promote: %v", err)
	}
	tasks := readFile(t, root, "docs/tasks.md")
	if !strings.Contains(tasks, "Current: TASK-0002-Second") {
		t.Fatalf("expected Current advanced after verify pass, got:\n%s", tasks)
	}
}

// TestIntegrationWithRealAudit runs promote on a synthetic repo and then
// invokes the real run-workflow-audit binary's task-state mode against the
// resulting state. This is the canonical proof that the tool's mutation
// matches the audit's expectations end-to-end.
func TestIntegrationWithRealAudit(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0001-First -> tasks/TASK-0001-First.md

- [ ] TASK-0001-First - first work -> tasks/TASK-0001-First.md
- [ ] TASK-0002-Second - second work -> tasks/TASK-0002-Second.md
`)
	writeFile(t, root, "docs/tasks/TASK-0001-First.md", `# TASK-0001-First

## Why

First TASK is the Current item that we will promote in this fixture; Done has been signed off.

## Status

State: Done

## Scheduling

- Priority: P1
- Depends On: none
- Parallel Group: none
- Expected Touch Surfaces: docs/tasks/**
- Order Rationale: This task sits here because its outputs unblock the dependency layer aligned with the spec ownership graph.
- Scope Type: Audit Repair
- Spec Lines: none
- Spec Bindings: none
- Research Refs: none
- Completion Claim: Done means the workflow audit fixture remains valid against the real task-state audit.

## Technical Plan

- Run the workflow audit step with same-TASK tests.

## Acceptance

- The workflow audit has a measurable pass condition with test coverage.

## Sub-Tasks

- [x] Done item with test coverage

## Notes

Concrete resume context.

## Deviations

None.

## Final Reality Check

- Spec Parity: NO_SPEC_SURFACE
- Spec Scope: no spec surface touched
- Reality Check: PASS - workflow tooling proves itself
- Reality Check Loop: PASS - 2 passes, nothing left
- Tests: NO_CODE_CHANGED
- Evidence: integration fixture
- Beyond Spec Handling: N/A
`)
	writeFile(t, root, "docs/tasks/TASK-0002-Second.md", `# TASK-0002-Second

## Why

Second TASK is the next executable item; tests are part of its plan.

## Status

State: Queued

## Scheduling

- Priority: P1
- Depends On: none
- Parallel Group: none
- Expected Touch Surfaces: docs/tasks/**
- Order Rationale: This task sits here because its outputs unblock the dependency layer aligned with the spec ownership graph.
- Scope Type: Audit Repair
- Spec Lines: none
- Spec Bindings: none
- Research Refs: none
- Completion Claim: Done means the workflow audit fixture remains valid against the real task-state audit.

## Technical Plan

- Run the workflow audit step with same-TASK tests.

## Acceptance

- The workflow audit has a measurable pass condition with test coverage.

## Sub-Tasks

- [ ] Pending item with test coverage

## Notes

Concrete resume context.

## Deviations

None.
`)

	if err := promote(root, defaultOpts()); err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Find the real audit binary under the actual repo (test runs in temp).
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := wd
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err == nil {
			break
		}
		repoRoot = filepath.Dir(repoRoot)
	}
	auditBin := filepath.Join(t.TempDir(), "workflow-audit")
	if runtime.GOOS == "windows" {
		auditBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", auditBin, "./audits")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build audit binary: %v\n%s", err, out)
	}
	cmd := exec.Command(auditBin, "task-state")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real audit task-state failed:\n%s", string(output))
	}
}
