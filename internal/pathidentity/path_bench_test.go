package pathidentity

import (
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
