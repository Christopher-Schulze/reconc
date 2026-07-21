package tasklifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSectionsTaskIsQueuedAndPreservesExistingRows(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{{id: "001", title: "Current", state: StateActive, subTasks: "- [~] Work"}})
	before, err := os.ReadFile(filepath.Join(repo, "docs/tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := Create(repo, "Ship polished CLI", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.TaskID != "002" || created.State != StateQueued || created.TaskPath != "docs/tasks/002-ship-polished-cli.md" {
		t.Fatalf("unexpected create result: %#v", created)
	}
	board, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(board.Queue) != 1 || board.Queue[0].ID != "002" || board.Queue[0].Title != "Ship polished CLI" {
		t.Fatalf("created TASK is not queued: %#v", board.Queue)
	}
	after, _ := os.ReadFile(filepath.Join(repo, "docs/tasks.md"))
	for _, line := range strings.Split(string(before), "\n") {
		if strings.HasPrefix(line, "- [") && !strings.Contains(string(after), line) {
			t.Fatalf("existing overview row changed: %q\n%s", line, after)
		}
	}
}

func TestCreateLogbookTaskUsesExactGrammar(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: none\n\n- [ ] TASK-0002-existing - Existing -> tasks/TASK-0002-existing.md\n"))
	writeFile(t, repo, "docs/tasks/TASK-0002-existing.md", logbookDetail("TASK-0002-existing", "Queued", "- [ ] Start"))
	created, err := Create(repo, "Add Windows proof", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.TaskID != "0003" || created.TaskPath != "docs/tasks/TASK-0003-add-windows-proof.md" {
		t.Fatalf("unexpected result: %#v", created)
	}
	board, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(board.Queue) != 2 || board.Queue[1].Name != "TASK-0003-add-windows-proof" {
		t.Fatalf("logbook TASK missing: %#v", board.Queue)
	}
}

func TestCreateRejectsCollisionAndAmbiguousProfileWithoutMutation(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{{id: "001", title: "Existing", state: StateQueued, subTasks: "- [ ] Work"}})
	before, _ := os.ReadFile(filepath.Join(repo, "docs/tasks.md"))
	if _, err := Create(repo, "Collision", "001"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected collision refusal, got %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(repo, "docs/tasks.md"))
	if string(after) != string(before) {
		t.Fatal("collision changed the overview")
	}

	ambiguous := t.TempDir()
	writeFile(t, ambiguous, "docs/tasks.md", []byte("# Tasks\n\nCurrent: none\n\n## Active\n\n## Queue\n\n## Blocked\n\n## Done\n"))
	if _, err := Create(ambiguous, "Must refuse", ""); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous profile refusal, got %v", err)
	}
}

func TestCreateRejectsUnlinkedHistoryIDCollision(t *testing.T) {
	repo := sectionedRepo(t, "", nil)
	writeFile(t, repo, "docs/tasks/done/007-old-title.md", []byte("# archived but unlinked\n"))
	before, err := os.ReadFile(filepath.Join(repo, "docs/tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(repo, "New title", "007"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unlinked historical ID collision was accepted: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(repo, "docs/tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("collision refusal changed the overview")
	}
}

func TestBlockWithoutNextLeavesQueueUntouched(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Active", state: StateActive, subTasks: "- [~] Work"},
		{id: "002", title: "Queued", state: StateQueued, subTasks: "- [ ] Later"},
	})
	result, err := BlockWithoutNext(repo, "operator decision")
	if err != nil {
		t.Fatalf("BlockWithoutNext: %v", err)
	}
	if result.NextTaskID != "" {
		t.Fatalf("unexpected successor: %#v", result)
	}
	board, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if board.Active != nil || len(board.Queue) != 1 || board.Queue[0].ID != "002" || len(board.Blocked) != 1 {
		t.Fatalf("--no-next mutated the wrong TASKs: active=%#v queue=%#v blocked=%#v", board.Active, board.Queue, board.Blocked)
	}
}

func TestRecoverRemovesOnlyTransactionCreatedFile(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks.md", []byte("before\n"))
	files := []fileMutation{
		{Path: "docs/tasks.md", After: []byte("after\n")},
		{Path: "docs/tasks/002-created.md", After: []byte("created\n"), Create: true},
	}
	journal, err := buildTransaction(repo, "new", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := publishTransaction(repo, journal, files, nil); err != nil {
		t.Fatal(err)
	}
	if err := rollbackTransaction(repo, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "docs/tasks/002-created.md")); !os.IsNotExist(err) {
		t.Fatalf("created file survived rollback: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(repo, "docs/tasks.md"))
	if string(body) != "before\n" {
		t.Fatalf("overview was not restored: %q", body)
	}
}
