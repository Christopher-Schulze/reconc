package agentsession

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// CommandExecution binds a Reconc-owned execution to its start session and
// causal epoch. Private fields prevent callers from manufacturing a binding.
type CommandExecution struct {
	root       ResolvedRepoRoot
	sessionID  string
	epoch      uint64
	generation string
}

// CaptureCommandExecution selects the evidence owner before the process starts.
// No active session is valid and cannot attach to a session created later.
func CaptureCommandExecution(repoRoot string) (CommandExecution, error) {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return CommandExecution{}, err
	}
	sessionID, err := resolveActiveSessionIDResolved(root.Path())
	binding := CommandExecution{root: root, sessionID: sessionID}
	if err != nil || sessionID == "" {
		return binding, err
	}
	err = withSessionLock(root.Path(), sessionID, func() error {
		if err := requireExistingSessionStateResolved(root.Path(), sessionID); err != nil {
			return err
		}
		state, err := loadSessionStateResolved(root.Path(), sessionID)
		if err != nil {
			return err
		}
		if state.EvidenceOverflow {
			return errors.New(evidenceOverflowMessage(state))
		}
		if state.CommandEvidenceGeneration == "" {
			var identity [16]byte
			count, err := rand.Read(identity[:])
			if err != nil {
				return fmt.Errorf("create command evidence generation: %w", err)
			}
			state.CommandEvidenceGeneration = hex.EncodeToString(identity[:count])
			if err := saveSessionStateLocked(state); err != nil {
				return err
			}
		}
		binding.epoch = state.EvidenceEpoch
		binding.generation = state.CommandEvidenceGeneration
		return nil
	})
	return binding, err
}

// RecordCommandOutcome appends the real result to the start-bound session,
// without advancing its epoch or changing the current active-session pointer.
func RecordCommandOutcome(binding CommandExecution, command, outcome string, exitCode int) error {
	if err := binding.root.Revalidate(); err != nil {
		return err
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("command must be non-empty")
	}
	if outcome != "success" && outcome != "failure" {
		return errors.New("command outcome must be success or failure")
	}
	if binding.sessionID == "" {
		return nil
	}
	var bindingErr error
	updated, err := mutateSessionStateResolvedMode(binding.root.Path(), binding.sessionID, func(state SessionState) SessionState {
		state, bindingErr = appendCommandExecutionResult(state, binding, command, outcome, exitCode)
		return state
	}, false)
	if err != nil {
		return err
	}
	if bindingErr != nil {
		return bindingErr
	}
	if updated.EvidenceOverflow {
		return errors.New(evidenceOverflowMessage(updated))
	}
	return nil
}

func appendCommandExecutionResult(state SessionState, binding CommandExecution, command, outcome string, exitCode int) (SessionState, error) {
	if state.CommandEvidenceGeneration != binding.generation || state.EvidenceEpoch < binding.epoch {
		return state, errors.New("bound session was restarted or its causal epoch regressed")
	}
	if state.EvidenceOverflow {
		return state, errors.New(evidenceOverflowMessage(state))
	}
	interrupted := false
	state = AppendCommand(state, command)
	state = AppendCommandResult(state, CommandResult{
		Command: command, Outcome: outcome, EvidenceEpoch: binding.epoch, ToolUseID: "reconc-exec",
		ExitCode: &exitCode, IsInterrupt: &interrupted,
	})
	signatureInput := command + "\x00" + outcome + "\x00" + strconv.Itoa(exitCode)
	signatureHash := sha256.Sum256([]byte(signatureInput))
	return RecordMaterialEvent(state, "reconc-exec:"+hex.EncodeToString(signatureHash[:])), nil
}
