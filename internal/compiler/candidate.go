package compiler

import (
	"fmt"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

// CandidateSource is one additive in-memory policy input. Name is a logical
// provenance label only and never carries the candidate file's host path.
type CandidateSource struct {
	Kind    policy.SourceKind
	Name    string
	Content string
}

// RenderRepoPolicyWithCandidate compiles the current discovered sources plus
// one additive candidate through the production parser and compiler without
// taking the compile lock or publishing a lockfile.
func RenderRepoPolicyWithCandidate(repoStartPath, compilerVersion string, candidate CandidateSource) (*CompiledPolicy, []byte, string, error) {
	bundle, err := ingest.LoadPolicySources(repoStartPath)
	if err != nil {
		return nil, nil, "", err
	}
	baseSourceDigest, err := ComputeSourceDigest(bundle)
	if err != nil {
		return nil, nil, "", err
	}
	source, err := candidate.policySource()
	if err != nil {
		return nil, nil, "", err
	}
	bundle, err = ingest.AppendCandidateSource(bundle, source)
	if err != nil {
		return nil, nil, "", err
	}
	compiled, body, err := renderPolicyBundle(bundle, compilerVersion)
	return compiled, body, baseSourceDigest, err
}

// RenderRepoPolicyWithTargetCandidate compiles a repository as if one exact
// repository policy fragment already contained candidateContent. It never
// writes the target or lockfile, and replacement uses production provenance.
func RenderRepoPolicyWithTargetCandidate(repoStartPath, compilerVersion, targetPath, candidateContent string) (*CompiledPolicy, []byte, string, error) {
	bundle, err := ingest.LoadPolicySources(repoStartPath)
	if err != nil {
		return nil, nil, "", err
	}
	baseSourceDigest, err := ComputeSourceDigest(bundle)
	if err != nil {
		return nil, nil, "", err
	}
	bundle, err = ingest.ReplacePolicyFileSource(bundle, targetPath, candidateContent)
	if err != nil {
		return nil, nil, "", err
	}
	compiled, body, err := renderPolicyBundle(bundle, compilerVersion)
	return compiled, body, baseSourceDigest, err
}

func (candidate CandidateSource) policySource() (policy.PolicySource, error) {
	if candidate.Name == "" || candidate.Content == "" {
		return policy.PolicySource{}, fmt.Errorf("candidate name and content must be non-empty")
	}
	switch candidate.Kind {
	case policy.SourcePolicyFile:
		return policy.PolicySource{
			Kind: candidate.Kind, Path: ".reconc/impact/candidate.yml",
			BlockID: policy.ImpactCandidateBlockPrefix + candidate.Name, Content: candidate.Content,
		}, nil
	case policy.SourcePreset:
		return policy.PolicySource{
			Kind: candidate.Kind, Path: "preset:" + candidate.Name,
			BlockID: policy.ImpactCandidateBlockPrefix + candidate.Name, Content: candidate.Content,
		}, nil
	default:
		return policy.PolicySource{}, fmt.Errorf("candidate kind %q must be policy_file or preset", candidate.Kind)
	}
}
