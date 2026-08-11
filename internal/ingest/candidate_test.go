package ingest

import (
	"slices"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestAppendCandidateSourcePreservesCanonicalPrecedenceAndInput(t *testing.T) {
	base := []policy.PolicySource{
		{Kind: policy.SourceCompilerConfig, Path: ".reconc.yml", Content: "rules: []"},
		{Kind: policy.SourcePreset, Path: "preset:base", Content: "rules: []"},
		{Kind: policy.SourcePolicyFile, Path: ".reconc/policy.yml", Content: "rules: []"},
		{Kind: policy.SourceCustomRuntime, Path: ".reconc/runtime.yml", Content: "runtime"},
	}
	for _, test := range []struct {
		name string
		kind policy.SourceKind
		want []policy.SourceKind
	}{
		{
			name: "preset", kind: policy.SourcePreset,
			want: []policy.SourceKind{
				policy.SourceCompilerConfig, policy.SourcePreset, policy.SourcePreset,
				policy.SourcePolicyFile, policy.SourceCustomRuntime,
			},
		},
		{
			name: "policy file", kind: policy.SourcePolicyFile,
			want: []policy.SourceKind{
				policy.SourceCompilerConfig, policy.SourcePreset, policy.SourcePolicyFile,
				policy.SourcePolicyFile, policy.SourceCustomRuntime,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := append([]policy.PolicySource{}, base...)
			bundle := &SourceBundle{Sources: append([]policy.PolicySource{}, base...)}
			candidate := policy.PolicySource{
				Kind: test.kind, Path: "candidate", Content: "rules: []",
				BlockID: policy.ImpactCandidateBlockPrefix + "candidate",
			}
			got, err := AppendCandidateSource(bundle, candidate)
			if err != nil {
				t.Fatal(err)
			}
			kinds := make([]policy.SourceKind, len(got.Sources))
			for index, source := range got.Sources {
				kinds[index] = source.Kind
			}
			if !slices.Equal(kinds, test.want) || !slices.Equal(bundle.Sources, original) {
				t.Fatalf("candidate sources = %v; input mutated=%t", kinds, !slices.Equal(bundle.Sources, original))
			}
		})
	}
}
