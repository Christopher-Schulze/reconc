package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime"
)

func BenchmarkVerifiedEvidencePrefix(b *testing.B) {
	_, repo := withBenchmarkStateRoot(b)
	const sessionID = "segment-benchmark"
	if _, err := InitializeSessionState(repo, sessionID); err != nil {
		b.Fatal(err)
	}
	for segment := 0; segment < 8; segment++ {
		if _, err := MutateSessionState(repo, sessionID, func(state SessionState) SessionState {
			for index := 0; index < maxCommandEvidenceItems; index++ {
				state = AppendCommand(state, fmt.Sprintf("command-%02d-%04d", segment, index))
			}
			return state
		}); err != nil {
			b.Fatal(err)
		}
	}
	state, err := LoadSessionState(repo, sessionID)
	if err != nil {
		b.Fatal(err)
	}
	cache := NewStopDecisionCache()
	if _, err := loadCompleteSessionEvidenceWithCache(state.RepoRoot, state, cache); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := loadCompleteSessionEvidenceWithCache(state.RepoRoot, state, cache); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWorkerPreHookVerifiedEvidencePrefix(b *testing.B) {
	repo := setupStopBenchmarkRepo(b)
	state := writeEvidenceChainFixture(b, repo, "worker-prefix-benchmark", 16, 128)
	if err := SaveSessionState(state); err != nil {
		b.Fatal(err)
	}
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		b.Fatal(err)
	}
	payload := []byte(`{"session_id":"worker-prefix-benchmark","tool_use_id":"benchmark-call","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`)
	evaluator := runtime.NewEvaluator()
	warmCache := NewStopDecisionCache()
	if result := RunHookRequestWithEvaluatorAndStopCache(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload, evaluator, warmCache); result.ExitCode != 0 {
		b.Fatalf("warm-up pre-hook failed: %+v", result)
	}
	b.Run("cold-prefix", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			cache := NewStopDecisionCache()
			if result := RunHookRequestWithEvaluatorAndStopCache(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload, evaluator, cache); result.ExitCode != 0 {
				b.Fatalf("cold worker pre-hook failed: %+v", result)
			}
		}
	})
	b.Run("warm-prefix", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if result := RunHookRequestWithEvaluatorAndStopCache(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload, evaluator, warmCache); result.ExitCode != 0 {
				b.Fatalf("warm worker pre-hook failed: %+v", result)
			}
		}
	})
}

// BenchmarkOneShotUniquePreHook measures what a host pays on every new tool
// call: a one-shot hook process without a worker cache and a tool_use_id the
// decision cache has never seen, over a sealed chain of maximal segments.
func BenchmarkOneShotUniquePreHook(b *testing.B) {
	repo := setupStopBenchmarkRepo(b)
	state := writeAggregateEvidenceChainFixture(b, repo, "one-shot-unique", 16)
	if err := SaveSessionState(state); err != nil {
		b.Fatal(err)
	}
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for index := range b.N {
		payload := []byte(fmt.Sprintf(`{"session_id":"one-shot-unique","tool_use_id":"call-%d","tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`, index))
		if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 0 {
			b.Fatalf("one-shot pre-hook failed: %+v", result)
		}
	}
}

func BenchmarkStopLoadedEvidenceAttempt(b *testing.B) {
	_, repo := withBenchmarkStateRoot(b)
	state := writeEvidenceChainFixture(b, repo, "attempt-evidence-benchmark", 4, 4*1024)
	var chainLoads int
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		loaded := &stopLoadedEvidence{}
		first, _, err := loaded.load(state.RepoRoot, state, nil)
		if err != nil {
			b.Fatal(err)
		}
		second, _, err := loaded.load(state.RepoRoot, state, nil)
		if err != nil || len(second.Commands) != len(first.Commands) {
			b.Fatalf("reused evidence commands=%d/%d err=%v", len(first.Commands), len(second.Commands), err)
		}
		chainLoads += loaded.chainLoads
	}
	b.ReportMetric(float64(chainLoads)/float64(b.N), "chain-load/op")
}

func BenchmarkPackedRefLookup(b *testing.B) {
	root := b.TempDir()
	var body strings.Builder
	for index := 0; index < 20_000; index++ {
		fmt.Fprintf(&body, "%040x refs/heads/branch-%05d\n", index, index)
	}
	const target = "refs/heads/branch-19999"
	if err := os.WriteFile(filepath.Join(root, "packed-refs"), []byte(body.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, found, err := readPackedGitRef([]string{root}, target); err != nil || !found {
			b.Fatalf("packed ref found=%t err=%v", found, err)
		}
	}
}

func BenchmarkRecordWriteEventLargeState(b *testing.B) {
	state := SessionState{WriteEpochs: make(map[string]uint64)}
	for index := 0; index < 1_024; index++ {
		state.WritePaths = append(state.WritePaths, fmt.Sprintf("src/file-%04d.go", index))
	}
	paths := make([]string, 128)
	for index := range paths {
		paths[index] = fmt.Sprintf("src/file-%04d.go", 896+index)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = RecordWriteEvent(state, paths)
	}
}

func withBenchmarkStateRoot(b *testing.B) (string, string) {
	b.Helper()
	stateDir := b.TempDir()
	b.Setenv(StateRootEnv, stateDir)
	b.Setenv("TMPDIR", b.TempDir())
	return stateDir, b.TempDir()
}

// BenchmarkOneShotClaudeSessionPreHook models a long Claude Code session in
// one live state: absolute read paths and repo-relative write paths of files
// that exist, and recorded commands, judged by Write and Bash pre-tool calls.
func BenchmarkOneShotClaudeSessionPreHook(b *testing.B) {
	repo := setupStopBenchmarkRepo(b)
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		b.Fatal(err)
	}
	const sessionID = "one-shot-claude"
	state, err := InitializeSessionState(repo, sessionID)
	if err != nil {
		b.Fatal(err)
	}
	for directory := range 40 {
		for file := range 25 {
			relative := fmt.Sprintf("src/module%02d/internal/file%02d.go", directory, file)
			path := filepath.Join(repo, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("package internal\n"), 0o644); err != nil {
				b.Fatal(err)
			}
			state.ReadPaths = append(state.ReadPaths, path)
			if file < 5 {
				state.WritePaths = append(state.WritePaths, relative)
			}
		}
	}
	for index := range 200 {
		state.Commands = append(state.Commands, fmt.Sprintf("go test ./src/module%02d/...", index%40))
	}
	if err := SaveSessionState(state); err != nil {
		b.Fatal(err)
	}
	for _, call := range []struct {
		name    string
		payload string
	}{
		{name: "write", payload: `{"session_id":"one-shot-claude","tool_use_id":"call-%d","tool_name":"Write","tool_input":{"file_path":"src/module00/internal/file00.go"}}`},
		{name: "bash", payload: `{"session_id":"one-shot-claude","tool_use_id":"call-%d","tool_name":"Bash","tool_input":{"command":"go vet ./src/..."}}`},
	} {
		b.Run(call.name, func(b *testing.B) {
			b.ReportAllocs()
			for index := range b.N {
				payload := []byte(fmt.Sprintf(call.payload, index))
				if result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); result.ExitCode != 0 {
					b.Fatalf("one-shot pre-hook failed: %+v", result)
				}
			}
		})
	}
}

// BenchmarkStrictPostToolLargeOutput records a failed shell call whose tool
// response carries one MiB of output, as OpenCode, Kilo, OMP, and Pi report it.
func BenchmarkStrictPostToolLargeOutput(b *testing.B) {
	_, repo := withBenchmarkStateRoot(b)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := InitializeSessionState(repo, "strict-post"); err != nil {
		b.Fatal(err)
	}
	payload := []byte(fmt.Sprintf(`{"session_id":"strict-post","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":1,"output":%q}}`, strings.Repeat("x", 1<<20)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runPostToolUseCompleteStrictResolved(root, payload)
	}
}
