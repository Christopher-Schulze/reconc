package tasklifecycle

import (
	"reflect"
	"strings"
	"testing"
)

func TestDirtyCompletionPathsMatchesOnlyTaskControlPlane(t *testing.T) {
	cfg := Config{
		OverviewPath: "docs/tasks.md",
		DetailDir:    "docs/tasks",
	}
	dirty := []string{
		"README.md",
		"docs/tasks.md",
		"docs/tasks",
		"docs/tasks/",
		"docs/tasks/092-coverage.md",
		"docs/tasks/done/091-bootstrap.md",
		"docs/",
		"internal/runtime/events.go",
	}
	want := []string{
		"docs/tasks.md",
		"docs/tasks",
		"docs/tasks/",
		"docs/tasks/092-coverage.md",
		"docs/tasks/done/091-bootstrap.md",
		"docs/",
	}
	if got := DirtyCompletionPaths(cfg, dirty); !reflect.DeepEqual(got, want) {
		t.Fatalf("DirtyCompletionPaths = %v, want %v", got, want)
	}
}

func TestCheckCompletionResolvesDefaultAndExplicitTask(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Active", state: StateActive, subTasks: "- [x] Complete"},
		{id: "002", title: "Queued", state: StateQueued, subTasks: "- [ ] Waiting"},
	})

	id, issues, err := CheckCompletion(repo, "")
	if err != nil {
		t.Fatalf("CheckCompletion(active): %v", err)
	}
	if id != "001" || len(issues) != 0 {
		t.Fatalf("active completion = id %q, issues %+v", id, issues)
	}

	id, issues, err = CheckCompletion(repo, "002")
	if err != nil {
		t.Fatalf("CheckCompletion(explicit): %v", err)
	}
	if id != "002" || len(issues) == 0 {
		t.Fatalf("queued completion = id %q, issues %+v", id, issues)
	}
}

func TestCheckCompletionReportsMissingTaskAndInvalidBoard(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Active", state: StateActive, subTasks: "- [x] Complete"},
	})
	if _, _, err := CheckCompletion(repo, "999"); err == nil || !strings.Contains(err.Error(), `TASK "999" was not found`) {
		t.Fatalf("expected missing-task error, got %v", err)
	}

	invalid := t.TempDir()
	if _, _, err := CheckCompletion(invalid, ""); err == nil {
		t.Fatal("invalid TASK board was accepted")
	}
}
