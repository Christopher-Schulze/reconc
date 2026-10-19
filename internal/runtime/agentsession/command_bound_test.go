package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOversizedCommandIsBoundToDigestWithoutTaint(t *testing.T) {
	long := "cat > script.py <<'EOF'\n" + strings.Repeat("print('x')\n", 6000) + "EOF"
	other := long + "\n# changed tail"
	state := emptyState("/repo", "bound")
	state = AppendCommand(state, long)
	state = AppendCommand(state, long)
	state = AppendCommand(state, other)
	if state.EvidenceOverflow {
		t.Fatalf("oversized command tainted the session: %s/%s", state.EvidenceOverflowReason, state.EvidenceOverflowLimit)
	}
	if len(state.Commands) != 2 {
		t.Fatalf("commands = %d, want the equal long command deduplicated and the different one kept", len(state.Commands))
	}
	digest := sha256.Sum256([]byte(long))
	want := commandTruncationMarker + hex.EncodeToString(digest[:]) + " bytes=" + strconv.Itoa(len(long)) + "]"
	var found bool
	for _, stored := range state.Commands {
		if len(stored) > maxCommandBytes {
			t.Fatalf("stored command has %d bytes, limit %d", len(stored), maxCommandBytes)
		}
		if strings.HasSuffix(stored, want) {
			found = true
			if !strings.HasPrefix(long, strings.TrimSuffix(stored, want)) {
				t.Fatal("stored prefix is not the leading text of the full command")
			}
		}
	}
	if !found {
		t.Fatalf("no stored command carries the full-text digest marker %q", want)
	}
	if !sessionStateIsNormalized(normalizeSessionState(state)) {
		t.Fatal("bounded commands are not normalized state")
	}
}

func TestOversizedCommandResultIsBoundWithoutTaint(t *testing.T) {
	long := "python3 - <<'EOF'\n" + strings.Repeat("a = 1\n", 9000) + "EOF"
	exitCode := 0
	state := emptyState("/repo", "bound-result")
	state = AppendCommandResult(state, CommandResult{Command: long, Outcome: "success", ExitCode: &exitCode})
	if state.EvidenceOverflow {
		t.Fatalf("oversized command result tainted the session: %s/%s", state.EvidenceOverflowReason, state.EvidenceOverflowLimit)
	}
	if len(state.CommandResults) != 1 {
		t.Fatalf("command results = %d, want 1", len(state.CommandResults))
	}
	stored := state.CommandResults[0].Command
	if len(stored) > maxCommandBytes || stored != boundCommandText(long) {
		t.Fatalf("stored result command is not the bounded form (%d bytes)", len(stored))
	}
	encoded, err := json.Marshal(state.CommandResults[0])
	if err != nil || state.CommandResultBytes != int64(len(encoded)) {
		t.Fatalf("result byte accounting = %d, encoded %d err=%v", state.CommandResultBytes, len(encoded), err)
	}
}

func TestBoundCommandTextKeepsUTF8AndShortCommands(t *testing.T) {
	if got := boundCommandText("go test ./..."); got != "go test ./..." {
		t.Fatalf("short command changed: %q", got)
	}
	exact := strings.Repeat("y", maxCommandBytes)
	if got := boundCommandText(exact); got != exact {
		t.Fatal("command at the limit was changed")
	}
	multibyte := "echo " + strings.Repeat("äöü€", maxCommandBytes/4)
	bound := boundCommandText(multibyte)
	if !utf8.ValidString(bound) || len(bound) > maxCommandBytes {
		t.Fatalf("bounded multibyte command invalid=%v bytes=%d", !utf8.ValidString(bound), len(bound))
	}
}

func TestNormalizationBoundsHistoricalOversizedCommands(t *testing.T) {
	state := emptyState("/repo", "legacy")
	state.Commands = []string{strings.Repeat("z", maxCommandBytes+10), "go vet ./..."}
	normalized := normalizeSessionState(state)
	if normalized.EvidenceOverflow {
		t.Fatalf("historical oversized command tainted normalization: %s/%s", normalized.EvidenceOverflowReason, normalized.EvidenceOverflowLimit)
	}
	if len(normalized.Commands) != 2 {
		t.Fatalf("commands = %+v", normalized.Commands)
	}
	for _, command := range normalized.Commands {
		if len(command) > maxCommandBytes {
			t.Fatalf("normalized command keeps %d bytes", len(command))
		}
	}
}

func TestLongHeredocDoesNotSealTheSession(t *testing.T) {
	repo := setupPolicyRepo(t)
	if result := RunSessionStart(repo, []byte(`{"session_id":"heredoc"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	heredoc := "cat > /dev/null <<'EOF'\n" + strings.Repeat("payload line\n", 5600) + "EOF"
	payload, err := json.Marshal(map[string]interface{}{
		"session_id":    "heredoc",
		"tool_name":     "Bash",
		"tool_input":    map[string]string{"command": heredoc},
		"tool_response": map[string]interface{}{"exit_code": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(heredoc) <= maxCommandBytes {
		t.Fatalf("fixture is only %d bytes", len(heredoc))
	}
	if result := RunPostToolUse(repo, payload); result.ExitCode != 0 {
		t.Fatalf("recording the long command failed: %+v", result)
	}
	state, err := LoadSessionState(repo, "heredoc")
	if err != nil || state.EvidenceOverflow {
		t.Fatalf("long command tainted the session: %+v err=%v", state.EvidenceOverflowReason, err)
	}
	next := RunPreToolUse(repo, []byte(`{"session_id":"heredoc","tool_name":"Bash","tool_input":{"command":"ls"}}`))
	if next.ExitCode != 0 {
		t.Fatalf("the next command was blocked after a long heredoc: %+v", next)
	}
}
