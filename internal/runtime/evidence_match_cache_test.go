package runtime

import (
	"os"
	"path/filepath"
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
	memo := newMatchContextMemo()
	writes := []string{"src/TASK-1.md"}
	patterns := []string{"src/{task}.md"}
	first, err := memo.collect(nil, writes, patterns)
	if err != nil || len(first) != 1 || first[0].captures["task"] != "TASK-1" {
		t.Fatalf("first context = %#v, %v", first, err)
	}
	first[0].captures["task"] = "mutated"
	second, err := memo.collect(nil, writes, patterns)
	if err != nil || second[0].captures["task"] != "TASK-1" {
		t.Fatalf("memo returned mutable context: %#v, %v", second, err)
	}
	invalid := []string{"src/["}
	if _, err := memo.collect(nil, writes, invalid); err == nil {
		t.Fatal("invalid matcher was accepted")
	}
	if _, err := memo.collect(nil, writes, invalid); err == nil || len(memo.entries) != 2 {
		t.Fatalf("invalid matcher was not memoized: err=%v entries=%d", err, len(memo.entries))
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
