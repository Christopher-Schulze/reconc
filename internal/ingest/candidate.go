package ingest

import (
	"fmt"
	"slices"
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

// ReplacePolicyFileSource returns a private source bundle in which one real
// repository policy-file path has the supplied candidate bytes. A missing
// target is inserted at the same lexical position production discovery uses.
func ReplacePolicyFileSource(bundle *SourceBundle, path, content string) (*SourceBundle, error) {
	if bundle == nil || path == "" || content == "" {
		return nil, fmt.Errorf("replacement policy source bundle, path, and content must be non-empty")
	}
	sources := make([]policy.PolicySource, 0, len(bundle.Sources)+1)
	found := false
	for _, source := range bundle.Sources {
		if source.Kind == policy.SourcePolicyFile && source.Path == path {
			if found {
				return nil, fmt.Errorf("replacement policy source path %q is duplicated", path)
			}
			found = true
			continue
		}
		sources = append(sources, source)
	}
	sources = insertPolicyFileSource(sources, policy.PolicySource{
		Kind: policy.SourcePolicyFile, Path: path, Content: content,
	})
	out := *bundle
	out.Sources = sources
	out.Discovery = discoveryWithPolicyPath(bundle.Discovery, path)
	if err := validatePolicySourceBounds(out.Sources); err != nil {
		return nil, err
	}
	return &out, nil
}

func discoveryWithPolicyPath(discovery DiscoveryResult, path string) DiscoveryResult {
	out := discovery
	out.ClaudePath = cloneOptionalString(discovery.ClaudePath)
	out.AgentsPath = cloneOptionalString(discovery.AgentsPath)
	out.StartMDPath = cloneOptionalString(discovery.StartMDPath)
	out.ConfigPath = cloneOptionalString(discovery.ConfigPath)
	out.LockfilePath = cloneOptionalString(discovery.LockfilePath)
	out.ConfigCandidates = cloneStringSlice(discovery.ConfigCandidates)
	out.PolicyPaths = cloneStringSlice(discovery.PolicyPaths)
	if !slices.Contains(out.PolicyPaths, path) {
		out.PolicyPaths = append(out.PolicyPaths, path)
		slices.Sort(out.PolicyPaths)
	}
	out.Warnings = make([]string, 0, len(discovery.Warnings))
	for _, warning := range discovery.Warnings {
		if warning != policyFragmentsMissingWarning {
			out.Warnings = append(out.Warnings, warning)
		}
	}
	return out
}

func insertPolicyFileSource(sources []policy.PolicySource, candidate policy.PolicySource) []policy.PolicySource {
	index := len(sources)
	for sourceIndex, source := range sources {
		if policy.SourceRank(source.Kind) > policy.SourceRank(candidate.Kind) ||
			source.Kind == candidate.Kind && source.Path > candidate.Path {
			index = sourceIndex
			break
		}
	}
	return slices.Insert(sources, index, candidate)
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
