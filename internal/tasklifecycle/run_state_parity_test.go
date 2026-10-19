package tasklifecycle

import (
	"strings"
	"testing"
)

const parityActiveDetail = "# TASK 001: Current\n\n## Why\n\nRun.\n\n## Acceptance\n\n- Done.\n\n## Sub-Tasks\n\n- [~] Continue safely.\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n"

func writeParityBoard(t *testing.T, doneRows string, archives map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	overview := "# Tasks\n\n## Active\n\n- [~] 001 Current -> tasks/001-current.md\n\n## Queue\n\n## Blocked\n\n## Done\n" + doneRows
	writeRunStateFile(t, repo, ".reconc.yml", "task_lifecycle:\n  profile: sections-v1\n  done_visible: 10\nrules: []\n")
	writeRunStateFile(t, repo, "docs/tasks.md", overview)
	writeRunStateFile(t, repo, "docs/tasks/001-current.md", parityActiveDetail)
	for path, body := range archives {
		writeRunStateFile(t, repo, path, body)
	}
	return repo
}

// The Stop fast path must never continue a board the full parser rejects.
func TestSectionsRunStateFastPathAgreesWithTheFullParser(t *testing.T) {
	archive := "# TASK 000: Old\n"
	cases := map[string]string{
		"trailing space after target": writeParityBoardOverview(t, "- [~] 001 Current -> tasks/001-current.md \n"),
		"ascending done rows": writeParityBoard(t, "- [x] 002 Older -> tasks/done/002-older.md\n- [x] 003 Newer -> tasks/done/003-newer.md\n",
			map[string]string{"docs/tasks/done/002-older.md": archive, "docs/tasks/done/003-newer.md": archive}),
		"missing archived detail": writeParityBoard(t, "- [x] 002 Gone -> tasks/done/002-gone.md\n", nil),
	}
	for name, repo := range cases {
		t.Run(name, func(t *testing.T) {
			if _, fast, err := inspectActiveSectionsRunState(repo); err == nil && fast {
				t.Fatal("fast path accepted a board the full parser rejects")
			}
			if _, err := Inspect(repo); err == nil {
				t.Fatal("full parser accepted the fixture; it no longer proves parity")
			}
		})
	}
	valid := writeParityBoard(t, "- [x] 003 Newer -> tasks/done/003-newer.md\n- [x] 002 Older -> tasks/done/002-older.md\n",
		map[string]string{"docs/tasks/done/002-older.md": archive, "docs/tasks/done/003-newer.md": archive})
	if state, fast, err := inspectActiveSectionsRunState(valid); err != nil || !fast || state.Disposition != RunContinue {
		t.Fatalf("valid board left the fast path: fast=%t state=%+v err=%v", fast, state, err)
	}
}

func writeParityBoardOverview(t *testing.T, activeRow string) string {
	t.Helper()
	repo := writeParityBoard(t, "", nil)
	overview := "# Tasks\n\n## Active\n\n" + activeRow + "\n## Queue\n\n## Blocked\n\n## Done\n"
	writeRunStateFile(t, repo, "docs/tasks.md", strings.ReplaceAll(overview, "\n\n\n", "\n\n"))
	return repo
}
