package tasklifecycle

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func convertMarkdownToCRLF(t *testing.T, repo string) {
	t.Helper()
	err := filepath.Walk(filepath.Join(repo, "docs"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, bytes.ReplaceAll(body, []byte("\n"), []byte("\r\n")), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func requireOnlyCRLF(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bare := bytes.Count(body, []byte("\n")) - bytes.Count(body, []byte("\r\n")); bare != 0 {
		t.Fatalf("%s mixes %d bare LF line endings into a CRLF file", path, bare)
	}
}

func TestCRLFSectionsBoardInspectsAndMutatesLikeLF(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Queued work", state: StateQueued, subTasks: "- [ ] Build it"},
	})
	convertMarkdownToCRLF(t, repo)
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("CRLF board does not inspect: %v", err)
	}
	if board.Profile != ProfileSections || len(board.Queue) != 1 {
		t.Fatalf("CRLF board parsed differently: %#v", board)
	}
	if state, err := InspectRunState(repo); err != nil || state.Disposition == "" {
		t.Fatalf("CRLF run state = %#v, %v", state, err)
	}
	if _, err := Claim(repo, "001"); err != nil {
		t.Fatalf("CRLF board cannot be claimed: %v", err)
	}
	requireOnlyCRLF(t, filepath.Join(repo, "docs", "tasks.md"))
	details, err := filepath.Glob(filepath.Join(repo, "docs", "tasks", "001-*.md"))
	if err != nil || len(details) != 1 {
		t.Fatalf("detail = %v %v", details, err)
	}
	requireOnlyCRLF(t, details[0])
	board, err = Inspect(repo)
	if err != nil || board.Active == nil || board.Active.ID != "001" {
		t.Fatalf("claimed CRLF board = %#v, %v", board, err)
	}
}

func TestCRLFLogbookBoardInspects(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: TASK-0001-Active -> tasks/TASK-0001-Active.md\n\n- [ ] TASK-0001-Active - Active work -> tasks/TASK-0001-Active.md\n"))
	writeFile(t, repo, "docs/tasks/TASK-0001-Active.md", logbookDetail("TASK-0001-Active", "Active", "- [~] Work"))
	convertMarkdownToCRLF(t, repo)
	board, err := Inspect(repo)
	if err != nil || board.Active == nil || board.Active.Name != "TASK-0001-Active" {
		t.Fatalf("CRLF logbook = %#v, %v", board, err)
	}
}
