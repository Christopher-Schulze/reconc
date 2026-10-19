package tasklifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspectRetriesAReadThatRacedAConcurrentWrite(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{{id: "001", title: "Current", state: StateActive, subTasks: "- [~] Work"}})
	detail := filepath.Join(repo, "docs", "tasks", "001-current.md")
	probes := 0
	previous := concurrentReadProbe
	t.Cleanup(func() { concurrentReadProbe = previous })
	concurrentReadProbe = func() {
		probes++
		if probes == 1 {
			later := time.Now().Add(2 * time.Second)
			if err := os.Chtimes(detail, later, later); err != nil {
				t.Error(err)
			}
		}
	}
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("a read that raced one concurrent write failed: %v", err)
	}
	if board.Active == nil || probes < 2 {
		t.Fatalf("read was not retried: probes=%d board=%#v", probes, board)
	}
	concurrentReadProbe = func() {
		later := time.Now().Add(time.Duration(probes+3) * time.Second)
		probes++
		_ = os.Chtimes(detail, later, later)
	}
	_, err = Inspect(repo)
	var validation *ValidationError
	if !errors.As(err, &validation) || !onlyConcurrentReadMutation(err) {
		t.Fatalf("a board that never stops changing was accepted: %v", err)
	}
}

func TestTaskPathGuardProbesStayLinearInBoardSize(t *testing.T) {
	tasks := []testTask{{id: "001", title: "Current", state: StateActive, subTasks: "- [~] Work"}}
	for index := 2; index <= 301; index++ {
		tasks = append(tasks, testTask{id: fmt.Sprintf("%03d", index), title: fmt.Sprintf("Queued %d", index), state: StateQueued, subTasks: "- [ ] Wait"})
	}
	repo := sectionedRepo(t, "", tasks)
	probes := 0
	previous := lstatTaskPathComponent
	t.Cleanup(func() { lstatTaskPathComponent = previous })
	lstatTaskPathComponent = func(path string) (os.FileInfo, error) {
		probes++
		return previous(path)
	}
	if _, err := Inspect(repo); err != nil {
		t.Fatal(err)
	}
	if limit := 12 * len(tasks); probes > limit {
		t.Fatalf("path guard made %d Lstat probes for %d TASKs, want at most %d", probes, len(tasks), limit)
	}
}

func TestLogbookRowTargetsShareDirectoryProbes(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	var overview strings.Builder
	overview.WriteString("# Tasks\n\nCurrent: TASK-0001-Active -> tasks/TASK-0001-Active.md\n\n- [ ] TASK-0001-Active - Active work -> tasks/TASK-0001-Active.md\n")
	writeFile(t, repo, "docs/tasks/TASK-0001-Active.md", logbookDetail("TASK-0001-Active", "Active", "- [~] Work"))
	const queued = 300
	for index := 2; index < 2+queued; index++ {
		name := fmt.Sprintf("TASK-%04d-Queued", index)
		fmt.Fprintf(&overview, "- [ ] %s - Queued work -> tasks/%s.md\n", name, name)
		writeFile(t, repo, "docs/tasks/"+name+".md", logbookDetail(name, "Queued", "- [ ] Wait"))
	}
	writeFile(t, repo, "docs/tasks.md", []byte(overview.String()))
	probes := 0
	previous := lstatTaskPathComponent
	t.Cleanup(func() { lstatTaskPathComponent = previous })
	lstatTaskPathComponent = func(path string) (os.FileInfo, error) {
		probes++
		return previous(path)
	}
	board, err := Inspect(repo)
	if err != nil || board.Active == nil || len(board.Queue) != queued {
		t.Fatalf("inspect err=%v queued=%d", err, queued)
	}
	if limit := 3*queued + 40; probes > limit {
		t.Fatalf("path guard made %d Lstat probes for %d open rows, want at most %d", probes, queued+1, limit)
	}
}
