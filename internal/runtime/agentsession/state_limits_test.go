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
