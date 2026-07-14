package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTaskStatusJSONIsCompactAndTyped(t *testing.T) {
	repo := makeTaskCLIRepo(t, "- [~] 001 Active work -> tasks/001-active-work.md", "- [~] Build it")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"task", "status", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("task status: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode status: %v\n%s", err, stdout.String())
	}
	current, ok := payload["current"].(map[string]any)
	if !ok || current["id"] != "001" || current["current_sub_task"] != "Build it" {
		t.Fatalf("unexpected current TASK payload: %#v", payload)
	}
	if stdout.Len() > 1200 {
		t.Fatalf("status payload exceeded compact contract: %d bytes", stdout.Len())
	}
}

func TestRunTaskClaimMutatesThroughCLI(t *testing.T) {
	repo := makeTaskCLIRepo(t, "- [ ] 001 Active work -> tasks/001-active-work.md", "- [ ] Build it")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"task", "claim", "001", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("task claim: %v", err)
	}
	if !strings.Contains(stdout.String(), `"state": "active"`) {
		t.Fatalf("claim JSON missing active state: %s", stdout.String())
	}
	overview, _ := os.ReadFile(filepath.Join(repo, "docs/tasks.md"))
	detail, _ := os.ReadFile(filepath.Join(repo, "docs/tasks/001-active-work.md"))
	if !strings.Contains(string(overview), "- [~] 001") || !strings.Contains(string(detail), "- [~] Build it") {
		t.Fatalf("claim did not update both files atomically\noverview=%s\ndetail=%s", overview, detail)
	}
}

func TestRunTaskValidateReturnsStableIssueJSON(t *testing.T) {
	repo := makeTaskCLIRepo(t, "- [~] 001 Active work -> tasks/001-active-work.md", "- [?] broken")
	var stdout, stderr bytes.Buffer
	err := Run([]string{"task", "validate", repo, "--json"}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected blocking validation error, got %v", err)
	}
	if !strings.Contains(stdout.String(), `"id": "task/detail/invalid-subtask"`) {
		t.Fatalf("stable issue id missing: %s", stdout.String())
	}
}

func TestRunTaskHelpListsEveryMutation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"task", "--help"}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"check-done", "claim", "block", "resume", "split", "promote", "archive", "recover"} {
		if !strings.Contains(stdout.String(), "task "+command) {
			t.Fatalf("task help missing %s: %s", command, stdout.String())
		}
	}
}

func makeTaskCLIRepo(t *testing.T, row, subTask string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "docs/tasks/done"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := "task_lifecycle:\n  profile: sections-v1\n"
	overview := "# TASK Control Plane\n\n## Active\n\n"
	if strings.Contains(row, "[~]") {
		overview += row
	}
	overview += "\n\n## Queue\n\n"
	if strings.Contains(row, "[ ]") {
		overview += row
	}
	overview += "\n\n## Blocked\n\n## Done\n"
	detail := "# TASK 001: Active work\n\n## Why\n\nReason.\n\n## Acceptance\n\n- Result.\n\n## Sub-Tasks\n\n" + subTask + "\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n"
	for path, body := range map[string]string{
		".reconc.yml":                   config,
		"docs/tasks.md":                 overview,
		"docs/tasks/001-active-work.md": detail,
	} {
		if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(path)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}
