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
	appendNormalizedExactStrings(&state, &state.ReadPaths, reads, maxPathEvidenceItems, maxPathEvidenceBytes, maxPathBytes, "read_paths")
	appendNormalizedExactStrings(&state, &state.WritePaths, writes, maxPathEvidenceItems, maxPathEvidenceBytes, maxPathBytes, "write_paths")
	for _, value := range writes {
		if epoch := writeEpochs[value]; epoch > 0 {
			state.WriteEpochs[value] = epoch
		}
	}
	appendNormalizedExactStrings(&state, &state.Commands, commands, maxCommandEvidenceItems, maxCommandEvidenceBytes, maxCommandBytes, "commands")
	appendNormalizedExactStrings(&state, &state.Claims, claims, maxClaimEvidenceItems, maxClaimEvidenceBytes, maxClaimBytes, "claims")
	appendNormalizedCommandResults(&state, results)
	keys := make([]string, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		state = putPendingToolCall(state, key, pending[key], false)
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

// appendNormalizedExactStrings appends an already sorted and deduplicated
// collection in one bounded pass. The caller performs membership work while
// building the normalized input, so this path only accounts retained bytes.
func appendNormalizedExactStrings(state *SessionState, target *[]string, values []string, maxItems, maxBytes, maxItemBytes int, field string) {
	retainedBytes := 0
	for _, item := range values {
		if item == "" {
			continue
		}
		if len(item) > maxItemBytes {
			markEvidenceOverflowWithLimit(state, field, "item_bytes")
			continue
		}
		if len(*target) >= maxItems {
			markEvidenceOverflowWithLimit(state, field, "item_count")
			continue
		}
		if retainedBytes+len(item) > maxBytes {
			markEvidenceOverflowWithLimit(state, field, "byte_budget")
			continue
		}
		*target = append(*target, item)
		retainedBytes += len(item)
	}
}

func appendBoundedString(state *SessionState, values *[]string, item string, maxItems, maxBytes, maxItemBytes int, field string) {
	item = strings.TrimSpace(item)
	appendBoundedExactString(state, values, item, maxItems, maxBytes, maxItemBytes, field)
}

func appendBoundedExactString(state *SessionState, values *[]string, item string, maxItems, maxBytes, maxItemBytes int, field string) {
	retainedBytes := stringBytes(*values)
	appendBoundedExactStringPrepared(state, values, item, maxItems, maxBytes, maxItemBytes, field, nil, &retainedBytes)
}

func appendBoundedExactStringPrepared(
	state *SessionState,
	values *[]string,
	item string,
	maxItems, maxBytes, maxItemBytes int,
	field string,
	seen map[string]struct{},
	retainedBytes *int,
) bool {
	if item == "" {
		return false
	}
	if seen != nil {
		if _, exists := seen[item]; exists {
			return false
		}
	} else {
		for _, current := range *values {
			if current == item {
				return false
			}
		}
	}
	if len(item) > maxItemBytes {
		markEvidenceOverflowWithLimit(state, field, "item_bytes")
		return false
	}
	if len(*values) >= maxItems {
		markEvidenceOverflowWithLimit(state, field, "item_count")
		return false
	}
	if *retainedBytes+len(item) > maxBytes {
		markEvidenceOverflowWithLimit(state, field, "byte_budget")
		return false
	}
	*values = append(*values, item)
	*retainedBytes += len(item)
	if seen != nil {
		seen[item] = struct{}{}
	}
	return true
}

func appendBoundedCommandResult(state *SessionState, result CommandResult) {
	retainedBytes := state.CommandResultBytes
	appendBoundedCommandResultPrepared(state, result, nil, &retainedBytes)
}

func appendNormalizedCommandResults(state *SessionState, results []CommandResult) {
	seen := make(map[commandResultKey]struct{}, len(results))
	retainedBytes := int64(0)
	for _, result := range results {
		appendBoundedCommandResultPrepared(state, result, seen, &retainedBytes)
	}
}

func appendBoundedCommandResultPrepared(state *SessionState, result CommandResult, seen map[commandResultKey]struct{}, retainedBytes *int64) {
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
	key := commandResultIdentity(result)
	if seen != nil {
		if _, exists := seen[key]; exists {
			return
		}
	} else {
		for _, current := range state.CommandResults {
			if commandResultsEqual(current, result) {
				return
			}
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
	if *retainedBytes+int64(len(encoded)) > maxCommandResultBytes {
		markEvidenceOverflowWithLimit(state, "command_results", "byte_budget")
		return
	}
	*retainedBytes += int64(len(encoded))
	state.CommandResultBytes = *retainedBytes
	state.CommandResults = append(state.CommandResults, result)
	if seen != nil {
		seen[key] = struct{}{}
	}
}

// PutPendingToolCall stores one adapter correlation record under the same
// deterministic bounds as the rest of session state.
func PutPendingToolCall(state SessionState, key string, call PendingToolCall) SessionState {
	return putPendingToolCall(state, key, call, true)
}

func putPendingToolCall(state SessionState, key string, call PendingToolCall, clone bool) SessionState {
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
	if _, exists := state.PendingToolCalls[key]; !exists && len(state.PendingToolCalls) >= maxPendingToolCalls {
		markEvidenceOverflowWithLimit(&state, "pending_tool_calls", "item_count")
		return state
	}
	if clone || state.PendingToolCalls == nil {
		pending := make(map[string]PendingToolCall, len(state.PendingToolCalls)+1)
		for currentKey, currentCall := range state.PendingToolCalls {
			pending[currentKey] = currentCall
		}
		state.PendingToolCalls = pending
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
