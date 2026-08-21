package compiler

import (
	"strconv"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

func benchmarkSourceBundle() *ingest.SourceBundle {
	sources := make([]policy.PolicySource, 256)
	for index := range sources {
		sources[index] = policy.PolicySource{
			Kind: policy.SourcePolicyFile, Path: "policies/r" + strconv.Itoa(index) + ".yml",
			Content: "rules:\n  - id: rule-" + strconv.Itoa(index) + "\n    kind: deny_write\n    paths: ['src/**']\n",
		}
	}
	return &ingest.SourceBundle{Sources: sources}
}

func BenchmarkCompileSourceProvenancePrepared(b *testing.B) {
	bundle := benchmarkSourceBundle()
	provenance, err := compileSourceProvenance(bundle)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := computeSerializedSourceDigest(provenance.records); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileSourceProvenanceRebuild(b *testing.B) {
	bundle := benchmarkSourceBundle()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		first := make([]interface{}, 0, len(bundle.Sources))
		for _, source := range bundle.Sources {
			first = append(first, sourceToMap(source))
		}
		if _, err := computeSerializedSourceDigest(first); err != nil {
			b.Fatal(err)
		}
		second := make([]interface{}, 0, len(bundle.Sources))
		for _, source := range bundle.Sources {
			second = append(second, sourceToMap(source))
		}
		if _, err := computeSerializedSourceDigest(second); err != nil {
			b.Fatal(err)
		}
	}
}
