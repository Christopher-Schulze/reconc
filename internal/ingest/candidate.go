package ingest

import (
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

// AppendCandidateSource returns a private copy of a discovered source bundle
// with one bounded in-memory policy or preset source at the normal production
// precedence. The original bundle is never mutated.
func AppendCandidateSource(bundle *SourceBundle, source policy.PolicySource) (*SourceBundle, error) {
	if bundle == nil {
		return nil, fmt.Errorf("candidate source bundle is nil")
	}
	if source.Kind != policy.SourcePolicyFile && source.Kind != policy.SourcePreset {
		return nil, fmt.Errorf("candidate source kind %q must be policy_file or preset", source.Kind)
	}
	if source.Path == "" || source.Content == "" {
		return nil, fmt.Errorf("candidate source path and content must be non-empty")
	}
	if !strings.HasPrefix(source.BlockID, policy.ImpactCandidateBlockPrefix) ||
		len(source.BlockID) == len(policy.ImpactCandidateBlockPrefix) {
		return nil, fmt.Errorf("candidate source block identity is invalid")
	}
	out := *bundle
	out.Sources = insertCandidateSource(bundle.Sources, source)
	if err := validatePolicySourceBounds(out.Sources); err != nil {
		return nil, err
	}
	return &out, nil
}

func insertCandidateSource(sources []policy.PolicySource, candidate policy.PolicySource) []policy.PolicySource {
	index := len(sources)
	candidateRank := candidateSourceRank(candidate.Kind)
	for sourceIndex, source := range sources {
		if candidateSourceRank(source.Kind) > candidateRank {
			index = sourceIndex
			break
		}
	}
	out := make([]policy.PolicySource, len(sources)+1)
	copy(out, sources[:index])
	out[index] = candidate
	copy(out[index+1:], sources[index:])
	return out
}

func candidateSourceRank(kind policy.SourceKind) int {
	return policy.SourceRank(kind)
}
