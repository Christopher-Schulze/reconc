package tasklifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSectionsRunStateFastPathResolvesOverviewRelativeTargets(t *testing.T) {
	repo := writeSectionsRunStateFixture(t,
		"",
		"# TASK 001: Current\n\n## Why\n\nRun.\n\n## Acceptance\n\n- Done.\n\n## Sub-Tasks\n\n- [~] Continue safely.\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n",
	)
	state, fast, err := inspectActiveSectionsRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !fast || state.Disposition != RunContinue || state.TaskPath != "tasks/001-current.md" || state.SubTask != "Continue safely." {
		t.Fatalf("unexpected fast run state: fast=%t state=%+v", fast, state)
	}
}

func TestSectionsRunStateFastPathFallsBackOnInvalidOpenDetail(t *testing.T) {
	repo := writeSectionsRunStateFixture(t,
		"- [ ] 002 Next -> tasks/002-next.md",
		"# TASK 001: Current\n\n## Why\n\nRun.\n\n## Acceptance\n\n- Done.\n\n## Sub-Tasks\n\n- [~] Continue safely.\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n",
	)
	writeRunStateFile(t, repo, "docs/tasks/002-next.md", "# TASK 002: Next\n\n## Why\n\nWait.\n")
	if _, fast, err := inspectActiveSectionsRunState(repo); err != nil || fast {
		t.Fatalf("invalid queued detail must fall back to full validation: fast=%t err=%v", fast, err)
	}
	_, err := InspectRunStateResolved(repo)
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("invalid queued detail was not rejected: %v", err)
	}
}

func TestSectionsRunStateFastPathFallsBackOnDuplicateOverviewIdentity(t *testing.T) {
	repo := writeSectionsRunStateFixture(t,
		"- [ ] 001 Duplicate -> tasks/002-next.md",
		"# TASK 001: Current\n\n## Why\n\nRun.\n\n## Acceptance\n\n- Done.\n\n## Sub-Tasks\n\n- [~] Continue safely.\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n",
	)
	writeRunStateFile(t, repo, "docs/tasks/002-next.md", "# TASK 001: Duplicate\n\n## Why\n\nWait.\n\n## Acceptance\n\n- Ready.\n\n## Sub-Tasks\n\n- [ ] Wait.\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n")
	if _, fast, err := inspectActiveSectionsRunState(repo); err != nil || fast {
		t.Fatalf("duplicate overview identity must fall back: fast=%t err=%v", fast, err)
	}
	if _, err := InspectRunStateResolved(repo); err == nil {
		t.Fatal("duplicate overview identity was not rejected")
	}
}

func TestSectionsRunStateFastPathRejectsEveryDuplicateSectionCount(t *testing.T) {
	tests := []struct {
		name       string
		duplicates int
		bodyBytes  int
	}{
		{name: "two", duplicates: 2},
		{name: "255", duplicates: 255},
		{name: "256", duplicates: 256},
		{name: "257", duplicates: 257},
		{name: "maximum-size overview", duplicates: 2, bodyBytes: maxTaskControlBytes},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := writeDuplicateSectionsRunStateFixture(t, test.duplicates, test.bodyBytes)
			if _, fast, err := inspectActiveSectionsRunState(repo); err != nil || fast {
				t.Fatalf("duplicate sections reached fast state: fast=%t err=%v", fast, err)
			}
			_, err := inspectResolved(repo)
			var validation *ValidationError
			if !errors.As(err, &validation) || !hasTaskIssue(validation.Issues, "task/overview/duplicate-section") {
				t.Fatalf("full parser did not reject duplicate sections: %v", err)
			}
		})
	}
}

func writeDuplicateSectionsRunStateFixture(t *testing.T, activeHeadings, bodyBytes int) string {
	t.Helper()
	repo := t.TempDir()
	overview := "# Tasks\n\n" + strings.Repeat("## Active\n", activeHeadings) +
		"\n- [~] 001 Current -> tasks/001-current.md\n\n## Queue\n\n## Blocked\n\n## Done\n"
	if bodyBytes > 0 {
		if len(overview) >= bodyBytes {
			t.Fatalf("overview fixture is %d bytes, target is %d", len(overview), bodyBytes)
		}
		overview += strings.Repeat("x", bodyBytes-len(overview)-1) + "\n"
	}
	if bodyBytes > 0 && len(overview) != bodyBytes {
		t.Fatalf("overview fixture is %d bytes, want %d", len(overview), bodyBytes)
	}
	writeRunStateFile(t, repo, ".reconc.yml", "task_lifecycle:\n  profile: sections-v1\nrules: []\n")
	writeRunStateFile(t, repo, "docs/tasks.md", overview)
	writeRunStateFile(t, repo, "docs/tasks/001-current.md", "# TASK 001: Current\n\n## Why\n\nRun.\n\n## Acceptance\n\n- Done.\n\n## Sub-Tasks\n\n- [~] Continue safely.\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n")
	return repo
}

func hasTaskIssue(issues []Issue, id string) bool {
	for _, issue := range issues {
		if issue.ID == id {
			return true
		}
	}
	return false
}

func writeSectionsRunStateFixture(t *testing.T, queueRow, activeDetail string) string {
	t.Helper()
	repo := t.TempDir()
	overview := "# Tasks\n\n## Active\n\n- [~] 001 Current -> tasks/001-current.md\n\n## Queue\n\n" + queueRow + "\n\n## Blocked\n\n## Done\n"
	writeRunStateFile(t, repo, ".reconc.yml", "task_lifecycle:\n  profile: sections-v1\nrules: []\n")
	writeRunStateFile(t, repo, "docs/tasks.md", overview)
	writeRunStateFile(t, repo, "docs/tasks/001-current.md", activeDetail)
	return repo
}

func writeRunStateFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
