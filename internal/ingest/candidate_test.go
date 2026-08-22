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

func TestReplacePolicyFileSourceUsesProductionOrderAndDetachedInput(t *testing.T) {
	base := []policy.PolicySource{
		{Kind: policy.SourceCompilerConfig, Path: ".reconc.yml", Content: "rules: []"},
		{Kind: policy.SourcePolicyFile, Path: "policies/a.yml", Content: "rules: []"},
		{Kind: policy.SourcePolicyFile, Path: "policies/z.yml", Content: "rules: []"},
		{Kind: policy.SourceCustomRuntime, Path: ".reconc/runtimes/custom.json", Content: "runtime"},
	}
	bundle := &SourceBundle{Sources: append([]policy.PolicySource(nil), base...)}
	got, err := ReplacePolicyFileSource(bundle, "policies/m.yml", "rules:\n  - id: middle\n")
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{".reconc.yml", "policies/a.yml", "policies/m.yml", "policies/z.yml", ".reconc/runtimes/custom.json"}
	paths := make([]string, len(got.Sources))
	for index, source := range got.Sources {
		paths[index] = source.Path
	}
	if !slices.Equal(paths, wantPaths) || !slices.Equal(bundle.Sources, base) {
		t.Fatalf("replacement paths = %v; input mutated=%t", paths, !slices.Equal(bundle.Sources, base))
	}

	replaced, err := ReplacePolicyFileSource(got, "policies/m.yml", "rules:\n  - id: replacement\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced.Sources) != len(got.Sources) || replaced.Sources[2].Content != "rules:\n  - id: replacement\n" {
		t.Fatalf("replacement source = %#v", replaced.Sources)
	}
}
