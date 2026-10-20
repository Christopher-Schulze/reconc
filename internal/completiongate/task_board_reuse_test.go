package completiongate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/tasklifecycle"
)

// TASK completion is judged on the board the check inspected; a change after
// that inspection is left to the candidate checks around the whole gate.
func TestTaskChecksJudgeTheInspectedBoard(t *testing.T) {
	repo := t.TempDir()
	write := func(relative, body string) {
		t.Helper()
		path := filepath.Join(repo, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	detail := "# TASK 001: Ship proof\n\n## Why\n\nProve completion.\n\n## Acceptance\n\n- Evidence is current.\n\n## Sub-Tasks\n\n- [x] Prove it\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n"
	write(".reconc.yml", "task_lifecycle:\n  profile: sections-v1\n")
	write("docs/tasks.md", "# TASK Control Plane\n\n## Active\n\n- [~] 001 Ship proof -> tasks/001-ship-proof.md\n\n## Queue\n\n## Blocked\n\n## Done\n")
	write("docs/tasks/001-ship-proof.md", detail)
	previous := inspectTaskBoard
	t.Cleanup(func() { inspectTaskBoard = previous })
	inspections := 0
	inspectTaskBoard = func(root string) (*tasklifecycle.Board, error) {
		inspections++
		board, err := previous(root)
		write("docs/tasks/001-ship-proof.md", strings.Replace(detail, "- [x] Prove it", "- [ ] Prove it", 1))
		return board, err
	}
	report := &Report{}
	statuses := map[string]Status{}
	collectTaskChecks(report, agentsession.CompletionStateSnapshot{RepoRoot: repo}, func(id string, status Status, _, _ string) {
		statuses[id] = status
	})
	if inspections != 1 || report.TaskID != "001" || statuses["task/completion"] != StatusPass {
		t.Fatalf("TASK checks re-read the board: inspections=%d task=%q statuses=%v", inspections, report.TaskID, statuses)
	}
}
