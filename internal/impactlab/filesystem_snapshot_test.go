package impactlab

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkRepositorySnapshotManySmallFiles(b *testing.B) {
	root := b.TempDir()
	content := make([]byte, 1024)
	for index := range 256 {
		path := filepath.Join(root, fmt.Sprintf("evidence-%03d.txt", index))
		if err := os.WriteFile(path, content, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(content) * 256))
	b.ResetTimer()
	for b.Loop() {
		if _, err := captureRepositorySnapshot(root); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRepositorySnapshotMaximumAcceptedFile(b *testing.B) {
	root := b.TempDir()
	path := filepath.Join(root, "maximum.bin")
	file, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	if err := file.Close(); err != nil {
		b.Fatal(err)
	}
	if err := os.Truncate(path, maxImpactSnapshotFileBytes); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(maxImpactSnapshotFileBytes)
	for b.Loop() {
		if _, err := captureRepositorySnapshot(root); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRepositorySnapshotByteLimits(t *testing.T) {
	tests := []struct {
		name          string
		files         map[string]int
		fileLimit     int64
		totalLimit    int64
		wantFileBytes int64
		wantError     string
	}{
		{
			name:          "maximum accepted",
			files:         map[string]int{"evidence.bin": 8},
			fileLimit:     8,
			totalLimit:    8,
			wantFileBytes: 8,
		},
		{
			name:       "one huge file",
			files:      map[string]int{"evidence.bin": 9},
			fileLimit:  8,
			totalLimit: 16,
			wantError:  `capture "evidence.bin": regular file size 9 exceeds 8-byte limit`,
		},
		{
			name:       "aggregate overflow",
			files:      map[string]int{"a.bin": 5, "b.bin": 4},
			fileLimit:  5,
			totalLimit: 8,
			wantError:  `capture "b.bin": repository snapshot file bytes exceed 8-byte limit`,
		},
		{
			name:          "empty file with zero byte budget",
			files:         map[string]int{"empty.bin": 0},
			fileLimit:     0,
			totalLimit:    0,
			wantFileBytes: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for name, size := range test.files {
				if err := os.WriteFile(filepath.Join(root, name), make([]byte, size), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			limits := repositorySnapshotLimits{entries: 10, fileBytes: test.fileLimit, totalFileBytes: test.totalLimit}
			snapshot, err := captureRepositorySnapshotWithLimits(root, limits)
			if test.wantError != "" {
				if err == nil || err.Error() != test.wantError {
					t.Fatalf("captureRepositorySnapshotWithLimits() error = %v, want %q", err, test.wantError)
				}
				if snapshot != nil {
					t.Fatal("captureRepositorySnapshotWithLimits() returned a partial snapshot")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.fileBytes != test.wantFileBytes {
				t.Fatalf("snapshot file bytes = %d, want %d", snapshot.fileBytes, test.wantFileBytes)
			}
		})
	}
}

func TestRepositorySnapshotAcceptsManySmallFilesAtAggregateLimit(t *testing.T) {
	root := t.TempDir()
	for index := range 32 {
		path := filepath.Join(root, fmt.Sprintf("evidence-%02d.bin", index))
		if err := os.WriteFile(path, make([]byte, 7), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	limits := repositorySnapshotLimits{entries: 33, fileBytes: 7, totalFileBytes: 32 * 7}
	snapshot, err := captureRepositorySnapshotWithLimits(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.fileBytes != limits.totalFileBytes || len(snapshot.entries) != limits.entries {
		t.Fatalf("snapshot = %d bytes and %d entries, want %d bytes and %d entries", snapshot.fileBytes, len(snapshot.entries), limits.totalFileBytes, limits.entries)
	}
}

func TestRepositorySnapshotRevalidationRetainsByteLimits(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "evidence.bin")
	if err := os.WriteFile(path, make([]byte, 8), 0o600); err != nil {
		t.Fatal(err)
	}
	limits := repositorySnapshotLimits{entries: 2, fileBytes: 8, totalFileBytes: 8}
	snapshot, err := captureRepositorySnapshotWithLimits(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, 9), 0o600); err != nil {
		t.Fatal(err)
	}

	err = snapshot.revalidate(root)
	want := `capture "evidence.bin": regular file size 9 exceeds 8-byte limit`
	if err == nil || err.Error() != want {
		t.Fatalf("revalidate() error = %v, want %q", err, want)
	}
}

func TestRepositorySnapshotRejectsSameMetadataContentDrift(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "evidence.txt")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := captureRepositorySnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}

	err = snapshot.revalidate(root)
	if err == nil || !strings.Contains(err.Error(), "evidence.txt") {
		t.Fatalf("revalidate() error = %v, want evidence path drift", err)
	}
}
