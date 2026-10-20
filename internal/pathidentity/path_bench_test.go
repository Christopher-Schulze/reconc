package pathidentity

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkResolveProspectiveBatch(b *testing.B) {
	root := b.TempDir()
	paths := []string{
		filepath.Join(root, "shared", "missing", "one.txt"),
		filepath.Join(root, "shared", "missing", "two.txt"),
		filepath.Join(root, "shared", "missing", "three.txt"),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := ResolveProspectiveBatch(paths); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveProspectiveIndependent(b *testing.B) {
	root := b.TempDir()
	paths := []string{
		filepath.Join(root, "shared", "missing", "one.txt"),
		filepath.Join(root, "shared", "missing", "two.txt"),
		filepath.Join(root, "shared", "missing", "three.txt"),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, path := range paths {
			if _, err := ResolveProspective(path); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkResolveProspectiveSessionEvidence resolves the shape of a long
// session's evidence: existing files spread over a few nested directories,
// plus deleted files below directories that no longer exist.
func BenchmarkResolveProspectiveSessionEvidence(b *testing.B) {
	root := b.TempDir()
	paths := make([]string, 0, 1024)
	for directory := range 16 {
		parent := filepath.Join(root, "src", fmt.Sprintf("module-%02d", directory), "internal")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			b.Fatal(err)
		}
		for file := range 48 {
			path := filepath.Join(parent, fmt.Sprintf("file-%02d.go", file))
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				b.Fatal(err)
			}
			paths = append(paths, path)
		}
		for file := range 16 {
			paths = append(paths, filepath.Join(root, "removed", fmt.Sprintf("module-%02d", directory), "internal", fmt.Sprintf("file-%02d.go", file)))
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := ResolveProspectiveBatch(paths); err != nil {
			b.Fatal(err)
		}
	}
}
