package agentsession

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeSessionStatePreservesCollectionSemantics(t *testing.T) {
	exitCode := 0
	longPath := strings.Repeat("x", maxPathBytes+1)
	state := SessionState{
		ReadPaths:  []string{"z.go", "a.go", "z.go", "", " spaced.go "},
		WritePaths: []string{"z.go", "a.go", "z.go", "", " spaced.go ", longPath},
		WriteEpochs: map[string]uint64{
			"a.go":        3,
			"z.go":        7,
			longPath:      13,
			"spare-entry": 11,
		},
		Commands: []string{" git status ", "echo ok", "git status", "   "},
		Claims:   []string{" ci-green ", "ci-green", "   "},
		CommandResults: []CommandResult{
			{Command: " git status ", Outcome: "success", EvidenceEpoch: 2, ToolUseID: "call-1", ExitCode: &exitCode},
			{Command: "git status", Outcome: "success", EvidenceEpoch: 2, ToolUseID: "call-1", ExitCode: &exitCode},
			{Command: "echo ok", Outcome: "failure", EvidenceEpoch: 3, ToolUseID: "call-2"},
		},
		CommandResultBytes: 999999,
		PendingToolCalls: map[string]PendingToolCall{
			"call-b": {ToolName: " Read ", ToolUseID: " call-b "},
			"call-a": {ToolName: "Write", ToolUseID: "call-a"},
		},
		EvidenceOverflow:       true,
		EvidenceOverflowReason: "legacy-field",
		EvidenceOverflowLimit:  "item_count",
	}

	normalized := normalizeSessionState(state)
	if got, want := normalized.ReadPaths, []string{" spaced.go ", "a.go", "z.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadPaths = %#v, want %#v", got, want)
	}
	if got, want := normalized.WritePaths, []string{" spaced.go ", "a.go", "z.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("WritePaths = %#v, want %#v", got, want)
	}
	if got, want := normalized.WriteEpochs, map[string]uint64{"a.go": 3, "z.go": 7, longPath: 13}; !reflect.DeepEqual(got, want) {
		t.Fatalf("WriteEpochs = %#v, want %#v", got, want)
	}
	if got, want := normalized.Commands, []string{"echo ok", "git status"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Commands = %#v, want %#v", got, want)
	}
	if got, want := normalized.Claims, []string{"ci-green"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Claims = %#v, want %#v", got, want)
	}
	if len(normalized.CommandResults) != 2 || normalized.CommandResults[0].Command != "git status" || normalized.CommandResults[1].Command != "echo ok" {
		t.Fatalf("CommandResults = %#v, want normalized order and deduplication", normalized.CommandResults)
	}
	wantResultBytes := int64(0)
	for _, result := range normalized.CommandResults {
		wantResultBytes += int64(commandResultEncodedBytes(result))
	}
	if normalized.CommandResultBytes != wantResultBytes {
		t.Fatalf("CommandResultBytes = %d, want %d", normalized.CommandResultBytes, wantResultBytes)
	}
	if got, want := normalized.PendingToolCalls["call-b"].ToolName, "Read"; got != want {
		t.Fatalf("pending call tool name = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(normalized.PendingToolCalls["call-b"].ToolInput, map[string]interface{}(nil)) {
		t.Fatalf("pending call input changed unexpectedly: %#v", normalized.PendingToolCalls["call-b"].ToolInput)
	}
	if !normalized.EvidenceOverflow || normalized.EvidenceOverflowReason != "write_paths" || normalized.EvidenceOverflowLimit != "item_bytes" {
		t.Fatalf("overflow marker changed: %+v", normalized)
	}
}

func TestNormalizeSessionStateRecomputesCommandResultBytes(t *testing.T) {
	state := emptyState("/repo", "normalization-bytes")
	state.CommandResults = []CommandResult{
		{Command: " first ", Outcome: "success"},
		{Command: "second", Outcome: "failure"},
	}
	state.CommandResultBytes = 1
	normalized := normalizeSessionState(state)
	want := int64(0)
	for _, result := range normalized.CommandResults {
		body, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		want += int64(len(body))
	}
	if normalized.CommandResultBytes != want {
		t.Fatalf("normalized command-result bytes = %d, want %d", normalized.CommandResultBytes, want)
	}
}

func TestSessionStateNormalizationAdmissionRequiresCanonicalCollections(t *testing.T) {
	state := normalizeSessionState(emptyState("/repo", "admission"))
	if !sessionStateIsNormalized(state) {
		t.Fatal("normalized empty state was rejected by admission")
	}
	state = AppendReadPath(state, "z.go")
	state = AppendReadPath(state, "a.go")
	if !sessionStateIsNormalized(state) {
		t.Fatal("bounded path mutators did not preserve canonical ordering")
	}
	state.Commands = append(state.Commands, "  git status  ")
	if sessionStateIsNormalized(state) {
		t.Fatal("untrusted non-canonical command was admitted")
	}
	canonical := normalizeSessionState(state)
	if !sessionStateIsNormalized(canonical) || len(canonical.Commands) != 1 || canonical.Commands[0] != "git status" {
		t.Fatalf("canonical repair = %+v", canonical)
	}
	canonical.EvidenceOverflowReason = "stale-marker"
	if sessionStateIsNormalized(canonical) {
		t.Fatal("orphaned overflow marker was admitted")
	}
}

func TestNormalizeSessionStateIsIdempotentAtAdmissionBoundary(t *testing.T) {
	exitCode := 0
	inputs := []SessionState{
		emptyState("/repo", "idempotent-empty"),
		maximumNormalizationState(),
		{
			ReadPaths:        []string{"z.go", "a.go", "a.go"},
			WritePaths:       []string{"src/out.go"},
			WriteEpochs:      map[string]uint64{"src/out.go": 4, "stale.go": 2},
			Commands:         []string{"  go test  ", "go test"},
			CommandResults:   []CommandResult{{Command: " cmd ", Outcome: "success", ExitCode: &exitCode}},
			PendingToolCalls: map[string]PendingToolCall{" call ": {ToolName: " Read "}},
		},
	}
	for index, input := range inputs {
		first := normalizeSessionState(input)
		second := normalizeSessionState(first)
		if !reflect.DeepEqual(second, first) {
			t.Fatalf("input %d changed after second normalization: first=%+v second=%+v", index, first, second)
		}
		if !sessionStateIsNormalized(first) {
			t.Fatalf("input %d normalized state was not admitted: %+v", index, first)
		}
	}
}

func TestPendingToolCallExpiryReclaimsCapacity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	state := emptyState("/repo", "pending-expiry")
	state.PendingToolCalls = make(map[string]PendingToolCall, maxPendingToolCalls)
	for index := 0; index < maxPendingToolCalls; index++ {
		key := fmt.Sprintf("stale-%02d", index)
		state.PendingToolCalls[key] = PendingToolCall{
			ToolName: "Read", ToolUseID: key,
			CreatedAtUnixNano: now.Add(-pendingToolCallLifetime).UnixNano(),
		}
	}
	state = reapPendingToolCalls(state, now)
	if state.PendingToolCalls != nil {
		t.Fatalf("expired host-crash correlations survived: %v", state.PendingToolCalls)
	}
	if len(state.RetiredToolCallKeys) != maxPendingToolCalls {
		t.Fatalf("expired identities were not retained as tombstones: %v", state.RetiredToolCallKeys)
	}
	if reused, err := putPendingToolCallTransient(state, "stale-00", PendingToolCall{
		ToolName: "Write", ToolUseID: "stale-00", CreatedAtUnixNano: now.UnixNano(),
	}); err == nil || len(reused.PendingToolCalls) != 0 {
		t.Fatalf("retired identity was reused: err=%v state=%+v", err, reused)
	}
	state = PutPendingToolCall(state, "fresh", PendingToolCall{
		ToolName: "Read", ToolUseID: "fresh", CreatedAtUnixNano: now.UnixNano(),
	})
	if state.EvidenceOverflow || len(state.PendingToolCalls) != 1 {
		t.Fatalf("reclaimed correlation capacity = %+v", state)
	}
	future := emptyState("/repo", "future-pending")
	future.PendingToolCalls = map[string]PendingToolCall{"future": {
		ToolName: "Read", ToolUseID: "fresh", CreatedAtUnixNano: now.Add(time.Second).UnixNano(),
	}}
	if reaped := reapPendingToolCalls(future, now); reaped.PendingToolCalls != nil {
		t.Fatalf("future-dated correlation survived fail-closed reaping: %v", reaped.PendingToolCalls)
	}
}

func TestPendingToolCallIdentityConflictPreservesOriginal(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	state := PutPendingToolCall(emptyState("/repo", "pending-conflict"), "step:1", PendingToolCall{
		ToolName: "Read", ToolUseID: "step:1", ToolInput: map[string]interface{}{"file_path": "first.go"},
		CreatedAtUnixNano: now.UnixNano(),
	})
	state = PutPendingToolCall(state, "step:1", PendingToolCall{
		ToolName: "Write", ToolUseID: "step:1", ToolInput: map[string]interface{}{"file_path": "second.go"},
		CreatedAtUnixNano: now.Add(time.Second).UnixNano(),
	})
	if !state.EvidenceOverflow || state.EvidenceOverflowReason != "pending_tool_calls" || state.EvidenceOverflowLimit != "correlation_conflict" {
		t.Fatalf("identity conflict did not fail closed: %+v", state)
	}
	if got := state.PendingToolCalls["step:1"]; got.ToolName != "Read" || got.ToolInput["file_path"] != "first.go" {
		t.Fatalf("identity conflict replaced original correlation: %+v", got)
	}
}

func TestPendingToolCallCapacityFailureIsTransient(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	state := emptyState("/repo", "pending-capacity")
	for index := 0; index < maxPendingToolCalls; index++ {
		key := fmt.Sprintf("live-%02d", index)
		state = PutPendingToolCall(state, key, PendingToolCall{
			ToolName: "Read", ToolUseID: key, CreatedAtUnixNano: now.UnixNano(),
		})
	}
	updated, err := putPendingToolCallTransient(state, "overflow", PendingToolCall{
		ToolName: "Read", ToolUseID: "overflow", CreatedAtUnixNano: now.UnixNano(),
	})
	if err == nil || updated.EvidenceOverflow || len(updated.PendingToolCalls) != maxPendingToolCalls {
		t.Fatalf("transient capacity failure = err=%v state=%+v", err, updated)
	}
}

func TestPendingToolCallRetiredCapacityFailsClosedWithoutTaint(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	state := emptyState("/repo", "retired-capacity")
	state.RetiredToolCallKeys = make(map[string]int64, maxRetiredToolCallKeys)
	for index := 0; index < maxRetiredToolCallKeys; index++ {
		state.RetiredToolCallKeys[fmt.Sprintf("retired-%03d", index)] = now.UnixNano()
	}
	updated, err := putPendingToolCallTransient(state, "new-call", PendingToolCall{
		ToolName: "Read", ToolUseID: "new-call", CreatedAtUnixNano: now.UnixNano(),
	})
	if err == nil || updated.EvidenceOverflow || len(updated.PendingToolCalls) != 0 {
		t.Fatalf("retired capacity failure = err=%v state=%+v", err, updated)
	}
}

func TestAntigravityStepHighWaterExceedsRetiredKeyCapacity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	state := emptyState("/repo", "antigravity-high-water")
	for index := 0; index < maxRetiredToolCallKeys+64; index++ {
		key := fmt.Sprintf("step:%d", index)
		var err error
		state, err = putPendingToolCallTransient(state, key, PendingToolCall{
			ToolName: "Read", ToolUseID: key, CreatedAtUnixNano: now.UnixNano(),
		})
		if err != nil {
			t.Fatalf("step %d pre rejected: %v", index, err)
		}
		var found bool
		state, _, found = takePendingToolCall(state, key, now)
		if !found {
			t.Fatalf("step %d post was not associated", index)
		}
	}
	if state.AntigravityStepHighWater == nil || *state.AntigravityStepHighWater != maxRetiredToolCallKeys+63 {
		t.Fatalf("step high-water=%v", state.AntigravityStepHighWater)
	}
	if len(state.RetiredToolCallKeys) != 0 {
		t.Fatalf("step sequence consumed fallback tombstones: %v", state.RetiredToolCallKeys)
	}
	if _, err := putPendingToolCallTransient(state, "step:1", PendingToolCall{ToolName: "Read", ToolUseID: "step:1", CreatedAtUnixNano: now.UnixNano()}); err == nil {
		t.Fatal("replayed step was accepted below high-water")
	}
}

func TestAntigravityLegacyRetiredStepsPromoteAtInvocationBoundary(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	state := emptyState("/repo", "antigravity-legacy")
	state.RetiredToolCallKeys = map[string]int64{"step:7": now.UnixNano(), "step:9": now.UnixNano()}
	if _, err := putPendingToolCallTransient(state, "step:8", PendingToolCall{ToolName: "Read", ToolUseID: "step:8", CreatedAtUnixNano: now.UnixNano()}); err != nil {
		t.Fatalf("legacy exact tombstones blocked a valid gap before rollover: %v", err)
	}
	state = clearPendingToolCalls(state)
	if state.AntigravityStepHighWater == nil || *state.AntigravityStepHighWater != 9 || len(state.RetiredToolCallKeys) != 0 {
		t.Fatalf("legacy rollover did not promote sequence state: %+v", state)
	}
	if _, err := putPendingToolCallTransient(state, "step:8", PendingToolCall{ToolName: "Read", ToolUseID: "step:8", CreatedAtUnixNano: now.UnixNano()}); err == nil {
		t.Fatal("step below promoted high-water was accepted")
	}
}

func TestAntigravityUnknownStepDoesNotAdvanceHighWater(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	state := emptyState("/repo", "antigravity-unknown")
	state, _, found := takePendingToolCall(state, "step:18446744073709551615", now)
	if found || state.AntigravityStepHighWater != nil {
		t.Fatalf("unknown step advanced replay high-water: found=%v state=%+v", found, state)
	}
	if _, retired := state.RetiredToolCallKeys["step:18446744073709551615"]; !retired {
		t.Fatalf("unknown step was not retained as a replay tombstone: %+v", state.RetiredToolCallKeys)
	}
}

func TestNormalizeSessionStateIsSafeForConcurrentCallers(t *testing.T) {
	state := maximumNormalizationState()
	const callers = 16
	results := make(chan SessionState, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			results <- normalizeSessionState(state)
		}()
	}
	wait.Wait()
	close(results)
	var first SessionState
	for result := range results {
		if first.SessionID == "" {
			first = result
			continue
		}
		if !reflect.DeepEqual(result, first) {
			t.Fatal("concurrent normalization produced different states")
		}
	}
}

func BenchmarkNormalizeSessionStateCollections(b *testing.B) {
	for _, size := range []int{64, maxPathEvidenceItems} {
		b.Run(fmt.Sprintf("paths-%d", size), func(b *testing.B) {
			state := normalizationStateWithPathCount(size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = normalizeSessionState(state)
			}
		})
	}
}

func BenchmarkNormalizeSessionStateCanonicalAdmission(b *testing.B) {
	state := normalizeSessionState(maximumNormalizationState())
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		state = normalizeSessionState(state)
	}
}

func maximumNormalizationState() SessionState {
	state := emptyState("/repo", "maximum-normalization")
	state.ReadPaths = make([]string, 0, maxPathEvidenceItems)
	state.WritePaths = make([]string, 0, maxPathEvidenceItems)
	state.WriteEpochs = make(map[string]uint64, maxPathEvidenceItems)
	state.Commands = make([]string, 0, maxCommandEvidenceItems)
	state.Claims = make([]string, 0, maxClaimEvidenceItems)
	state.CommandResults = make([]CommandResult, 0, maxCommandResultItems)
	state.PendingToolCalls = make(map[string]PendingToolCall, maxPendingToolCalls)
	for index := 0; index < maxPathEvidenceItems; index++ {
		path := fmt.Sprintf("src/file-%04d.go", maxPathEvidenceItems-index-1)
		state.ReadPaths = append(state.ReadPaths, path)
		state.WritePaths = append(state.WritePaths, path)
		state.WriteEpochs[path] = uint64(index + 1)
	}
	for index := 0; index < maxCommandEvidenceItems; index++ {
		state.Commands = append(state.Commands, fmt.Sprintf("go test ./pkg-%04d", maxCommandEvidenceItems-index-1))
	}
	for index := 0; index < maxClaimEvidenceItems; index++ {
		state.Claims = append(state.Claims, fmt.Sprintf("claim-%04d", maxClaimEvidenceItems-index-1))
	}
	for index := 0; index < maxCommandResultItems; index++ {
		state.CommandResults = append(state.CommandResults, CommandResult{
			Command: fmt.Sprintf("go test ./pkg-%04d", index), Outcome: "success", EvidenceEpoch: uint64(index + 1), ToolUseID: fmt.Sprintf("call-%04d", index),
		})
	}
	for index := 0; index < maxPendingToolCalls; index++ {
		key := fmt.Sprintf("call-%04d", maxPendingToolCalls-index-1)
		state.PendingToolCalls[key] = PendingToolCall{ToolName: "Read", ToolUseID: key}
	}
	return state
}

func normalizationStateWithPathCount(count int) SessionState {
	state := emptyState("/repo", fmt.Sprintf("normalization-%d", count))
	state.ReadPaths = make([]string, 0, count)
	for index := 0; index < count; index++ {
		state.ReadPaths = append(state.ReadPaths, fmt.Sprintf("src/file-%04d.go", count-index-1))
	}
	return state
}
