package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
)

var errCompleteCommandCapacity = errors.New("complete command evidence capacity exceeded")

func retainCommandText(state *SessionState, command string) string {
	if len(command) > maxCompleteEvidenceBytes {
		markEvidenceOverflowWithLimit(state, "commands", "item_bytes")
		return ""
	}
	reference := boundCommandText(command)
	if reference != command {
		texts := make(map[string]string, len(state.commandTexts)+1)
		for key, value := range state.commandTexts {
			texts[key] = value
		}
		texts[reference] = command
		state.commandTexts = texts
	}
	return reference
}

func commandTextDigest(reference string) (string, bool) {
	if len(reference) > maxCommandBytes || len(reference) <= maxCommandBytes-utf8.UTFMax {
		return "", false
	}
	index := strings.LastIndex(reference, commandTruncationMarker)
	if index < 0 || !strings.HasSuffix(reference, "]") {
		return "", false
	}
	fields := strings.Split(reference[index+len(commandTruncationMarker):len(reference)-1], " bytes=")
	if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
		return "", false
	}
	digest, err := hex.DecodeString(fields[0])
	if err != nil || hex.EncodeToString(digest) != fields[0] {
		return "", false
	}
	size, err := strconv.Atoi(fields[1])
	if err != nil || size <= maxCommandBytes || strconv.Itoa(size) != fields[1] {
		return "", false
	}
	return fields[0], true
}

func commandTextPath(state SessionState, digest string) string {
	return filepath.Join(filepath.Dir(sessionStatePath(state.RepoRoot, state.SessionID)),
		sessionFileKey(state.SessionID)+".command-"+digest+".txt")
}

func publishCommandTexts(state SessionState) error {
	if len(state.commandTexts) == 0 {
		return nil
	}
	err := publishCommandTextObjects(state)
	if err == nil {
		return nil
	}
	limit := "storage"
	if errors.Is(err, errCompleteCommandCapacity) {
		limit = "aggregate_bytes"
	}
	return persistCommandTextFailure(state, err, limit)
}

func publishCommandTextObjects(state SessionState) error {
	if err := checkCommandTextBudget(state); err != nil {
		return err
	}
	for reference, command := range state.commandTexts {
		if !commandReferenceRetained(state, reference) {
			continue
		}
		digest, ok := commandTextDigest(reference)
		if !ok || len(command) <= maxCommandBytes || len(command) > maxCompleteEvidenceBytes || boundCommandText(command) != reference {
			return fmt.Errorf("complete command text exceeds its %d-byte budget or reference is invalid", maxCompleteEvidenceBytes)
		}
		path := commandTextPath(state, digest)
		body, err := boundedio.ReadRegularFile(path, maxCompleteEvidenceBytes)
		if err == nil {
			if string(body) != command {
				return fmt.Errorf("complete command text %s does not match its digest", digest)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect complete command text: %w", err)
		}
		if _, err := atomicfile.WritePrivateIfChanged(path, []byte(command), 0o600); err != nil {
			return fmt.Errorf("publish complete command text: %w", err)
		}
	}
	return nil
}

func commandReferenceRetained(state SessionState, reference string) bool {
	if containsString(state.Commands, reference) {
		return true
	}
	for _, result := range state.CommandResults {
		if result.Command == reference {
			return true
		}
	}
	return false
}

// resolveCommandTexts verifies every referenced object on each observation,
// including cache hits. Missing legacy tails cannot be reconstructed safely.
func resolveCommandTexts(state SessionState) (SessionState, error) {
	if !sessionHasLongCommandText(state) {
		return state, nil
	}
	state.Commands = cloneSessionStrings(state.Commands)
	state.CommandResults = cloneSessionCommandResults(state.CommandResults)
	resolved := make(map[string]string)
	remaining := maxCompleteEvidenceBytes - mergedEvidenceBytes(state)
	for index, command := range state.Commands {
		full, err := resolveCommandText(state, command, resolved, &remaining)
		if err != nil {
			return SessionState{}, err
		}
		state.Commands[index] = full
	}
	for index, result := range state.CommandResults {
		full, err := resolveCommandText(state, result.Command, resolved, &remaining)
		if err != nil {
			return SessionState{}, err
		}
		state.CommandResults[index].Command = full
	}
	state.commandTexts = nil
	if mergedEvidenceBytes(state) > maxCompleteEvidenceBytes {
		return SessionState{}, errCompleteCommandCapacity
	}
	return state, nil
}

func sessionHasLongCommandText(state SessionState) bool {
	for _, command := range state.Commands {
		if len(command) >= maxCommandBytes-utf8.UTFMax {
			return true
		}
	}
	for _, result := range state.CommandResults {
		if len(result.Command) >= maxCommandBytes-utf8.UTFMax {
			return true
		}
	}
	return false
}

func resolveCommandText(state SessionState, command string, resolved map[string]string, remaining *int) (string, error) {
	reference := boundCommandText(command)
	digest, ok := commandTextDigest(reference)
	if !ok {
		return command, nil
	}
	full, cached := resolved[reference]
	if !cached {
		full, cached = state.commandTexts[reference]
		if !cached {
			body, err := boundedio.ReadRegularFile(commandTextPath(state, digest), maxCompleteEvidenceBytes)
			if err != nil {
				return "", fmt.Errorf("complete command text %s unavailable; original command evidence must be recaptured: %w", digest, err)
			}
			full = string(body)
		}
		if len(full) <= maxCommandBytes || len(full) > maxCompleteEvidenceBytes || boundCommandText(full) != reference {
			return "", fmt.Errorf("complete command text %s does not match its reference", digest)
		}
		resolved[reference] = full
	}
	*remaining -= max(len(full)-len(command), 0)
	if *remaining < 0 {
		return "", fmt.Errorf("%w: maximum %d merged bytes", errCompleteCommandCapacity, maxCompleteEvidenceBytes)
	}
	return full, nil
}

func resolveObservedCommandTexts(state SessionState, cache *StopDecisionCache) (SessionState, error) {
	complete, err := resolveCommandTexts(state)
	if err == nil {
		return complete, nil
	}
	cache.dropVerifiedEvidencePrefix(state.RepoRoot, state.SessionID)
	limit := "chain_integrity"
	if errors.Is(err, errCompleteCommandCapacity) {
		limit = "aggregate_bytes"
	}
	return SessionState{}, persistCommandTextFailure(state, err, limit)
}

func persistCommandTextFailure(state SessionState, cause error, limit string) error {
	markEvidenceOverflowWithLimit(&state, "command_texts", limit)
	return errors.Join(cause, persistEvidenceTaint(state.RepoRoot, state))
}
