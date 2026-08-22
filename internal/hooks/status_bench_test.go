package hooks

import (
	"testing"
)

func BenchmarkInspectPlatforms(b *testing.B) {
	b.Setenv("KIMI_CODE_HOME", b.TempDir())
	b.Setenv("PI_CODING_AGENT_DIR", b.TempDir())
	repo := b.TempDir()
	for _, kind := range []string{KindClaudeCode, KindCodex, KindKilo, KindOMP} {
		if _, err := Install(kind, repo, false); err != nil {
			b.Fatalf("install %s: %v", kind, err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := InspectPlatforms(repo); err != nil {
			b.Fatal(err)
		}
	}
}
