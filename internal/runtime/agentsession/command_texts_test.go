package agentsession

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime"
)

func TestRecordedLongCommandRetainsForbiddenTail(t *testing.T) {
	fixture := declaredCommandFixture(t, "rules:\n  - id: no-publish\n    kind: forbid_command\n    commands: ['npm publish']\n    mode: block\n    message: no publication\n")
	command := "echo " + strings.Repeat("x", maxCommandBytes+100) + "\nnpm publish"
	payload, err := json.Marshal(struct {
		SessionID    string            `json:"session_id"`
		ToolName     string            `json:"tool_name"`
		ToolInput    map[string]string `json:"tool_input"`
		ToolResponse map[string]int    `json:"tool_response"`
	}{fixture.sessionID, "Bash", map[string]string{"command": command}, map[string]int{"exit_code": 0}})
	if err != nil {
		t.Fatal(err)
	}
	if post := RunPostToolUse(fixture.repo, payload); post.ExitCode != 0 {
		t.Fatalf("post: %+v", post)
	}
	state, err := LoadSessionState(fixture.repo, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Commands) != 1 || state.Commands[0] != command || state.CommandResults[0].Command != command {
		t.Fatal("persisted command or result lost its complete text")
	}
	inputs := runtime.Empty()
	inputs.Commands = state.Commands
	report, err := runtime.CheckRepoPolicy(fixture.repo, inputs)
	if err != nil || report.Decision != runtime.DecisionBlock {
		t.Fatalf("forbidden tail bypassed policy: report=%+v err=%v", report, err)
	}
	stopPayload, err := json.Marshal(map[string]string{"session_id": fixture.sessionID})
	if err != nil {
		t.Fatal(err)
	}
	stop := RunStop(fixture.repo, stopPayload)
	if !strings.Contains(stop.Stdout, `"decision":"block"`) || !strings.Contains(stop.Stdout, "no-publish") {
		t.Fatalf("real Stop accepted the forbidden tail: %+v", stop)
	}
}

func TestCommandTextIntegrityIncludesStopCacheReuse(t *testing.T) {
	for _, damage := range []string{"missing", "changed", "reference"} {
		t.Run(damage, func(t *testing.T) {
			_, repo := withStateRoot(t)
			repo, err := ResolveRepoRoot(repo)
			if err != nil {
				t.Fatal(err)
			}
			command := "echo " + strings.Repeat("x", maxCommandBytes+100)
			state := AppendCommand(emptyState(repo, "integrity"), command)
			if err := SaveSessionState(state); err != nil {
				t.Fatal(err)
			}
			raw, err := loadSessionStateWithLockResolved(repo, state.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			loaded := &stopLoadedEvidence{}
			if _, _, err := loaded.load(repo, raw, nil); err != nil {
				t.Fatal(err)
			}
			digest, _ := commandTextDigest(boundCommandText(command))
			damageCommandText(t, commandTextPath(raw, digest), damage)
			if _, err := LoadSessionState(repo, state.SessionID); err == nil {
				t.Fatal("public state load accepted damaged command text")
			}
			if _, _, err := loaded.load(repo, raw, nil); err == nil {
				t.Fatal("Stop cache reused damaged command text")
			}
			status, err := ReadEvidenceTaintStatus(repo)
			if err != nil || !status.Present || !status.Persisted || status.Field != "command_texts" {
				t.Fatalf("damaged command evidence did not persist its gap: %+v %v", status, err)
			}
		})
	}
}

func TestCommandTextPublicationFailurePersistsTaint(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Repeat("x", maxCommandBytes+100)
	state := AppendCommand(emptyState(root, "publication-integrity"), command)
	if err := SaveSessionState(state); err != nil {
		t.Fatal(err)
	}
	digest, _ := commandTextDigest(boundCommandText(command))
	damageCommandText(t, commandTextPath(state, digest), "changed")
	if err := SaveSessionState(state); err == nil {
		t.Fatal("publication reused corrupted command bytes")
	}
	status, err := ReadEvidenceTaintStatus(root)
	if err != nil || !status.Persisted || status.Field != "command_texts" || status.Limit != "storage" {
		t.Fatalf("publication failure did not durably block subsequent decisions: %+v %v", status, err)
	}
}

func TestCommandTextStorageIsBoundedAcrossPostToolEvents(t *testing.T) {
	_, repo := withStateRoot(t)
	const sessionID = "bounded-objects"
	if _, err := InitializeSessionState(repo, sessionID); err != nil {
		t.Fatal(err)
	}
	for index := range 4 {
		command := strings.Repeat(string(rune('a'+index)), maxCompleteEvidenceBytes/4+1)
		_, err := MutateSessionState(repo, sessionID, func(state SessionState) SessionState {
			return AppendCommand(state, command)
		})
		if index < 3 && err != nil {
			t.Fatal(err)
		}
		if index == 3 && !errors.Is(err, errCompleteCommandCapacity) {
			t.Fatalf("storage capacity not enforced: %v", err)
		}
	}
	status, err := ReadEvidenceTaintStatus(repo)
	if err != nil || !status.Present || !status.Persisted || status.Limit != "aggregate_bytes" {
		t.Fatalf("storage gap was not durable: %+v %v", status, err)
	}
}

func TestCommandTextOverlargeItemTaintsInsteadOfDroppingItsMeaning(t *testing.T) {
	_, repo := withStateRoot(t)
	const sessionID = "oversized-command"
	_, err := MutateSessionState(repo, sessionID, func(state SessionState) SessionState {
		return AppendCommand(state, strings.Repeat("x", maxCompleteEvidenceBytes+1))
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadEvidenceTaintStatus(repo)
	if err != nil || !status.Present || !status.Persisted || status.Limit != "item_bytes" {
		t.Fatalf("oversized command was silently dropped: %+v %v", status, err)
	}
}

func TestCommandTextAggregateBudgetCountsJSONEscapes(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	state := AppendCommandResult(emptyState(root, "encoded-budget"), CommandResult{
		Command: strings.Repeat("\x01", 3<<20), Outcome: "success",
	})
	if err := SaveSessionState(state); err != nil {
		t.Fatal(err)
	}
	raw, err := loadSessionStateWithLockResolved(root, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadCompleteSessionEvidence(root, raw); !errors.Is(err, errCompleteCommandCapacity) {
		t.Fatalf("encoded result exceeded the aggregate budget silently: %v", err)
	}
	status, err := ReadEvidenceTaintStatus(repo)
	if err != nil || !status.Present || status.Limit != "aggregate_bytes" {
		t.Fatalf("encoded-size overflow was not durable: %+v %v", status, err)
	}
}

func damageCommandText(t *testing.T, path, damage string) {
	t.Helper()
	if damage == "missing" {
		if err := os.Rename(path, path+".unavailable"); err != nil {
			t.Fatal(err)
		}
		return
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("false")
	if damage == "reference" {
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(errors.Join(err, file.Close()))
		}
		body = []byte(boundCommandText(string(original)))
	}
	_, writeErr := file.WriteAt(body, 0)
	var truncateErr error
	if damage == "reference" {
		truncateErr = file.Truncate(int64(len(body)))
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, truncateErr, closeErr); err != nil {
		t.Fatalf("damage command: %v", err)
	}
}

func TestCommandTextSurvivesEvidenceRotation(t *testing.T) {
	_, repo := withStateRoot(t)
	repo, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "long-rotation"
	if _, err := InitializeSessionState(repo, sessionID); err != nil {
		t.Fatal(err)
	}
	commands := []string{}
	for index := 0; index < 6; index++ {
		commands = append(commands, strings.Repeat(string(rune('a'+index)), maxCommandBytes+100))
	}
	for _, command := range commands {
		appendCommandValues(t, repo, sessionID, []string{command})
	}
	raw, err := loadSessionStateWithLockResolved(repo, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if raw.EvidenceSegmentCount == 0 {
		t.Fatal("fixture did not rotate")
	}
	complete, err := loadCompleteSessionEvidence(repo, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if !containsString(complete.Commands, command) {
			t.Fatal("rotation lost complete command text")
		}
	}
}
