package tasklifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every board the parser accepts must stay mutable.
func TestMutatorsAcceptEveryStatusShapeTheParserAccepts(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: TASK-0001-Active -> tasks/TASK-0001-Active.md\n\n- [ ] TASK-0001-Active - Active work -> tasks/TASK-0001-Active.md\n- [ ] TASK-0002-Next - Next work -> tasks/TASK-0002-Next.md\n"))
	active := strings.Replace(string(logbookDetail("TASK-0001-Active", "Active", "- [~] Work")), "State: Active", "- State: active", 1)
	active = strings.Replace(active, "## Status", "## Status ", 1)
	writeFile(t, repo, "docs/tasks/TASK-0001-Active.md", []byte(active))
	writeFile(t, repo, "docs/tasks/TASK-0002-Next.md", logbookDetail("TASK-0002-Next", "Queued", "- [ ] Continue"))
	if _, err := Inspect(repo); err != nil {
		t.Fatalf("fixture no longer parses: %v", err)
	}
	if _, err := Block(repo, "waiting for review", "0002"); err != nil {
		t.Fatalf("a parser-accepted Status shape could not be mutated: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, "docs", "tasks", "TASK-0001-Active.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "- State: Blocked") {
		t.Fatalf("mutated State lost its bullet or was not rewritten:\n%s", body)
	}
}
