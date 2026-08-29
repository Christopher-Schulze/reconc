package runtime

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEvidenceMatchMemoSeparatesOptionsAndFileContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte("approved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readEvidenceSnapshot(path, true)
	if err != nil {
		t.Fatal(err)
	}
	memo := newEvidenceMatchMemo()
	options := evidenceMatchOptions{file: "evidence.txt", mustContain: []string{"approved"}}
	if got := memo.match(path, snapshot, options); len(got.reasons) != 0 {
		t.Fatalf("first match = %#v", got)
	}
	if got := memo.match(path, snapshot, options); len(got.reasons) != 0 || len(memo.entries) != 1 {
		t.Fatalf("identical match did not reuse one entry: result=%#v entries=%d", got, len(memo.entries))
	}
	forbidden := options
	forbidden.mustContain = nil
	forbidden.mustNotContain = "approved"
	got := memo.match(path, snapshot, forbidden)
	if len(got.reasons) != 1 || !strings.Contains(got.reasons[0], "forbidden substring") || len(memo.entries) != 2 {
		t.Fatalf("different matcher options were conflated: result=%#v entries=%d", got, len(memo.entries))
	}
	if err := os.WriteFile(path, []byte("rejected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := readEvidenceSnapshot(path, true)
	if err != nil {
		t.Fatal(err)
	}
	got = memo.match(path, changed, options)
	if len(got.reasons) != 1 || !strings.Contains(got.reasons[0], "approved") || len(memo.entries) != 3 {
		t.Fatalf("changed file content reused stale result: result=%#v entries=%d", got, len(memo.entries))
	}
}

func TestMatchContextMemoClonesResultsAndErrors(t *testing.T) {
	writes := []string{"src/TASK-1.md"}
	memo := newMatchContextMemo(writes)
	patterns := []string{"src/{task}.md"}
	first, err := memo.collect(nil, patterns)
	if err != nil || len(first) != 1 || first[0].captures["task"] != "TASK-1" {
		t.Fatalf("first context = %#v, %v", first, err)
	}
	if !memo.writeIdentityReady || memo.writeIdentity != digestStrings(writes) {
		t.Fatal("memo did not retain one evaluation-scoped write identity")
	}
	first[0].captures["task"] = "mutated"
	second, err := memo.collect(nil, patterns)
	if err != nil || second[0].captures["task"] != "TASK-1" {
		t.Fatalf("memo returned mutable context: %#v, %v", second, err)
	}
	second[0].captures["task"] = "mutated-hit"
	third, err := memo.collect(nil, patterns)
	if err != nil || third[0].captures["task"] != "TASK-1" {
		t.Fatalf("memo hit exposed cached context ownership: %#v, %v", third, err)
	}
	invalid := []string{"src/["}
	if _, err := memo.collect(nil, invalid); err == nil {
		t.Fatal("invalid matcher was accepted")
	}
	if _, err := memo.collect(nil, invalid); err == nil || len(memo.entries) != 2 {
		t.Fatalf("invalid matcher was not memoized: err=%v entries=%d", err, len(memo.entries))
	}
}

func TestMatchContextMemoEnforcesByteBudget(t *testing.T) {
	oversized := []matchContext{{
		path: strings.Repeat("p", maxMatchContextMemoBytes),
	}}
	key := matchContextMemoKey{writes: digestStrings([]string{"write"})}
	if bytes := matchContextMemoEntryBytes(key, oversized, nil); bytes <= maxMatchContextMemoBytes {
		t.Fatalf("oversized fixture charged only %d bytes", bytes)
	}
	// The production store path must never retain a result whose defensive
	// storage and return clones exceed the complete memo budget.
	writes := []string{strings.Repeat("p", maxMatchContextMemoBytes)}
	memo := newMatchContextMemo(writes)
	patterns := []string{"**"}
	if _, err := memo.collect(nil, patterns); err != nil {
		t.Fatal(err)
	}
	if len(memo.entries) != 0 || memo.bytes != 0 {
		t.Fatalf("oversized entry retained: entries=%d bytes=%d", len(memo.entries), memo.bytes)
	}
}

func TestComparableEvidenceOptionsPreservesSliceBoundaries(t *testing.T) {
	first := comparableEvidenceOptions(evidenceMatchOptions{mustContain: []string{"ab", "c"}})
	second := comparableEvidenceOptions(evidenceMatchOptions{mustContain: []string{"a", "bc"}})
	if first == second {
		t.Fatal("length-delimited evidence options conflated distinct slices")
	}
}

func BenchmarkEvidenceMatchMemoShared(b *testing.B) {
	snapshot := evidenceFileSnapshot{
		path:          "evidence.txt",
		identity:      "fixture",
		exists:        true,
		info:          fakeEvidenceInfo{mode: 0o600, size: 9},
		content:       "approved\n",
		contentLoaded: true,
	}
	memo := newEvidenceMatchMemo()
	options := evidenceMatchOptions{file: "evidence.txt", mustContain: []string{"approved"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if got := memo.match("/repo/evidence.txt", snapshot, options); len(got.reasons) != 0 {
			b.Fatal(got.reasons)
		}
	}
}

func BenchmarkMatchContextMemoHit(b *testing.B) {
	writes := []string{"src/TASK-1.md", "src/TASK-2.md"}
	memo := newMatchContextMemo(writes)
	patterns := []string{"src/{task}.md"}
	if _, err := memo.collect(nil, patterns); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		contexts, err := memo.collect(nil, patterns)
		if err != nil || len(contexts) != len(writes) {
			b.Fatalf("memo hit = %d, %v", len(contexts), err)
		}
	}
}

func BenchmarkMatchContextMemoMiss(b *testing.B) {
	writes := make([]string, 32)
	for index := range writes {
		writes[index] = "src/TASK-" + strconv.Itoa(index) + ".md"
	}
	patterns := []string{"src/{task}.md"}
	matchers := &runtimeTemplateMatchers{byPattern: map[string]compiledTemplateMatcher{
		patterns[0]: compileTemplateMatcher(patterns[0]),
	}}
	b.ReportAllocs()
	for range b.N {
		memo := newMatchContextMemo(writes)
		contexts, err := memo.collect(matchers, patterns)
		if err != nil || len(contexts) != len(writes) {
			b.Fatalf("memo miss = %d, %v", len(contexts), err)
		}
	}
}

func BenchmarkMatchContextMemoDuplicateWrites(b *testing.B) {
	writes := make([]string, 1024)
	for index := range writes {
		writes[index] = "src/TASK-1.md"
	}
	patterns := []string{"src/{task}.md"}
	matchers := &runtimeTemplateMatchers{byPattern: map[string]compiledTemplateMatcher{
		patterns[0]: compileTemplateMatcher(patterns[0]),
	}}
	contextCount := 0
	b.ReportAllocs()
	for range b.N {
		normalized := finishInputNormalization(ExecutionInputs{}, nil, writes, nil)
		memo := newMatchContextMemo(normalized.inputs.WritePaths)
		contexts, err := memo.collect(matchers, patterns)
		if err != nil || len(contexts) == 0 {
			b.Fatalf("duplicate contexts = %d, %v", len(contexts), err)
		}
		contextCount = len(contexts)
	}
	b.ReportMetric(float64(contextCount), "contexts/op")
}

type fakeEvidenceInfo struct {
	mode os.FileMode
	size int64
}

func (i fakeEvidenceInfo) Name() string       { return "evidence.txt" }
func (i fakeEvidenceInfo) Size() int64        { return i.size }
func (i fakeEvidenceInfo) Mode() os.FileMode  { return i.mode }
func (i fakeEvidenceInfo) ModTime() time.Time { return time.Unix(1, 0) }
func (i fakeEvidenceInfo) IsDir() bool        { return false }
func (i fakeEvidenceInfo) Sys() interface{}   { return struct{ ID int }{1} }
