package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/tasklifecycle"
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
	for _, command := range []string{"check-done", "new", "claim", "block", "resume", "split", "promote", "archive", "recover"} {
		if !strings.Contains(stdout.String(), "task "+command) {
			t.Fatalf("task help missing %s: %s", command, stdout.String())
		}
	}
}

func TestRunTaskNewAndBlockNoNext(t *testing.T) {
	repo := makeTaskCLIRepo(t, "- [~] 001 Active work -> tasks/001-active-work.md", "- [~] Build it")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"task", "new", repo, "--title", "Second task", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("task new: %v", err)
	}
	if !strings.Contains(stdout.String(), `"task_id": "002"`) || !strings.Contains(stdout.String(), `"state": "queued"`) {
		t.Fatalf("task new JSON mismatch: %s", stdout.String())
	}
	stdout.Reset()
	if err := Run([]string{"task", "block", repo, "--reason", "pause", "--no-next"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("task block --no-next: %v", err)
	}
	if strings.Contains(stdout.String(), "active=002") {
		t.Fatalf("--no-next activated queued work: %s", stdout.String())
	}
	board, err := tasklifecycle.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if board.Active != nil || len(board.Queue) != 1 {
		t.Fatalf("unexpected board after --no-next: %#v", board)
	}
}

func TestRunTaskBlockRejectsConflictingNextFlags(t *testing.T) {
	repo := makeTaskCLIRepo(t, "- [~] 001 Active work -> tasks/001-active-work.md", "- [~] Build it")
	var stdout bytes.Buffer
	err := runTaskBlock([]string{repo, "--reason", "pause", "--next", "002", "--no-next"}, &stdout)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected conflicting flag error, got %v", err)
	}
}

func TestWriteTaskFailureClassifiesAndBoundsJSON(t *testing.T) {
	validation := fmt.Errorf("wrapped lifecycle failure: %w", &tasklifecycle.ValidationError{Issues: []tasklifecycle.Issue{{
		ID: "task/test", Path: "docs/tasks.md", Line: 3, Message: "invalid state", Remediation: "repair state",
	}}})
	tests := []struct {
		name      string
		err       error
		wantCode  int
		wantClass string
		wantIssue string
		wantError string
	}{
		{name: "wrapped validation", err: validation, wantCode: 2, wantClass: "validation", wantIssue: "task/test"},
		{name: "permission read", err: &os.PathError{Op: "open", Path: "docs/tasks.md", Err: fs.ErrPermission}, wantCode: 1, wantClass: "operational", wantError: "permission denied"},
		{name: "malformed state", err: errors.New("decode TASK transaction: malformed JSON at byte 7"), wantCode: 1, wantClass: "operational", wantError: "malformed JSON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := writeTaskFailure("status", test.err, true, &output)
			if ExitCode(err) != test.wantCode {
				t.Fatalf("failure exit code = %d, want %d: %v", ExitCode(err), test.wantCode, err)
			}
			var envelope taskFailureEnvelope
			decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
			if err := decoder.Decode(&envelope); err != nil {
				t.Fatalf("decode failure envelope: %v: %s", err, output.String())
			}
			var trailing json.RawMessage
			if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
				t.Fatalf("failure output contains more than one envelope: %v", err)
			}
			if envelope.Valid || envelope.FailureClass != test.wantClass || len(output.Bytes()) > taskFailureJSONMaxBytes {
				t.Fatalf("failure envelope = %+v, bytes=%d", envelope, output.Len())
			}
			if test.wantIssue != "" && (len(envelope.Issues) != 1 || envelope.Issues[0].ID != test.wantIssue) {
				t.Fatalf("validation issues = %#v", envelope.Issues)
			}
			if test.wantError != "" && !strings.Contains(envelope.Error, test.wantError) {
				t.Fatalf("operational error = %q, want %q", envelope.Error, test.wantError)
			}
		})
	}

	hugeValidation := &tasklifecycle.ValidationError{Issues: []tasklifecycle.Issue{{
		ID: strings.Repeat("i", taskFailureJSONMaxBytes), Path: strings.Repeat("p", taskFailureJSONMaxBytes),
		Message: strings.Repeat("m", taskFailureJSONMaxBytes), Remediation: strings.Repeat("r", taskFailureJSONMaxBytes),
	}}}
	var bounded bytes.Buffer
	if err := writeTaskFailure("validate", hugeValidation, true, &bounded); ExitCode(err) != 2 {
		t.Fatalf("bounded validation exit = %d: %v", ExitCode(err), err)
	}
	if bounded.Len() > taskFailureJSONMaxBytes || !json.Valid(bounded.Bytes()) {
		t.Fatalf("bounded validation JSON bytes=%d valid=%t", bounded.Len(), json.Valid(bounded.Bytes()))
	}
}

func TestWriteTaskFailureCoversEveryTaskSubcommandAndOutputFailure(t *testing.T) {
	validation := &tasklifecycle.ValidationError{Issues: []tasklifecycle.Issue{{ID: "task/test", Message: "invalid"}}}
	for _, subcommand := range []string{"status", "validate", "check-done", "new", "claim", "block", "resume", "split", "promote", "archive", "recover"} {
		t.Run(subcommand, func(t *testing.T) {
			var output bytes.Buffer
			err := writeTaskFailure(subcommand, validation, true, &output)
			if ExitCode(err) != 2 || !strings.Contains(err.Error(), "reconc task "+subcommand+":") {
				t.Fatalf("%s failure = %v", subcommand, err)
			}
			var envelope taskFailureEnvelope
			if decodeErr := json.Unmarshal(output.Bytes(), &envelope); decodeErr != nil || envelope.FailureClass != "validation" {
				t.Fatalf("%s envelope=%+v decode=%v", subcommand, envelope, decodeErr)
			}
		})
	}
	err := writeTaskFailure("status", errors.New("read failure"), true, failingOutputWriter{})
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "write failure JSON: output unavailable") {
		t.Fatalf("output failure = %v", err)
	}
}

func TestRunTaskOperationalJSONFailuresUseExitOne(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	var output bytes.Buffer
	err := runTaskRead("status", []string{missing, "--json"}, &output)
	if ExitCode(err) != 1 || !strings.Contains(output.String(), `"failure_class": "operational"`) {
		t.Fatalf("missing repository = output %q, error %v", output.String(), err)
	}

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "task-transaction.json"), []byte(`{"malformed":`), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	err = runTaskRecover([]string{repo, "--json"}, &output)
	if ExitCode(err) != 1 || !strings.Contains(output.String(), `"failure_class": "operational"`) {
		t.Fatalf("malformed transaction = output %q, error %v", output.String(), err)
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
