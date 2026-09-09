package agentsession

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/retention"
)

const (
	// MaxSessionStateBytes is the hard serialized state ceiling. Historical
	// files get one larger recovery window so they can be compacted safely.
	MaxSessionStateBytes       = 1024 * 1024
	maxLegacySessionStateBytes = 8 * MaxSessionStateBytes

	maxPathEvidenceItems          = 2048
	maxPathEvidenceBytes          = 160 * 1024
	maxCommandEvidenceItems       = 512
	maxCommandEvidenceBytes       = 160 * 1024
	maxClaimEvidenceItems         = 256
	maxClaimEvidenceBytes         = 32 * 1024
	maxCommandResultItems         = 512
	maxCommandResultBytes         = 256 * 1024
	maxPendingToolCalls           = 64
	maxRetiredToolCallKeys        = 4 * maxPendingToolCalls
	maxPendingToolCallBytes       = 64 * 1024
	pendingToolCallLifetime       = 24 * time.Hour
	maxConsumedApprovalIdentities = 256
	maxConsumedApprovalBytes      = 64 * 1024
	maxConsumedApprovalBytesEach  = 256

	maxPathBytes        = 8 * 1024
	maxCommandBytes     = 32 * 1024
	maxClaimBytes       = 4 * 1024
	maxResultErrorBytes = 4 * 1024
	maxToolUseIDBytes   = 1024
	maxSessionIDBytes   = retention.MaxSessionIDBytes
)

func normalizeSessionState(state SessionState) SessionState {
	if sessionStateIsNormalized(state) {
		return state
	}
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
	retired := state.RetiredToolCallKeys
	consumedApprovals := sortedUnique(state.ConsumedApprovalIdentities)

	state.ReadPaths = []string{}
	state.WritePaths = []string{}
	state.WriteEpochs = map[string]uint64{}
	state.Commands = []string{}
	state.Claims = []string{}
	state.CommandResults = []CommandResult{}
	state.CommandResultBytes = 0
	state.PendingToolCalls = nil
	state.RetiredToolCallKeys = nil
	state.ConsumedApprovalIdentities = []string{}
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
	keys = keys[:0]
	for key := range retired {
		keys = append(keys, key)
	}
	appendNormalizedExactStrings(&state, &state.ConsumedApprovalIdentities, consumedApprovals,
		maxConsumedApprovalIdentities, maxConsumedApprovalBytes, maxConsumedApprovalBytesEach,
		"consumed_approval_identities")
	sort.Strings(keys)
	for _, key := range keys {
		state = putRetiredToolCallKey(state, key, retired[key], false)
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

// sessionStateIsNormalized verifies the in-memory admission contract used by
// MutateSessionState. It intentionally performs no repairs and returns false
// for any shape whose deterministic normalizer could change it. Keeping this
// check separate from normalizeSessionState lets trusted mutators publish a
// canonical state without rebuilding every bounded collection, while
// arbitrary callbacks still receive the defensive normalization path.
func sessionStateIsNormalized(state SessionState) bool {
	if !state.EvidenceOverflow && (state.EvidenceOverflowReason != "" || state.EvidenceOverflowLimit != "") {
		return false
	}
	if !normalizedExactStrings(state.ReadPaths, maxPathEvidenceItems, maxPathEvidenceBytes, maxPathBytes) ||
		!normalizedExactStrings(state.WritePaths, maxPathEvidenceItems, maxPathEvidenceBytes, maxPathBytes) ||
		!normalizedStrings(state.Commands, maxCommandEvidenceItems, maxCommandEvidenceBytes, maxCommandBytes) ||
		!normalizedStrings(state.Claims, maxClaimEvidenceItems, maxClaimEvidenceBytes, maxClaimBytes) ||
		!normalizedStrings(state.ConsumedApprovalIdentities, maxConsumedApprovalIdentities, maxConsumedApprovalBytes, maxConsumedApprovalBytesEach) {
		return false
	}
	if state.WriteEpochs == nil || len(state.WriteEpochs) > len(state.WritePaths) {
		return false
	}
	for path, epoch := range state.WriteEpochs {
		index := sort.SearchStrings(state.WritePaths, path)
		if epoch == 0 || index == len(state.WritePaths) || state.WritePaths[index] != path {
			return false
		}
	}
	if !normalizedCommandResults(state.CommandResults, state.CommandResultBytes) ||
		!normalizedPendingToolCalls(state.PendingToolCalls) ||
		!normalizedRetiredToolCallKeys(state.RetiredToolCallKeys) {
		return false
	}
	return true
}

func normalizedExactStrings(values []string, maxItems, maxBytes, maxItemBytes int) bool {
	if values == nil || len(values) > maxItems {
		return false
	}
	retainedBytes := 0
	for index, value := range values {
		if value == "" || len(value) > maxItemBytes || retainedBytes+len(value) > maxBytes {
			return false
		}
		if index > 0 && values[index-1] >= value {
			return false
		}
		retainedBytes += len(value)
	}
	return true
}

func normalizedStrings(values []string, maxItems, maxBytes, maxItemBytes int) bool {
	if values == nil || len(values) > maxItems {
		return false
	}
	retainedBytes := 0
	for index, value := range values {
		if value == "" || strings.TrimSpace(value) != value || len(value) > maxItemBytes || retainedBytes+len(value) > maxBytes {
			return false
		}
		if index > 0 && values[index-1] >= value {
			return false
		}
		retainedBytes += len(value)
	}
	return true
}

func normalizedCommandResults(results []CommandResult, encodedBytes int64) bool {
	if results == nil || len(results) > maxCommandResultItems {
		return false
	}
	var seen map[commandResultKey]struct{}
	if len(results) > 0 {
		seen = make(map[commandResultKey]struct{}, len(results))
	}
	retainedBytes := int64(0)
	for _, result := range results {
		if result.Command == "" || strings.TrimSpace(result.Command) != result.Command || len(result.Command) > maxCommandBytes ||
			result.Error != truncateBytes(result.Error, maxResultErrorBytes) ||
			result.ToolUseID != truncateBytes(result.ToolUseID, maxToolUseIDBytes) {
			return false
		}
		key := commandResultIdentity(result)
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
		encoded, err := json.Marshal(result)
		if err != nil {
			return false
		}
		retainedBytes += int64(len(encoded))
		if retainedBytes > maxCommandResultBytes {
			return false
		}
	}
	return encodedBytes == retainedBytes
}

func normalizedPendingToolCalls(calls map[string]PendingToolCall) bool {
	if len(calls) == 0 {
		return calls == nil
	}
	if len(calls) > maxPendingToolCalls {
		return false
	}
	for key, call := range calls {
		if key == "" || strings.TrimSpace(key) != key || len(key) > maxToolUseIDBytes ||
			call.ToolName != truncateBytes(strings.TrimSpace(call.ToolName), 1024) ||
			call.ToolUseID != truncateBytes(strings.TrimSpace(call.ToolUseID), maxToolUseIDBytes) {
			return false
		}
		encoded, err := json.Marshal(call)
		if err != nil || len(encoded) > maxPendingToolCallBytes {
			return false
		}
	}
	return true
}

func normalizedRetiredToolCallKeys(keys map[string]int64) bool {
	if len(keys) == 0 {
		return keys == nil
	}
	if len(keys) > maxRetiredToolCallKeys {
		return false
	}
	for key, retiredAt := range keys {
		if key == "" || strings.TrimSpace(key) != key || len(key) > maxToolUseIDBytes || retiredAt <= 0 {
			return false
		}
	}
	return true
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
	insertAt := sort.SearchStrings(*values, item)
	*values = append(*values, "")
	copy((*values)[insertAt+1:], (*values)[insertAt:])
	(*values)[insertAt] = item
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

func putPendingToolCallTransient(state SessionState, key string, call PendingToolCall) (SessionState, error) {
	key = strings.TrimSpace(key)
	if _, exists := state.PendingToolCalls[key]; !exists && antigravityStepRetired(state, key) {
		return state, fmt.Errorf("pending tool correlation rejected: retired_step")
	}
	if _, retired := state.RetiredToolCallKeys[key]; retired {
		return state, fmt.Errorf("pending tool correlation rejected: retired_key")
	}
	if _, exists := state.PendingToolCalls[key]; !exists && !isAntigravityStepKey(key) && len(state.RetiredToolCallKeys) >= maxRetiredToolCallKeys {
		return state, fmt.Errorf("pending tool correlation rejected: retired_key_capacity; PostInvocation is required to start a new replay generation")
	}
	updated := PutPendingToolCall(state, key, call)
	if state.EvidenceOverflow || !updated.EvidenceOverflow || updated.EvidenceOverflowReason != "pending_tool_calls" {
		return updated, nil
	}
	limit := updated.EvidenceOverflowLimit
	updated.EvidenceOverflow = state.EvidenceOverflow
	updated.EvidenceOverflowReason = state.EvidenceOverflowReason
	updated.EvidenceOverflowLimit = state.EvidenceOverflowLimit
	return updated, fmt.Errorf("pending tool correlation rejected: %s", limit)
}

func takePendingToolCall(state SessionState, key string, now time.Time) (SessionState, PendingToolCall, bool) {
	state = reapPendingToolCalls(state, now)
	if _, retired := state.RetiredToolCallKeys[key]; retired {
		return state, PendingToolCall{}, false
	}
	call, found := state.PendingToolCalls[key]
	if !found {
		if antigravityStepRetired(state, key) {
			return state, PendingToolCall{}, false
		}
		if isAntigravityStepKey(key) {
			state = putRetiredToolCallKey(state, key, now.UnixNano(), true)
		} else {
			state, _ = retireToolCallKey(state, key, now.UnixNano())
		}
		return state, PendingToolCall{}, false
	}
	var retired bool
	state, retired = retireToolCallKey(state, key, now.UnixNano())
	if !retired {
		return state, PendingToolCall{}, false
	}
	pending := make(map[string]PendingToolCall, len(state.PendingToolCalls)-1)
	for currentKey, currentCall := range state.PendingToolCalls {
		if currentKey != key {
			pending[currentKey] = currentCall
		}
	}
	state.PendingToolCalls = pending
	if len(pending) == 0 {
		state.PendingToolCalls = nil
	}
	return state, call, true
}

func reapPendingToolCalls(state SessionState, now time.Time) SessionState {
	if len(state.PendingToolCalls) == 0 {
		return state
	}
	reapable := false
	for key, call := range state.PendingToolCalls {
		if !pendingToolCallExpired(call, now) {
			continue
		}
		if isAntigravityStepKey(key) || len(state.RetiredToolCallKeys) < maxRetiredToolCallKeys {
			reapable = true
			break
		}
	}
	if !reapable {
		return state
	}
	pending := make(map[string]PendingToolCall, len(state.PendingToolCalls))
	for key, call := range state.PendingToolCalls {
		pending[key] = call
	}
	retired := make(map[string]int64, len(state.RetiredToolCallKeys)+len(pending))
	for key, retiredAt := range state.RetiredToolCallKeys {
		retired[key] = retiredAt
	}
	for key, call := range pending {
		if !pendingToolCallExpired(call, now) {
			continue
		}
		if isAntigravityStepKey(key) {
			state, _ = retireToolCallKey(state, key, now.UnixNano())
			delete(pending, key)
			continue
		}
		if _, exists := retired[key]; !exists && len(retired) >= maxRetiredToolCallKeys {
			continue
		}
		retired[key] = now.UnixNano()
		delete(pending, key)
	}
	if len(pending) == len(state.PendingToolCalls) {
		return state
	}
	state.PendingToolCalls = pending
	state.RetiredToolCallKeys = retired
	if len(pending) == 0 {
		state.PendingToolCalls = nil
	}
	return state
}

func clearPendingToolCalls(state SessionState) SessionState {
	state = promoteLegacyAntigravitySteps(state)
	state.PendingToolCalls = nil
	state.RetiredToolCallKeys = nil
	return state
}

func retireToolCallKey(state SessionState, key string, retiredAt int64) (SessionState, bool) {
	if isAntigravityStepKey(key) {
		state = advanceAntigravityStepHighWater(state, key)
		return state, true
	}
	if _, exists := state.RetiredToolCallKeys[key]; exists {
		return state, true
	}
	if len(state.RetiredToolCallKeys) >= maxRetiredToolCallKeys {
		return state, false
	}
	return putRetiredToolCallKey(state, key, retiredAt, true), true
}

func isAntigravityStepKey(key string) bool {
	_, ok := antigravityStepSequence(key)
	return ok
}

func antigravityStepSequence(key string) (uint64, bool) {
	value, ok := strings.CutPrefix(strings.TrimSpace(key), "step:")
	if !ok || value == "" {
		return 0, false
	}
	sequence, err := strconv.ParseUint(value, 10, 64)
	return sequence, err == nil
}

func antigravityStepRetired(state SessionState, key string) bool {
	sequence, ok := antigravityStepSequence(key)
	return ok && state.AntigravityStepHighWater != nil && sequence <= *state.AntigravityStepHighWater
}

func advanceAntigravityStepHighWater(state SessionState, key string) SessionState {
	sequence, ok := antigravityStepSequence(key)
	if !ok || state.AntigravityStepHighWater != nil && sequence <= *state.AntigravityStepHighWater {
		return state
	}
	state.AntigravityStepHighWater = new(sequence)
	return state
}

func promoteLegacyAntigravitySteps(state SessionState) SessionState {
	for key := range state.RetiredToolCallKeys {
		state = advanceAntigravityStepHighWater(state, key)
	}
	return state
}

func putRetiredToolCallKey(state SessionState, key string, retiredAt int64, clone bool) SessionState {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > maxToolUseIDBytes || retiredAt <= 0 {
		markEvidenceOverflowWithLimit(&state, "retired_tool_call_keys", "item_bytes")
		return state
	}
	if _, exists := state.RetiredToolCallKeys[key]; !exists && len(state.RetiredToolCallKeys) >= maxRetiredToolCallKeys {
		markEvidenceOverflowWithLimit(&state, "retired_tool_call_keys", "item_count")
		return state
	}
	if clone || state.RetiredToolCallKeys == nil {
		retired := make(map[string]int64, len(state.RetiredToolCallKeys)+1)
		for currentKey, currentTime := range state.RetiredToolCallKeys {
			retired[currentKey] = currentTime
		}
		state.RetiredToolCallKeys = retired
	}
	state.RetiredToolCallKeys[key] = retiredAt
	return state
}

func pendingToolCallExpired(call PendingToolCall, now time.Time) bool {
	if call.CreatedAtUnixNano <= 0 {
		return true
	}
	created := time.Unix(0, call.CreatedAtUnixNano)
	return now.Before(created) || !now.Before(created.Add(pendingToolCallLifetime))
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
	if existing, exists := state.PendingToolCalls[key]; exists {
		existing.CreatedAtUnixNano = call.CreatedAtUnixNano
		existingEncoded, existingErr := json.Marshal(existing)
		if existingErr == nil && string(existingEncoded) == string(encoded) {
			return state
		}
		markEvidenceOverflowWithLimit(&state, "pending_tool_calls", "correlation_conflict")
		return state
	}
	if len(state.PendingToolCalls) >= maxPendingToolCalls {
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
