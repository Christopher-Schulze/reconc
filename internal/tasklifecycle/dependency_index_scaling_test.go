package tasklifecycle

import (
	"fmt"
	"runtime"
	"testing"
)

// dependencyBoard returns a board whose queued TASKs each depend on the
// previous one, so per-TASK dependency work scales with the queue length.
func dependencyBoard(queued int) *Board {
	board := &Board{Config: Config{DoneVisible: 1 << 20}, doneIDs: map[string]bool{}}
	for index := 0; index < queued; index++ {
		task := &Task{ID: fmt.Sprintf("%03d", index+1), Name: fmt.Sprintf("Task-%03d", index+1), State: StateQueued}
		if index > 0 {
			task.Dependencies = []string{board.Queue[index-1].ID}
		}
		board.Queue = append(board.Queue, task)
	}
	return board
}

// validationBytes reports the bytes validateInvariants allocates for one
// prepared board.
func validationBytes(board *Board) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	board.validateInvariants()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestDependencyValidationIndexesTheBoardOnce(t *testing.T) {
	small := validationBytes(dependencyBoard(50))
	large := validationBytes(dependencyBoard(400))
	if large > 20*small {
		t.Fatalf("validation allocated %d bytes for 50 TASKs and %d for 400; dependency indexing must stay linear", small, large)
	}
	if issues := dependencyBoard(400).validateInvariants(); len(issues) != 0 {
		t.Fatalf("known dependencies must not raise issues: %+v", issues)
	}
	board := dependencyBoard(3)
	board.Queue[2].Dependencies = []string{"999"}
	issues := board.validateInvariants()
	if len(issues) != 1 || issues[0].ID != "task/detail/unknown-dependency" {
		t.Fatalf("unknown dependency must still be reported: %+v", issues)
	}
}
