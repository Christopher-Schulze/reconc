package ingest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func BenchmarkBoundedPolicyGlob(b *testing.B) {
	root := b.TempDir()
	for index := 0; index < 256; index++ {
		writeBenchSource(b, root, filepath.Join("policies", "r"+strconv.Itoa(index)+".yml"), "rules: []\n")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := boundedPolicyGlob(root, "policies/*.yml"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnboundedPolicyGlobBaseline(b *testing.B) {
	root := b.TempDir()
	for index := 0; index < 256; index++ {
		writeBenchSource(b, root, filepath.Join("policies", "r"+strconv.Itoa(index)+".yml"), "rules: []\n")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := filepath.Glob(filepath.Join(root, "policies", "*.yml")); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExtractInlineBlocksLinear(b *testing.B) {
	text := strings.Repeat("prefix text\n", 4096) + "```reconc\nrules: []\n```\n"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := ScanInlinePolicyBlocks("AGENTS.md", text); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRepositoryConfigOnce(b *testing.B) {
	config := "include:\n  - policies/*.yml\nextends:\n  - default\n"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		doc, err := decodeYAMLMapping(config, ".reconc.yml")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := loadIncludePatternsDocument(doc, ".reconc.yml"); err != nil {
			b.Fatal(err)
		}
		if _, err := loadPresetNamesDocument(doc, ".reconc.yml"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRepositoryConfigTwice(b *testing.B) {
	config := "include:\n  - policies/*.yml\nextends:\n  - default\n"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := loadIncludePatterns(config, ".reconc.yml"); err != nil {
			b.Fatal(err)
		}
		if _, err := loadPresetNames(config, ".reconc.yml"); err != nil {
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
