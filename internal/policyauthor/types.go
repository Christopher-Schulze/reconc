// Package policyauthor provides the read-only preview and transactional
// publication boundary for repository-owned policy fragments.
package policyauthor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/schema"
)

const (
	ReportFormatVersion = "reconc.policy-author/v1"
	DefaultTarget       = "policies/reconc-author.yml"
	MaxCandidateBytes   = 8 << 20
)

type Request struct {
	Repo          string
	Version       string
	Target        string
	CandidateKind string
	CandidateName string
	Body          []byte
}

type Validation struct {
	SchemaValid  bool `json:"schema_valid"`
	CompileValid bool `json:"compile_valid"`
	Ready        bool `json:"ready"`
}

type RuleKindCount struct {
	Kind  policy.Kind `json:"kind"`
	Count int         `json:"count"`
}

type Explanation struct {
	EffectivePacks []string                  `json:"effective_packs"`
	Rules          []policy.Rule             `json:"rules"`
	Sources        []compiler.CompiledSource `json:"sources"`
	RuleKinds      []RuleKindCount           `json:"rule_kinds"`
	Conflicts      []compiler.Conflict       `json:"conflicts"`
	Warnings       []string                  `json:"warnings"`
}

type Preview struct {
	FormatVersion        string      `json:"format_version"`
	RepoRoot             string      `json:"repo_root"`
	CandidateKind        string      `json:"candidate_kind"`
	CandidateName        string      `json:"candidate_name"`
	CandidateSHA256      string      `json:"candidate_sha256"`
	SchemaURL            string      `json:"schema_url"`
	Target               string      `json:"target"`
	BaseSourceDigest     string      `json:"base_source_digest"`
	CompiledSourceDigest string      `json:"compiled_source_digest"`
	Validation           Validation  `json:"validation"`
	Explanation          Explanation `json:"explanation"`

	physicalRoot string
	lockfile     []byte
}

type Adoption struct {
	Requested          bool   `json:"requested"`
	Confirmed          bool   `json:"confirmed"`
	Applied            bool   `json:"applied"`
	Declined           bool   `json:"declined"`
	RolledBack         bool   `json:"rolled_back"`
	Target             string `json:"target"`
	PreviousLockSHA256 string `json:"previous_lock_sha256,omitempty"`
	LockSHA256         string `json:"lock_sha256,omitempty"`
}

type lockExplanation struct {
	Sources []compiler.CompiledSource `json:"sources"`
	Rules   []policy.Rule             `json:"rules"`
}

func Prepare(request Request) (Preview, error) {
	request.Target = normalizedTarget(request.Target)
	if err := validateRequest(request); err != nil {
		return Preview{}, err
	}
	if err := schema.ValidatePolicyConfigYAML(request.Body); err != nil {
		return Preview{}, fmt.Errorf("validate candidate schema: %w", err)
	}
	compiled, body, baseDigest, err := compiler.RenderRepoPolicyWithTargetCandidate(
		request.Repo, request.Version, request.Target, string(request.Body),
	)
	if err != nil {
		return Preview{}, fmt.Errorf("compile candidate: %w", err)
	}
	if err := validateTargetIdentity(compiled.RepoRoot, request.Target); err != nil {
		return Preview{}, err
	}
	explanation, err := explainLockfile(body, compiled)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		FormatVersion: ReportFormatVersion, RepoRoot: ".",
		CandidateKind: request.CandidateKind, CandidateName: request.CandidateName,
		CandidateSHA256: digest(request.Body), SchemaURL: schema.PolicyConfigURL,
		Target: request.Target, BaseSourceDigest: baseDigest,
		CompiledSourceDigest: compiled.SourceDigest,
		Validation:           Validation{SchemaValid: true, CompileValid: true, Ready: len(compiled.Conflicts) == 0},
		Explanation:          explanation, physicalRoot: compiled.RepoRoot,
		lockfile: append([]byte(nil), body...),
	}, nil
}

func (preview Preview) LockfileBytes() []byte {
	return append([]byte(nil), preview.lockfile...)
}

func (preview Preview) RepositoryRoot() string {
	return preview.physicalRoot
}

func explainLockfile(body []byte, compiled *compiler.CompiledPolicy) (Explanation, error) {
	var lock lockExplanation
	if err := json.Unmarshal(body, &lock); err != nil {
		return Explanation{}, fmt.Errorf("decode compiled candidate explanation: %w", err)
	}
	kindCounts := map[policy.Kind]int{}
	for _, rule := range lock.Rules {
		kindCounts[rule.Kind]++
	}
	kinds := make([]policy.Kind, 0, len(kindCounts))
	for kind := range kindCounts {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	counts := make([]RuleKindCount, 0, len(kinds))
	for _, kind := range kinds {
		counts = append(counts, RuleKindCount{Kind: kind, Count: kindCounts[kind]})
	}
	packs := []string{}
	for _, source := range lock.Sources {
		if source.Kind == policy.SourcePreset {
			packs = append(packs, strings.TrimPrefix(source.Path, "preset:"))
		}
	}
	slices.Sort(packs)
	packs = slices.Compact(packs)
	return Explanation{
		EffectivePacks: packs, Rules: lock.Rules, Sources: lock.Sources,
		RuleKinds: counts, Conflicts: append([]compiler.Conflict{}, compiled.Conflicts...),
		Warnings: append([]string{}, compiled.Warnings...),
	}, nil
}

func validateRequest(request Request) error {
	if request.Repo == "" || request.Version == "" {
		return fmt.Errorf("repository and compiler version are required")
	}
	if request.CandidateKind != "file" && request.CandidateKind != "detected" {
		return fmt.Errorf("candidate kind must be file or detected")
	}
	if strings.TrimSpace(request.CandidateName) == "" {
		return fmt.Errorf("candidate name is required")
	}
	if len(request.Body) == 0 || len(request.Body) > MaxCandidateBytes || !utf8.Valid(request.Body) {
		return fmt.Errorf("candidate must contain 1..%d bytes of valid UTF-8 policy YAML", MaxCandidateBytes)
	}
	return validateTargetPath(request.Target)
}

func normalizedTarget(target string) string {
	if strings.TrimSpace(target) == "" {
		return DefaultTarget
	}
	return strings.ReplaceAll(strings.TrimSpace(target), `\`, "/")
}

func validateTargetPath(target string) error {
	if pathidentity.Rooted(target) || pathidentity.EscapesLexically(target) {
		return fmt.Errorf("policy target must be repository-relative without traversal")
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(target)))
	parts := strings.Split(cleaned, "/")
	if cleaned != target || len(parts) != 2 || parts[0] != "policies" || parts[1] == "" {
		return fmt.Errorf("policy target must be a direct policies/*.yml or policies/*.yaml file")
	}
	extension := strings.ToLower(filepath.Ext(parts[1]))
	if extension != ".yml" && extension != ".yaml" {
		return fmt.Errorf("policy target must end in .yml or .yaml")
	}
	stem := strings.TrimSuffix(parts[1], filepath.Ext(parts[1]))
	if strings.Trim(stem, ".") == "" {
		return fmt.Errorf("policy target %q must include a non-dot filename stem before .yml or .yaml", target)
	}
	return nil
}

func validateTargetIdentity(root, target string) error {
	rootIdentity, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return fmt.Errorf("resolve repository identity: %w", err)
	}
	targetIdentity, err := pathidentity.ResolveProspective(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil {
		return fmt.Errorf("resolve policy target identity: %w", err)
	}
	wantParent := filepath.Join(rootIdentity, "policies")
	if filepath.Dir(targetIdentity) != wantParent {
		return fmt.Errorf("policy target escapes the repository-owned policies directory")
	}
	return nil
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
