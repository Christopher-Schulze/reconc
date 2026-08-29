package proofbundle

import (
	"fmt"
	"testing"

	"reconc.dev/reconc/internal/completiongate"
)

func benchmarkMaximumBundle() *Bundle {
	bundle := validProofBundle()
	bundle.Checks = make([]Check, maxItems)
	bundle.Evidence.SatisfiedChecks = make([]string, maxItems)
	for index := range bundle.Checks {
		id := fmt.Sprintf("check-%03d", index)
		bundle.Checks[index] = Check{
			ID: id, Status: completiongate.StatusPass, Detail: "bounded completion detail",
		}
		bundle.Evidence.SatisfiedChecks[index] = id
	}
	bundle.Digest = mustDigest(bundle)
	return bundle
}

func BenchmarkDigestMaximumArtifact(b *testing.B) {
	bundle := benchmarkMaximumBundle()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := digest(bundle); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalJSONMaximumArtifact(b *testing.B) {
	bundle := benchmarkMaximumBundle()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := MarshalJSON(bundle); err != nil {
			b.Fatal(err)
		}
	}
}
