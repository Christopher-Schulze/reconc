package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
