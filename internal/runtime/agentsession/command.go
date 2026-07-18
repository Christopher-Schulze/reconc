package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// RecordCommandOutcome appends an execution performed by Reconc itself to the
// active agent session. A repo without an active session is valid: durable
// staged command proofs remain available to non-interactive gates.
func RecordCommandOutcome(repoRoot, command, outcome string, exitCode int) error {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("command must be non-empty")
	}
	if outcome != "success" && outcome != "failure" {
		return errors.New("command outcome must be success or failure")
	}
	sessionID, err := ResolveActiveSessionID(root)
	if err != nil || sessionID == "" {
		return err
	}
	interrupted := false
	signatureInput := command + "\x00" + outcome + "\x00" + strconv.Itoa(exitCode)
	signatureHash := sha256.Sum256([]byte(signatureInput))
	_, err = MutateSessionState(root, sessionID, func(state SessionState) SessionState {
		state = AppendCommand(state, command)
		state = AppendCommandResult(state, CommandResult{
			Command: command, Outcome: outcome, EvidenceEpoch: state.EvidenceEpoch, ToolUseID: "reconc-exec",
			ExitCode: &exitCode, IsInterrupt: &interrupted,
		})
		return RecordMaterialEvent(state, "reconc-exec:"+hex.EncodeToString(signatureHash[:]))
	})
	return err
}
