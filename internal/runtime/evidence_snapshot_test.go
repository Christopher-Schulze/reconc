package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEvidenceSnapshotCacheReusesStableContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := newEvidenceSnapshotCache()
	first, err := cache.snapshot(path, true)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	second, err := cache.snapshot(path, true)
	if err != nil {
		t.Fatalf("cached snapshot: %v", err)
	}
	if first.content != second.content || !second.contentLoaded || cache.bytes != int64(len(first.content)) {
		t.Fatalf("cache did not retain one stable body: first=%+v second=%+v bytes=%d", first, second, cache.bytes)
	}
}

func TestEvidenceSnapshotCacheIgnoresAccessTimeOnlyChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte("stable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := newEvidenceSnapshotCache()
	first, err := cache.snapshot(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().Add(-time.Hour), first.info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := cache.snapshot(path, true)
	if err != nil {
		t.Fatalf("access-time-only change invalidated stable evidence: %v", err)
	}
	if second.identity != first.identity || second.content != first.content {
		t.Fatalf("stable evidence identity changed: first=%+v second=%+v", first, second)
	}
}

func TestEvidenceSnapshotCacheFailsClosedAfterReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := newEvidenceSnapshotCache()
	if _, err := cache.snapshot(path, true); err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	replacement := filepath.Join(dir, "replacement.txt")
	if err := os.WriteFile(replacement, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	_, err := cache.snapshot(path, true)
	if err == nil || !errors.Is(err, errEvidenceSnapshotChanged) {
		t.Fatalf("replacement error = %v, want snapshot-change failure", err)
	}
}

func TestEvidenceSnapshotCacheTracksMissingAndRejectsAppearance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	cache := newEvidenceSnapshotCache()
	missing, err := cache.snapshot(path, false)
	if err != nil || missing.exists {
		t.Fatalf("missing snapshot = %+v, %v", missing, err)
	}
	if err := os.WriteFile(path, []byte("appeared"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = cache.snapshot(path, false)
	if err == nil || !errors.Is(err, errEvidenceSnapshotChanged) {
		t.Fatalf("appearance error = %v, want snapshot-change failure", err)
	}
}

func TestEvidenceSnapshotCacheReusesReadErrorsWithinIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(maxEvidenceFileBytes)+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := newEvidenceSnapshotCache()
	_, firstErr := cache.snapshot(path, true)
	_, secondErr := cache.snapshot(path, true)
	if firstErr == nil || secondErr == nil || firstErr.Error() != secondErr.Error() {
		t.Fatalf("read errors were not reused: first=%v second=%v", firstErr, secondErr)
	}
}

func TestEvidenceSnapshotCacheBoundsEntries(t *testing.T) {
	dir := t.TempDir()
	cache := newEvidenceSnapshotCache()
	for index := 0; index < maxEvidenceSnapshots+16; index++ {
		path := filepath.Join(dir, "f"+strconv.Itoa(index))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := cache.snapshot(path, true); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.entries) > maxEvidenceSnapshots || cache.bytes > maxEvidenceSnapshotBytes {
		t.Fatalf("cache bounds exceeded: entries=%d bytes=%d", len(cache.entries), cache.bytes)
	}
}
