package ingest

import (
	"fmt"

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
	copy := *bundle
	copy.Sources = append(append([]policy.PolicySource(nil), bundle.Sources...), source)
	if err := validatePolicySourceBounds(copy.Sources); err != nil {
		return nil, err
	}
	return &copy, nil
}
