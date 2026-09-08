package tasklifecycle

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReproduceBriefingRunStateQueueBlockedDisagreement(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Ready", state: StateQueued, subTasks: "- [ ] Start"},
		{id: "002", title: "Blocked", state: StateBlocked, subTasks: "- [~] Resume", blocker: "credential missing"},
	})
	board, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	runState, err := InspectRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	briefing := BuildBriefing(board)
	if runState.Disposition != RunClaim || runState.TaskID != "001" {
		t.Fatalf("run state = %+v, want claim of 001", runState)
	}
	if briefing.Remediation != "run `reconc task claim 001` for tasks/001-ready.md" {
		t.Fatalf("briefing remediation = %q, want exact selected task", briefing.Remediation)
	}
}

func TestRunStateAndBriefingShareBoardDecision(t *testing.T) {
	active := &Task{ID: "010", Title: "Active", Path: "tasks/010-active.md", State: StateActive, SubTasks: []SubTask{{State: StateActive, Text: "Continue"}}}
	dependency := &Task{ID: "011", Title: "Dependency", Path: "tasks/011-dependency.md", State: StateBlocked, Blocker: "credential missing"}
	waiting := &Task{ID: "012", Title: "Waiting", Path: "tasks/012-waiting.md", State: StateQueued, Dependencies: []string{"011"}}
	ready := &Task{ID: "013", Title: "Ready", Path: "tasks/013-ready.md", State: StateQueued}
	done := &Task{ID: "014", Title: "Done", Path: "tasks/done/014-done.md", State: StateDone}

	tests := []struct {
		name          string
		board         *Board
		want          RunState
		wantRemediate string
	}{
		{
			name:          "active wins over blocked and queue",
			board:         decisionBoard(active, []*Task{ready}, []*Task{dependency}),
			want:          RunState{Profile: ProfileSections, Disposition: RunContinue, TaskID: "010", TaskTitle: "Active", TaskPath: "tasks/010-active.md", SubTask: "Continue", OpenTasks: 3},
			wantRemediate: "continue the current Sub-Task; run `reconc task check-done` before promotion",
		},
		{
			name:          "queue claim is not hidden by blocked task",
			board:         decisionBoard(nil, []*Task{ready}, []*Task{dependency}),
			want:          RunState{Profile: ProfileSections, Disposition: RunClaim, TaskID: "013", TaskTitle: "Ready", TaskPath: "tasks/013-ready.md", OpenTasks: 2},
			wantRemediate: "run `reconc task claim 013` for tasks/013-ready.md",
		},
		{
			name:          "dependency chain skips waiting task",
			board:         decisionBoard(nil, []*Task{waiting, ready}, []*Task{dependency}),
			want:          RunState{Profile: ProfileSections, Disposition: RunClaim, TaskID: "013", TaskTitle: "Ready", TaskPath: "tasks/013-ready.md", OpenTasks: 3},
			wantRemediate: "run `reconc task claim 013` for tasks/013-ready.md",
		},
		{
			name:          "all queued tasks wait on dependency",
			board:         decisionBoard(nil, []*Task{waiting}, []*Task{dependency}),
			want:          RunState{Profile: ProfileSections, Disposition: RunBlocked, TaskID: "012", TaskTitle: "Waiting", TaskPath: "tasks/012-waiting.md", Blocker: runDependencyBlocker, OpenTasks: 2},
			wantRemediate: "resolve dependencies for TASK 012 at tasks/012-waiting.md, then run `reconc task claim 012`",
		},
		{
			name:          "resumable blocked task",
			board:         decisionBoard(nil, nil, []*Task{dependency}),
			want:          RunState{Profile: ProfileSections, Disposition: RunBlocked, TaskID: "011", TaskTitle: "Dependency", TaskPath: "tasks/011-dependency.md", Blocker: "credential missing", OpenTasks: 1},
			wantRemediate: "resolve the blocker for TASK 011 at tasks/011-dependency.md, then run `reconc task resume 011`",
		},
		{
			name:          "empty board completes",
			board:         decisionBoard(nil, nil, nil),
			want:          RunState{Profile: ProfileSections, Disposition: RunComplete},
			wantRemediate: "no open TASK remains",
		},
		{
			name:          "done-only board completes",
			board:         decisionBoardWithDone(nil, nil, nil, []*Task{done}),
			want:          RunState{Profile: ProfileSections, Disposition: RunComplete},
			wantRemediate: "no open TASK remains",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := fmt.Sprintf("%#v", test.board)
			state := RunStateFromBoard(test.board)
			if !reflect.DeepEqual(state, test.want) {
				t.Fatalf("run state = %#v, want %#v", state, test.want)
			}
			briefing := BuildBriefing(test.board)
			if briefing.Remediation != test.wantRemediate {
				t.Fatalf("briefing remediation = %q, want %q", briefing.Remediation, test.wantRemediate)
			}
			if got := fmt.Sprintf("%#v", test.board); got != before {
				t.Fatalf("board changed during pure decision: before=%s after=%s", before, got)
			}
		})
	}
}

func decisionBoard(active *Task, queue, blocked []*Task) *Board {
	return decisionBoardWithDone(active, queue, blocked, nil)
}

func decisionBoardWithDone(active *Task, queue, blocked, done []*Task) *Board {
	board := &Board{
		Profile: ProfileSections, Active: active, Queue: queue, Blocked: blocked, Done: done,
		tasksByID: map[string]*Task{}, tasksByName: map[string]*Task{}, doneIDs: map[string]bool{},
	}
	for _, task := range board.allTasks() {
		board.tasksByID[task.ID] = task
		board.tasksByName[task.Name] = task
	}
	for _, task := range done {
		board.doneIDs[task.ID] = true
		board.doneIDs[task.Name] = true
	}
	return board
}

func TestBriefingUsesExactLogbookTaskIdentifierAndPath(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: none\n\n- [ ] TASK-0002-Queued - Queued work -> tasks/TASK-0002-Queued.md\n"))
	writeFile(t, repo, "docs/tasks/TASK-0002-Queued.md", logbookDetail("TASK-0002-Queued", "Queued", "- [ ] Start"))
	board, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	state, err := InspectRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.TaskID != "0002" || state.TaskPath != "tasks/TASK-0002-Queued.md" {
		t.Fatalf("logbook selection = %+v", state)
	}
	want := "run `reconc task claim 0002` for tasks/TASK-0002-Queued.md"
	if got := BuildBriefing(board).Remediation; got != want {
		t.Fatalf("logbook remediation = %q, want %q", got, want)
	}
}
