package ingest

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func BenchmarkLoadPolicySourcesWithContext(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := b.TempDir()
	writeBenchSource(b, repo, "AGENTS.md", "# project\n")
	for index := 0; index < 16; index++ {
		writeBenchSource(b, repo, filepath.Join("policies", "r"+strconv.Itoa(index)+".yml"), "rules: []\n")
	}
	context, err := NewSourceLoadContext(repo)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := LoadPolicySourcesWithContext(context); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadPolicySourcesWithDiscovery(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := b.TempDir()
	writeBenchSource(b, repo, "AGENTS.md", "# project\n")
	for index := 0; index < 16; index++ {
		writeBenchSource(b, repo, filepath.Join("policies", "r"+strconv.Itoa(index)+".yml"), "rules: []\n")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := LoadPolicySources(repo); err != nil {
			b.Fatal(err)
		}
	}
}

func writeBenchSource(b *testing.B, root, relative, content string) {
	b.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
}
