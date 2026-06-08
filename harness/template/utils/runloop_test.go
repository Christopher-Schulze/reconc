package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRunLoopStateUsesCurrentAndActiveSubtask(t *testing.T) {
	root := t.TempDir()
	writeRunLoopFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0007-Test-Task -> tasks/TASK-0007-Test-Task.md

- [ ] TASK-0007-Test-Task - test task -> tasks/TASK-0007-Test-Task.md
`)
	writeRunLoopFile(t, root, "docs/tasks/TASK-0007-Test-Task.md", `# TASK-0007-Test-Task

## Status

State: Active

## Sub-Tasks

- [x] Done
- [~] Continue exact step
- [ ] Later step
`)

	state, err := readRunLoopState(root)
	if err != nil {
		t.Fatalf("readRunLoopState: %v", err)
	}
	if state.currentName != "TASK-0007-Test-Task" {
		t.Fatalf("currentName = %q", state.currentName)
	}
	if state.currentPath != "docs/tasks/TASK-0007-Test-Task.md" {
		t.Fatalf("currentPath = %q", state.currentPath)
	}
	if state.state != "Active" {
		t.Fatalf("state = %q", state.state)
	}
	if state.nextStep != "Continue exact step" {
		t.Fatalf("nextStep = %q", state.nextStep)
	}
}

func TestReadRunLoopStateFallsBackToOpenSubtask(t *testing.T) {
	root := t.TempDir()
	writeRunLoopFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0008-Test-Task -> tasks/TASK-0008-Test-Task.md

- [ ] TASK-0008-Test-Task - test task -> tasks/TASK-0008-Test-Task.md
`)
	writeRunLoopFile(t, root, "docs/tasks/TASK-0008-Test-Task.md", `# TASK-0008-Test-Task

## Status

State: Active

## Sub-Tasks

- [x] Done
- [ ] First open step
`)

	state, err := readRunLoopState(root)
	if err != nil {
		t.Fatalf("readRunLoopState: %v", err)
	}
	if state.nextStep != "First open step" {
		t.Fatalf("nextStep = %q", state.nextStep)
	}
}

func TestReadRunLoopStateRejectsDoneCurrent(t *testing.T) {
	root := t.TempDir()
	writeRunLoopFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0009-Test-Task -> tasks/TASK-0009-Test-Task.md

- [x] TASK-0009-Test-Task - test task -> tasks/done/TASK-0009-Test-Task.md
`)

	if _, err := readRunLoopState(root); err == nil {
		t.Fatal("expected error for done current task")
	}
}

func TestReadRunLoopStateIncludesPersistedRunloop(t *testing.T) {
	root := t.TempDir()
	writeRunLoopFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0010-Test-Task -> tasks/TASK-0010-Test-Task.md

- [ ] TASK-0010-Test-Task - test task -> tasks/TASK-0010-Test-Task.md
`)
	writeRunLoopFile(t, root, "docs/tasks/TASK-0010-Test-Task.md", `# TASK-0010-Test-Task

## Status

State: Active

## Sub-Tasks

- [~] Continue exact step
`)
	writeRunLoopFile(t, root, ".reconc/runloop/state.json", `{"enabled":true,"session_id":"ses_test"}`)

	state, err := readRunLoopState(root)
	if err != nil {
		t.Fatalf("readRunLoopState: %v", err)
	}
	if !state.modeEnabled {
		t.Fatal("expected persisted runloop to be enabled")
	}
	if state.modeSession != "ses_test" {
		t.Fatalf("modeSession = %q", state.modeSession)
	}
}

func TestReadRunLoopStateUsesActiveRunAndNoProgressNudges(t *testing.T) {
	root := t.TempDir()
	writeRunLoopFile(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0011-Test-Task -> tasks/TASK-0011-Test-Task.md

- [ ] TASK-0011-Test-Task - test task -> tasks/TASK-0011-Test-Task.md
`)
	writeRunLoopFile(t, root, "docs/tasks/TASK-0011-Test-Task.md", `# TASK-0011-Test-Task

## Status

State: Active

## Sub-Tasks

- [~] Continue exact step
`)
	writeRunLoopFile(t, root, ".reconc/runloop/state.json", `{"enabled":true,"session_id":"ses_old","active_run_id":"ses_active","no_progress_nudges":3}`)

	state, err := readRunLoopState(root)
	if err != nil {
		t.Fatalf("readRunLoopState: %v", err)
	}
	if state.modeActive != "ses_active" {
		t.Fatalf("modeActive = %q", state.modeActive)
	}
	if state.modeSession != "ses_active" {
		t.Fatalf("modeSession = %q", state.modeSession)
	}
	if state.modeNudges != 3 {
		t.Fatalf("modeNudges = %d", state.modeNudges)
	}
}

func writeRunLoopFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}
