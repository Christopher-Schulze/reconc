package tasklifecycle

import (
	"reflect"
	"strings"
	"testing"
)

func TestDependencyClassificationAndSuccessorSelectionRemainExplicit(t *testing.T) {
	done := &Task{ID: "001", Name: "TASK-0001-done", State: StateDone}
	active := &Task{ID: "002", Name: "TASK-0002-active", State: StateActive}
	blocked := &Task{ID: "004", Name: "TASK-0004-blocked", State: StateBlocked}
	waiting := &Task{
		ID: "003", Name: "TASK-0003-waiting", State: StateQueued,
		Dependencies: []string{"001", "TASK-0002-active", "999"},
	}
	ready := &Task{
		ID: "005", Name: "TASK-0005-ready", State: StateQueued,
		Dependencies: []string{"TASK-0001-done"},
	}
	board := &Board{
		Active:  active,
		Queue:   []*Task{waiting, ready},
		Blocked: []*Task{blocked},
		Done:    []*Task{done},
		doneIDs: map[string]bool{"001": true},
		tasksByID: map[string]*Task{
			"001": done, "002": active, "003": waiting, "004": blocked, "005": ready,
		},
		tasksByName: map[string]*Task{
			done.Name: done, active.Name: active, waiting.Name: waiting, blocked.Name: blocked, ready.Name: ready,
		},
	}

	unfinished, unknown := splitDependencies(board, waiting)
	if !reflect.DeepEqual(unfinished, []string{"TASK-0002-active"}) {
		t.Fatalf("unfinished = %#v", unfinished)
	}
	if !reflect.DeepEqual(unknown, []string{"999"}) {
		t.Fatalf("unknown = %#v", unknown)
	}
	if dependenciesDone(board, waiting) {
		t.Fatal("task with unfinished and unknown dependencies must not be ready")
	}
	if !dependenciesDone(board, ready) {
		t.Fatal("task whose dependency is done must be ready")
	}
	wantReason := "unfinished dependencies: TASK-0002-active; unknown dependency ids (no TASK on the board): 999"
	if got := dependencyBlockReason(board, waiting); got != wantReason {
		t.Fatalf("dependencyBlockReason() = %q, want %q", got, wantReason)
	}

	next, err := selectNext(board, "", false)
	if err != nil || next != ready {
		t.Fatalf("automatic selectNext() = (%+v, %v), want ready task", next, err)
	}
	if _, err := selectNext(board, "missing", false); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("missing requested task error = %v", err)
	}
	if _, err := selectNext(board, done.ID, false); err == nil || !strings.Contains(err.Error(), "not an eligible queued TASK") {
		t.Fatalf("done requested task error = %v", err)
	}
	if _, err := selectNext(board, waiting.ID, false); err == nil || !strings.Contains(err.Error(), "unfinished dependencies") {
		t.Fatalf("waiting requested task error = %v", err)
	}
	next, err = selectNext(board, blocked.ID, true)
	if err != nil || next != blocked {
		t.Fatalf("blocked selectNext(allow=true) = (%+v, %v), want blocked task", next, err)
	}

	board.Queue = []*Task{waiting}
	next, err = selectNext(board, "", false)
	if err != nil || next != nil {
		t.Fatalf("no eligible successor = (%+v, %v), want nil, nil", next, err)
	}
	if blockedSuffix(false) != "" || blockedSuffix(true) != " or blocked" {
		t.Fatalf("blockedSuffix(false/true) = %q/%q", blockedSuffix(false), blockedSuffix(true))
	}
}

func TestTaskMutationDiagnosticHelpersPinEveryStateAndBoundary(t *testing.T) {
	task := &Task{
		ID: "007", Name: "TASK-0007-child", Title: "Child", State: StateBlocked,
		Path: "docs/tasks/TASK-0007-child.md",
		rawDetail: []byte(`# TASK-0007-child

## Why

Derived from TASK-0006-parent.

## Acceptance

Done.
`),
	}
	parent := &Task{ID: "006", Name: "TASK-0006-parent"}
	board := &Board{
		tasksByID:   map[string]*Task{task.ID: task},
		tasksByName: map[string]*Task{task.Name: task},
	}

	if got, err := requireTask(board, task.ID, StateQueued, StateBlocked); err != nil || got != task {
		t.Fatalf("requireTask accepted states = (%+v, %v)", got, err)
	}
	if _, err := requireTask(board, task.ID, StateQueued, StateActive); err == nil ||
		!strings.Contains(err.Error(), "expected queued or active") {
		t.Fatalf("requireTask wrong-state error = %v", err)
	}
	if _, err := requireTask(board, "missing", StateQueued); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("requireTask missing error = %v", err)
	}
	if got := joinStates([]State{StateQueued, StatePaused, StateDone}); got != "queued or paused or done" {
		t.Fatalf("joinStates() = %q", got)
	}
	if !detailReferencesParent(task, parent) {
		t.Fatal("TASK name in Why must establish parent relationship")
	}
	if detailReferencesParent(task, &Task{ID: "999", Name: "TASK-0999-other"}) {
		t.Fatal("unmentioned task must not establish parent relationship")
	}

	normalized, err := normalizeBlocker("  waiting   for\nCI  ")
	if err != nil || normalized != "waiting for CI" {
		t.Fatalf("normalizeBlocker() = (%q, %v)", normalized, err)
	}
	if _, err := normalizeBlocker(" \n "); err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("empty blocker error = %v", err)
	}
	if _, err := normalizeBlocker(strings.Repeat("x", maxBlockerBytes+1)); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized blocker error = %v", err)
	}

	for state, want := range map[State]string{
		StateActive: "Active", StateQueued: "Queued", StateBlocked: "Blocked",
		StatePaused: "Paused", StateDone: "Done",
	} {
		if got := logbookState(state); got != want {
			t.Fatalf("logbookState(%q) = %q, want %q", state, got, want)
		}
	}
	mutation := result("resume", task, StateBlocked, StateActive, "008")
	if mutation.Action != "resume" || mutation.TaskID != task.ID || mutation.TaskPath != task.Path ||
		mutation.PreviousState != StateBlocked || mutation.State != StateActive || mutation.NextTaskID != "008" {
		t.Fatalf("result() = %+v", mutation)
	}
	if taskID(nil) != "" || taskID(task) != task.ID {
		t.Fatalf("taskID(nil/task) = %q/%q", taskID(nil), taskID(task))
	}
}
