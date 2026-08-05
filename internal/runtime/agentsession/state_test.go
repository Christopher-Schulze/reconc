package agentsession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func withStateRoot(t *testing.T) (string, string) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv(StateRootEnv, stateDir)
	t.Setenv("TMPDIR", t.TempDir())
	repo := t.TempDir()
	return stateDir, repo
}

func TestResolveRepoRootRejectsNonExistent(t *testing.T) {
	_, err := ResolveRepoRoot("/does/not/exist/anywhere")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestResolveRepoRootRejectsFile(t *testing.T) {
	f, err := os.CreateTemp("", "reconc-state-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()
	_, err = ResolveRepoRoot(f.Name())
	if err == nil {
		t.Fatal("expected error for file path (not a dir)")
	}
}

func TestResolveRepoRootCanonicalisesSymlink(t *testing.T) {
	// On macOS /tmp is a symlink to /private/tmp. Creating the temp
	// repo there and resolving should yield the /private/tmp form.
	repo := t.TempDir()
	resolved, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	realResolved, _ := filepath.EvalSymlinks(repo)
	if resolved != realResolved {
		t.Errorf("expected %q, got %q", realResolved, resolved)
	}
}

func TestInitializeSessionStateCreatesEmptyState(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "sess-001")
	if err != nil {
		t.Fatalf("InitializeSessionState: %v", err)
	}
	if state.SessionID != "sess-001" {
		t.Errorf("expected sess-001, got %s", state.SessionID)
	}
	if len(state.WritePaths) != 0 || len(state.ReadPaths) != 0 {
		t.Errorf("fresh state should be empty, got %+v", state)
	}
	// State file must now exist.
	path := sessionStatePath(state.RepoRoot, "sess-001")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("state file missing after init: %v", err)
	}
}

func TestLoadSessionStateRoundTrip(t *testing.T) {
	_, repo := withStateRoot(t)
	initial, err := InitializeSessionState(repo, "s1")
	if err != nil {
		t.Fatal(err)
	}
	initial = AppendReadPath(initial, "docs/x.md")
	initial = RecordWriteEvent(initial, []string{"src/a.go"})
	initial = AppendCommand(initial, "go test ./...")
	initial = AppendCommandResult(initial, CommandResult{Command: "go test ./...", Outcome: "success", EvidenceEpoch: initial.EvidenceEpoch})
	initial = AppendClaim(initial, "ci-green")
	if err := SaveSessionState(initial); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSessionState(repo, "s1")
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if len(loaded.WritePaths) != 1 || loaded.WritePaths[0] != "src/a.go" {
		t.Errorf("WritePaths roundtrip failed: %v", loaded.WritePaths)
	}
	if loaded.WriteEpochs["src/a.go"] != 1 || loaded.CommandResults[0].EvidenceEpoch != 1 {
		t.Fatalf("causal evidence roundtrip failed: %+v", loaded)
	}
	if len(loaded.Claims) != 1 || loaded.Claims[0] != "ci-green" {
		t.Errorf("Claims roundtrip failed: %v", loaded.Claims)
	}
}

func TestLoadLegacyStateInvalidatesUnorderedCommandSuccess(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	legacy := emptyState(root, "legacy")
	legacy.WritePaths = []string{"src/a.go"}
	legacy.CommandResults = []CommandResult{{Command: "go test ./...", Outcome: "success"}}
	body, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	path := sessionStatePath(root, "legacy")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSessionState(repo, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WriteEpochs["src/a.go"] != 1 || loaded.CommandResults[0].EvidenceEpoch != 0 {
		t.Fatalf("legacy evidence did not fail closed: %+v", loaded)
	}
}

func TestMutateSessionStateMergesConcurrentUpdates(t *testing.T) {
	_, repo := withStateRoot(t)
	if _, err := InitializeSessionState(repo, "parallel"); err != nil {
		t.Fatal(err)
	}

	const workers = 40
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := MutateSessionState(repo, "parallel", func(state SessionState) SessionState {
				return AppendReadPath(state, fmt.Sprintf("docs/read-%02d.md", i))
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	state, err := LoadSessionState(repo, "parallel")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ReadPaths) != workers {
		t.Fatalf("expected %d merged read paths, got %d: %v", workers, len(state.ReadPaths), state.ReadPaths)
	}
}

func TestLoadSessionStateWaitsForSessionLock(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "locked-read")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	returned := make(chan error, 1)
	err = withSessionLock(state.RepoRoot, state.SessionID, func() error {
		go func() {
			close(started)
			_, loadErr := LoadSessionState(repo, state.SessionID)
			returned <- loadErr
		}()
		<-started
		select {
		case loadErr := <-returned:
			return fmt.Errorf("load returned while session lock was held: %w", loadErr)
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case loadErr := <-returned:
		if loadErr != nil {
			t.Fatal(loadErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("load did not resume after session lock release")
	}
}

func TestActiveSessionWriteWaitsForPointerLock(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "active-lock")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	returned := make(chan error, 1)
	err = withActiveSessionLock(state.RepoRoot, func() error {
		go func() {
			close(started)
			returned <- writeActiveSession(state.RepoRoot, "next-session")
		}()
		<-started
		select {
		case writeErr := <-returned:
			return fmt.Errorf("write returned while active-session lock was held: %w", writeErr)
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case writeErr := <-returned:
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write did not resume after active-session lock release")
	}
}

func TestSaveSessionStateUsesRaceSafeTempFiles(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "save-parallel")
	if err != nil {
		t.Fatal(err)
	}

	const workers = 25
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			copyState := state
			copyState.ReadPaths = []string{fmt.Sprintf("docs/read-%02d.md", i)}
			if err := SaveSessionState(copyState); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	if _, err := LoadSessionState(repo, "save-parallel"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSessionStateMissingReturnsEmpty(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := LoadSessionState(repo, "never-existed")
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionID != "never-existed" {
		t.Errorf("expected echoed session_id, got %s", state.SessionID)
	}
	if len(state.WritePaths) != 0 {
		t.Errorf("empty-state should have no writes, got %v", state.WritePaths)
	}
}

func TestLoadSessionStateRejectsMalformedJSON(t *testing.T) {
	_, repo := withStateRoot(t)
	root, _ := ResolveRepoRoot(repo)
	path := sessionStatePath(root, "bad")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSessionState(repo, "bad")
	if err == nil {
		t.Fatal("expected error for malformed state file")
	}
}

func TestLoadSessionStateRejectsIdentityAndReportPathDrift(t *testing.T) {
	for name, mutate := range map[string]func(*SessionState){
		"session": func(state *SessionState) { state.SessionID = "other" },
		"repo":    func(state *SessionState) { state.RepoRoot = t.TempDir() },
		"report":  func(state *SessionState) { state.ReportPath = filepath.Join(t.TempDir(), "foreign.json") },
	} {
		t.Run(name, func(t *testing.T) {
			_, repo := withStateRoot(t)
			root, _ := ResolveRepoRoot(repo)
			state := emptyState(root, "identity")
			mutate(&state)
			body, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			path := sessionStatePath(root, "identity")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadSessionState(repo, "identity"); err == nil {
				t.Fatalf("expected %s drift to fail closed", name)
			}
		})
	}
}

func TestAppendUniqueDeduplicates(t *testing.T) {
	state := emptyState("/x", "s1")
	state = AppendReadPath(state, "a.go")
	state = AppendReadPath(state, "a.go")
	state = AppendReadPath(state, "b.go")
	state = AppendReadPath(state, "  a.go  ")
	if len(state.ReadPaths) != 3 {
		t.Errorf("expected 3 byte-distinct paths, got %v", state.ReadPaths)
	}
}

func TestAppendEmptyStringIgnored(t *testing.T) {
	state := emptyState("/x", "s1")
	state = AppendReadPath(state, "")
	state = AppendReadPath(state, "   ")
	if len(state.ReadPaths) != 1 || state.ReadPaths[0] != "   " {
		t.Errorf("only the empty path should be ignored, got %v", state.ReadPaths)
	}
}

func TestPathIdentitySurvivesStateNormalization(t *testing.T) {
	state := emptyState("/x", "identity")
	for _, path := range []string{" leading.go", "trailing.go ", `literal\\backslash.go`, "   "} {
		state = RecordWriteEvent(state, []string{path})
	}
	normalized := normalizeSessionState(state)
	for _, path := range []string{" leading.go", "trailing.go ", `literal\\backslash.go`, "   "} {
		if !containsString(normalized.WritePaths, path) {
			t.Fatalf("normalized paths lost %q: %v", path, normalized.WritePaths)
		}
		if normalized.WriteEpochs[path] == 0 {
			t.Fatalf("normalized write epoch lost %q: %v", path, normalized.WriteEpochs)
		}
	}
}

func TestSessionAndActivePointerSkipIdenticalWrites(t *testing.T) {
	_, repo := withStateRoot(t)
	state, err := InitializeSessionState(repo, "unchanged")
	if err != nil {
		t.Fatal(err)
	}
	written, err := saveSessionStateLockedIfChanged(state)
	if err != nil || written {
		t.Fatalf("unchanged state write: written=%v err=%v", written, err)
	}
	written, err = writeActiveSessionIfChanged(state.RepoRoot, state.SessionID)
	if err != nil || written {
		t.Fatalf("unchanged active pointer write: written=%v err=%v", written, err)
	}
	state = AppendReadPath(state, "docs/new.md")
	written, err = saveSessionStateLockedIfChanged(state)
	if err != nil || !written {
		t.Fatalf("changed state write: written=%v err=%v", written, err)
	}
}

func TestEvidenceCollectionsAreDeduplicatedAndBounded(t *testing.T) {
	state := emptyState("/repo", "bounded")
	result := CommandResult{Command: "go test ./...", Outcome: "success", ToolUseID: "tool-1"}
	state = AppendCommandResult(state, result)
	state = AppendCommandResult(state, result)
	if len(state.CommandResults) != 1 {
		t.Fatalf("repeated command result was not deduplicated: %d", len(state.CommandResults))
	}
	for index := 0; index <= maxPathEvidenceItems; index++ {
		state = AppendWritePath(state, fmt.Sprintf("src/generated-%04d.go", index))
	}
	if !state.EvidenceOverflow || state.EvidenceOverflowReason != "write_paths" {
		t.Fatalf("missing fail-closed overflow marker: %+v", state)
	}
	if len(state.WritePaths) > maxPathEvidenceItems {
		t.Fatalf("write path item cap escaped: %d", len(state.WritePaths))
	}
	body, err := marshalStateDeterministic(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > MaxSessionStateBytes {
		t.Fatalf("session byte cap escaped: %d", len(body))
	}
}

func TestCommandResultByteBudgetFailsClosedWithIncrementalAccounting(t *testing.T) {
	state := emptyState("/repo", "byte-budget")
	// Each result encodes to roughly 2 KiB, so the 256 KiB budget is hit
	// long before the 512-item cap.
	command := strings.Repeat("go test -run ", 160)
	appended := 0
	for index := range maxCommandResultItems {
		before := state.CommandResultBytes
		state = AppendCommandResult(state, CommandResult{
			Command:   fmt.Sprintf("%s-%04d", command, index),
			Outcome:   "success",
			ToolUseID: fmt.Sprintf("tool-%04d", index),
		})
		if state.EvidenceOverflow {
			if state.EvidenceOverflowReason != "command_results" || state.EvidenceOverflowLimit != "byte_budget" {
				t.Fatalf("overflow marker = %s/%s, want command_results/byte_budget", state.EvidenceOverflowReason, state.EvidenceOverflowLimit)
			}
			break
		}
		appended++
		if state.CommandResultBytes <= before {
			t.Fatalf("byte counter did not advance on append %d: %d -> %d", index, before, state.CommandResultBytes)
		}
	}
	if appended == 0 || appended >= maxCommandResultItems {
		t.Fatalf("appended %d results, want a budget-driven stop well below the item cap", appended)
	}
	if !state.EvidenceOverflow {
		t.Fatal("byte budget never tripped the fail-closed overflow marker")
	}
	want := int64(0)
	for _, result := range state.CommandResults {
		want += int64(commandResultEncodedBytes(result))
	}
	if state.CommandResultBytes != want {
		t.Fatalf("persisted byte counter %d drifted from encoded total %d", state.CommandResultBytes, want)
	}
}

func TestLegacyStateBackfillsCommandResultByteCounter(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	legacy := emptyState(root, "backfill")
	legacy.CommandResults = []CommandResult{
		{Command: "go test ./...", Outcome: "success"},
		{Command: "make lint", Outcome: "failure"},
	}
	body, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	path := sessionStatePath(root, legacy.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSessionState(root, legacy.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := int64(0)
	for _, result := range loaded.CommandResults {
		want += int64(commandResultEncodedBytes(result))
	}
	if loaded.CommandResultBytes != want {
		t.Fatalf("backfilled byte counter = %d, want %d", loaded.CommandResultBytes, want)
	}
}

func TestLegacyOversizedCollectionsCompactOnLoad(t *testing.T) {
	_, repo := withStateRoot(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	legacy := emptyState(root, "legacy")
	for index := 0; index < maxPathEvidenceItems+500; index++ {
		legacy.ReadPaths = append(legacy.ReadPaths, fmt.Sprintf("docs/legacy-%04d.md", index))
	}
	body, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	path := sessionStatePath(root, legacy.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSessionState(root, legacy.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.EvidenceOverflow || len(loaded.ReadPaths) > maxPathEvidenceItems {
		t.Fatalf("legacy state was not safely compacted: overflow=%v reads=%d", loaded.EvidenceOverflow, len(loaded.ReadPaths))
	}
}

func TestPendingToolCallsAreBounded(t *testing.T) {
	state := emptyState("/repo", "pending")
	for index := 0; index <= maxPendingToolCalls; index++ {
		state = PutPendingToolCall(state, fmt.Sprintf("call-%03d", index), PendingToolCall{ToolName: "Read", ToolInput: map[string]interface{}{"file_path": "docs/x.md"}})
	}
	if len(state.PendingToolCalls) != maxPendingToolCalls || !state.EvidenceOverflow {
		t.Fatalf("pending calls escaped bound: count=%d overflow=%v", len(state.PendingToolCalls), state.EvidenceOverflow)
	}
}

func TestActiveSessionPointerTracksLatest(t *testing.T) {
	_, repo := withStateRoot(t)
	_, _ = InitializeSessionState(repo, "sess-A")
	_, _ = InitializeSessionState(repo, "sess-B")
	active, err := ResolveActiveSessionID(repo)
	if err != nil {
		t.Fatal(err)
	}
	if active != "sess-B" {
		t.Errorf("expected sess-B as active, got %s", active)
	}
}

func TestCleanupSessionStateRemovesFileAndPointer(t *testing.T) {
	_, repo := withStateRoot(t)
	_, _ = InitializeSessionState(repo, "sess-A")
	if err := CleanupSessionState(repo, "sess-A"); err != nil {
		t.Fatal(err)
	}
	root, _ := ResolveRepoRoot(repo)
	if _, err := os.Stat(sessionStatePath(root, "sess-A")); !os.IsNotExist(err) {
		t.Errorf("state file should be gone, stat err: %v", err)
	}
	if _, err := os.Stat(activeSessionPath(root)); !os.IsNotExist(err) {
		t.Errorf("active pointer should be gone, stat err: %v", err)
	}
}

func TestSanitiseIDScrubsUnsafeChars(t *testing.T) {
	cases := map[string]string{
		"uuid-1234": "uuid-1234",
		"../escape": "___escape",
		"a/b\\c":    "a_b_c",
		"":          "unknown",
		"a b":       "a_b",
	}
	for in, want := range cases {
		if got := sanitiseID(in); got != want {
			t.Errorf("sanitiseID(%q) = %q, want %q", in, got, want)
		}
	}
}

func BenchmarkDuplicateSessionMutation(b *testing.B) {
	stateRoot := b.TempDir()
	b.Setenv(StateRootEnv, stateRoot)
	b.Setenv("TMPDIR", b.TempDir())
	repo := b.TempDir()
	if _, err := InitializeSessionState(repo, "duplicate"); err != nil {
		b.Fatal(err)
	}
	if _, err := MutateSessionState(repo, "duplicate", func(state SessionState) SessionState {
		return AppendReadPath(state, "docs/documentation.md")
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := MutateSessionState(repo, "duplicate", func(state SessionState) SessionState {
			return AppendReadPath(state, "docs/documentation.md")
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestProjectKeyDeterministic(t *testing.T) {
	k1 := projectKey("/repo/a")
	k2 := projectKey("/repo/a")
	k3 := projectKey("/repo/b")
	if k1 != k2 {
		t.Error("same path must hash identically")
	}
	if k1 == k3 {
		t.Error("different paths must hash differently")
	}
	if len(k1) != 16 {
		t.Errorf("project key should be 16 chars, got %d", len(k1))
	}
}
