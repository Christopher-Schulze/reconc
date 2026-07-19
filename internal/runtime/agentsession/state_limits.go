package agentsession

import (
	"encoding/json"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/retention"
)

const (
	// MaxSessionStateBytes is the hard serialized state ceiling. Historical
	// files get one larger recovery window so they can be compacted safely.
	MaxSessionStateBytes       = 1024 * 1024
	maxLegacySessionStateBytes = 8 * MaxSessionStateBytes

	maxPathEvidenceItems    = 2048
	maxPathEvidenceBytes    = 160 * 1024
	maxCommandEvidenceItems = 512
	maxCommandEvidenceBytes = 160 * 1024
	maxClaimEvidenceItems   = 256
	maxClaimEvidenceBytes   = 32 * 1024
	maxCommandResultItems   = 512
	maxCommandResultBytes   = 256 * 1024
	maxPendingToolCalls     = 64
	maxPendingToolCallBytes = 64 * 1024

	maxPathBytes        = 8 * 1024
	maxCommandBytes     = 32 * 1024
	maxClaimBytes       = 4 * 1024
	maxResultErrorBytes = 4 * 1024
	maxToolUseIDBytes   = 1024
	maxSessionIDBytes   = retention.MaxSessionIDBytes
)

func normalizeSessionState(state SessionState) SessionState {
	overflow := state.EvidenceOverflow
	reason := state.EvidenceOverflowReason
	state.EvidenceOverflow = false
	state.EvidenceOverflowReason = ""

	reads := sortedUnique(state.ReadPaths)
	writes := sortedUnique(state.WritePaths)
	writeEpochs := state.WriteEpochs
	commands := sortedUnique(state.Commands)
	claims := sortedUnique(state.Claims)
	results := append([]CommandResult(nil), state.CommandResults...)
	pending := state.PendingToolCalls

	state.ReadPaths = []string{}
	state.WritePaths = []string{}
	state.WriteEpochs = map[string]uint64{}
	state.Commands = []string{}
	state.Claims = []string{}
	state.CommandResults = []CommandResult{}
	state.PendingToolCalls = nil
	for _, value := range reads {
		state = AppendReadPath(state, value)
	}
	for _, value := range writes {
		state = AppendWritePath(state, value)
		if epoch := writeEpochs[value]; epoch > 0 {
			state.WriteEpochs[value] = epoch
		}
	}
	for _, value := range commands {
		state = AppendCommand(state, value)
	}
	for _, value := range claims {
		state = AppendClaim(state, value)
	}
	for _, result := range results {
		state = AppendCommandResult(state, result)
	}
	keys := make([]string, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		state = PutPendingToolCall(state, key, pending[key])
	}
	if overflow {
		state.EvidenceOverflow = true
		if state.EvidenceOverflowReason == "" {
			state.EvidenceOverflowReason = reason
		}
	}
	return state
}

func appendBoundedString(state *SessionState, values *[]string, item string, maxItems, maxBytes, maxItemBytes int, field string) {
	item = strings.TrimSpace(item)
	if item == "" {
		return
	}
	for _, current := range *values {
		if current == item {
			return
		}
	}
	if len(item) > maxItemBytes || len(*values) >= maxItems || stringBytes(*values)+len(item) > maxBytes {
		markEvidenceOverflow(state, field)
		return
	}
	*values = append(*values, item)
}

func appendBoundedCommandResult(state *SessionState, result CommandResult) {
	result.Command = strings.TrimSpace(result.Command)
	if result.Command == "" || len(result.Command) > maxCommandBytes {
		markEvidenceOverflow(state, "command_results")
		return
	}
	result.Error = truncateBytes(result.Error, maxResultErrorBytes)
	result.ToolUseID = truncateBytes(result.ToolUseID, maxToolUseIDBytes)
	for _, current := range state.CommandResults {
		if commandResultsEqual(current, result) {
			return
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(state.CommandResults) >= maxCommandResultItems || commandResultBytes(state.CommandResults)+len(encoded) > maxCommandResultBytes {
		markEvidenceOverflow(state, "command_results")
		return
	}
	state.CommandResults = append(state.CommandResults, result)
}

// PutPendingToolCall stores one adapter correlation record under the same
// deterministic bounds as the rest of session state.
func PutPendingToolCall(state SessionState, key string, call PendingToolCall) SessionState {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > maxToolUseIDBytes {
		markEvidenceOverflow(&state, "pending_tool_calls")
		return state
	}
	call.ToolName = truncateBytes(strings.TrimSpace(call.ToolName), 1024)
	call.ToolUseID = truncateBytes(strings.TrimSpace(call.ToolUseID), maxToolUseIDBytes)
	encoded, err := json.Marshal(call)
	if err != nil || len(encoded) > maxPendingToolCallBytes {
		markEvidenceOverflow(&state, "pending_tool_calls")
		return state
	}
	if state.PendingToolCalls == nil {
		state.PendingToolCalls = map[string]PendingToolCall{}
	}
	if _, exists := state.PendingToolCalls[key]; !exists && len(state.PendingToolCalls) >= maxPendingToolCalls {
		markEvidenceOverflow(&state, "pending_tool_calls")
		return state
	}
	state.PendingToolCalls[key] = call
	return state
}

func markEvidenceOverflow(state *SessionState, field string) {
	state.EvidenceOverflow = true
	if state.EvidenceOverflowReason == "" {
		state.EvidenceOverflowReason = field
	}
}

func stringBytes(values []string) int {
	total := 0
	for _, value := range values {
		total += len(value)
	}
	return total
}

func commandResultBytes(results []CommandResult) int {
	total := 0
	for _, result := range results {
		encoded, err := json.Marshal(result)
		if err == nil {
			total += len(encoded)
		}
	}
	return total
}

func commandResultsEqual(a, b CommandResult) bool {
	aBytes, aErr := json.Marshal(a)
	bBytes, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && string(aBytes) == string(bBytes)
}

func truncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
