package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluationMemosAllocateOrderStorageOnDemand(t *testing.T) {
	matchMemo := newEvidenceMatchMemo()
	contextMemo := newMatchContextMemo([]string{"a.txt"})
	snapshotCache := newEvidenceSnapshotCache()
	if matchMemo.entries != nil || matchMemo.order != nil ||
		contextMemo.entries != nil || contextMemo.order != nil ||
		snapshotCache.entries != nil || snapshotCache.order != nil {
		t.Fatal("evaluation memo constructors allocated maximum-bound storage")
	}

	matchMemo.store(evidenceMatchKey{path: "one"}, evidenceMatchResult{})
	if len(matchMemo.entries) != 1 || len(matchMemo.order) != 1 || cap(matchMemo.order) >= maxEvidenceMatchMemoEntries {
		t.Fatalf("match memo storage = entries %d, order %d/%d", len(matchMemo.entries), len(matchMemo.order), cap(matchMemo.order))
	}
	if _, err := contextMemo.collect(nil, []string{"{name}.txt"}); err != nil {
		t.Fatal(err)
	}
	if len(contextMemo.entries) != 1 || len(contextMemo.order) != 1 || cap(contextMemo.order) >= maxMatchContextMemoEntries {
		t.Fatalf("context memo storage = entries %d, order %d/%d", len(contextMemo.entries), len(contextMemo.order), cap(contextMemo.order))
	}
	snapshotCache.store("one", evidenceFileSnapshot{path: "one"})
	if len(snapshotCache.entries) != 1 || len(snapshotCache.order) != 1 || cap(snapshotCache.order) >= maxEvidenceSnapshots {
		t.Fatalf("snapshot memo storage = entries %d, order %d/%d", len(snapshotCache.entries), len(snapshotCache.order), cap(snapshotCache.order))
	}
}

func TestFreshnessFileUsesCallerOwnedCopyBuffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.yml")
	body := []byte("rules: []\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var total int64
	observation, err := observeFreshnessFile(path, &total, make([]byte, 1024))
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(body)
	if observation.Digest != hex.EncodeToString(want[:]) || total != int64(len(body)) {
		t.Fatalf("freshness observation = %#v, total %d", observation, total)
	}
	if _, err := observeFreshnessFile(path, &total, nil); err == nil {
		t.Fatal("empty freshness copy buffer was accepted")
	}
}
