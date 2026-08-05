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
	limit := state.EvidenceOverflowLimit
	state.EvidenceOverflow = false
	state.EvidenceOverflowReason = ""
	state.EvidenceOverflowLimit = ""

	reads := sortedUniqueExact(state.ReadPaths)
	writes := sortedUniqueExact(state.WritePaths)
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
	state.CommandResultBytes = 0
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
		if state.EvidenceOverflowLimit == "" {
			state.EvidenceOverflowLimit = limit
		}
	}
	return state
}

func appendBoundedString(state *SessionState, values *[]string, item string, maxItems, maxBytes, maxItemBytes int, field string) {
	item = strings.TrimSpace(item)
	appendBoundedExactString(state, values, item, maxItems, maxBytes, maxItemBytes, field)
}

func appendBoundedExactString(state *SessionState, values *[]string, item string, maxItems, maxBytes, maxItemBytes int, field string) {
	if item == "" {
		return
	}
	for _, current := range *values {
		if current == item {
			return
		}
	}
	if len(item) > maxItemBytes {
		markEvidenceOverflowWithLimit(state, field, "item_bytes")
		return
	}
	if len(*values) >= maxItems {
		markEvidenceOverflowWithLimit(state, field, "item_count")
		return
	}
	if stringBytes(*values)+len(item) > maxBytes {
		markEvidenceOverflowWithLimit(state, field, "byte_budget")
		return
	}
	*values = append(*values, item)
}

func appendBoundedCommandResult(state *SessionState, result CommandResult) {
	result.Command = strings.TrimSpace(result.Command)
	if result.Command == "" {
		return
	}
	if len(result.Command) > maxCommandBytes {
		markEvidenceOverflowWithLimit(state, "command_results", "item_bytes")
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
	if err != nil {
		markEvidenceOverflowWithLimit(state, "command_results", "serialization")
		return
	}
	if len(state.CommandResults) >= maxCommandResultItems {
		markEvidenceOverflowWithLimit(state, "command_results", "item_count")
		return
	}
	if state.CommandResultBytes+int64(len(encoded)) > maxCommandResultBytes {
		markEvidenceOverflowWithLimit(state, "command_results", "byte_budget")
		return
	}
	state.CommandResultBytes += int64(len(encoded))
	state.CommandResults = append(state.CommandResults, result)
}

// PutPendingToolCall stores one adapter correlation record under the same
// deterministic bounds as the rest of session state.
func PutPendingToolCall(state SessionState, key string, call PendingToolCall) SessionState {
	key = strings.TrimSpace(key)
	if key == "" {
		return state
	}
	if len(key) > maxToolUseIDBytes {
		markEvidenceOverflowWithLimit(&state, "pending_tool_calls", "item_bytes")
		return state
	}
	call.ToolName = truncateBytes(strings.TrimSpace(call.ToolName), 1024)
	call.ToolUseID = truncateBytes(strings.TrimSpace(call.ToolUseID), maxToolUseIDBytes)
	encoded, err := json.Marshal(call)
	if err != nil {
		markEvidenceOverflowWithLimit(&state, "pending_tool_calls", "serialization")
		return state
	}
	if len(encoded) > maxPendingToolCallBytes {
		markEvidenceOverflowWithLimit(&state, "pending_tool_calls", "item_bytes")
		return state
	}
	if state.PendingToolCalls == nil {
		state.PendingToolCalls = map[string]PendingToolCall{}
	}
	if _, exists := state.PendingToolCalls[key]; !exists && len(state.PendingToolCalls) >= maxPendingToolCalls {
		markEvidenceOverflowWithLimit(&state, "pending_tool_calls", "item_count")
		return state
	}
	state.PendingToolCalls[key] = call
	return state
}

func markEvidenceOverflowWithLimit(state *SessionState, field, limit string) {
	state.EvidenceOverflow = true
	if state.EvidenceOverflowReason == "" {
		state.EvidenceOverflowReason = field
	}
	if state.EvidenceOverflowLimit == "" {
		state.EvidenceOverflowLimit = limit
	}
}

func stringBytes(values []string) int {
	total := 0
	for _, value := range values {
		total += len(value)
	}
	return total
}

// commandResultEncodedBytes returns the JSON-encoded size of one command
// result, the unit the persisted SessionState.CommandResultBytes counter
// accumulates. Marshal errors contribute zero, matching the previous
// aggregate accounting.
func commandResultEncodedBytes(result CommandResult) int {
	encoded, err := json.Marshal(result)
	if err != nil {
		return 0
	}
	return len(encoded)
}

type commandResultKey struct {
	Command       string
	Outcome       string
	EvidenceEpoch uint64
	ToolUseID     string
	HasExitCode   bool
	ExitCode      int
	Error         string
	HasInterrupt  bool
	IsInterrupt   bool
}

func commandResultIdentity(result CommandResult) commandResultKey {
	key := commandResultKey{
		Command:       result.Command,
		Outcome:       result.Outcome,
		EvidenceEpoch: result.EvidenceEpoch,
		ToolUseID:     result.ToolUseID,
		Error:         result.Error,
	}
	if result.ExitCode != nil {
		key.HasExitCode = true
		key.ExitCode = *result.ExitCode
	}
	if result.IsInterrupt != nil {
		key.HasInterrupt = true
		key.IsInterrupt = *result.IsInterrupt
	}
	return key
}

func commandResultsEqual(a, b CommandResult) bool {
	return commandResultIdentity(a) == commandResultIdentity(b)
}

func truncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
