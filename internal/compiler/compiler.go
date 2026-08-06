// Package compiler is the third stage of the reconc pipeline.
//
// CompileRepoPolicy loads sources, parses them into a typed
// ParsedPolicy, computes a SHA-256 source digest over a canonicalized
// source bundle, and writes ".reconc/policy.lock.json" with sorted
// keys and explicit $schema / format_version fields.
//
// The lockfile is byte-stable for the same inputs: sorted keys,
// indent-2, trailing newline. Two compiles of identical sources
// produce identical bytes (and identical digests).
package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/customruntime"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/schema"
)

// LockfileFormatVersion is bumped whenever the lockfile contract changes in a
// non-additive way. Version 3 replaces embedded raw source bodies with
// portable SHA-256 provenance.
const LockfileFormatVersion = "3"

// PortableRepoRoot is the only repository identity serialized into current
// lockfiles. Runtime binds it to the discovered checkout and verifies semantic
// identity through source_digest.
const PortableRepoRoot = "."

// LegacyLockfileSchemaV1 is accepted only during the format-1 migration.
const LegacyLockfileSchemaV1 = schema.LegacyPolicyLockURL

// DefaultLockfileSchema is the canonical JSON-schema URL recorded in every
// lockfile by default. Deployments can override the base via
// $RECONC_SCHEMA_BASE_URL; the reader still accepts this default.
const DefaultLockfileSchema = schema.PolicyLockURL

// LockfileSchema resolves the $schema URL to write into new lockfiles.
// Honors $RECONC_SCHEMA_BASE_URL (W24). Falls back to
// DefaultLockfileSchema when no override is set.
func LockfileSchema() string {
	return schema.Resolve(schema.PolicyLock)
}

// LockfileRelativePath is the repo-relative location of the compiled
// lockfile. Mirrored in ingest.LockfilePath for discovery.
const LockfileRelativePath = ".reconc/policy.lock.json"

// CompiledPolicy is the summary of a successful compile run plus the
// metadata needed to surface the result to the user.
type CompiledPolicy struct {
	RepoRoot        string                  `json:"repo_root"`
	LockfilePath    string                  `json:"lockfile_path"`
	CompilerVersion string                  `json:"compiler_version"`
	FormatVersion   string                  `json:"format_version"`
	SourceDigest    string                  `json:"source_digest"`
	DefaultMode     policy.Mode             `json:"default_mode"`
	RuleCount       int                     `json:"rule_count"`
	MCPToolCount    int                     `json:"mcp_tool_count"`
	SourceCount     int                     `json:"source_count"`
	SourcePaths     []string                `json:"source_paths"`
	CustomRuntimes  []customruntime.Summary `json:"custom_runtimes"`
	Warnings        []string                `json:"warnings"`
	// Conflicts lists static rule-pair inconsistencies detected at
	// compile time (duplicates, deny-vs-require contradictions, etc).
	// Empty slice when the ruleset is clean. Never nil.
	Conflicts []Conflict             `json:"conflicts"`
	Discovery ingest.DiscoveryResult `json:"discovery"`
}

// CompiledSource is the privacy-preserving source provenance serialized into
// a policy lock. Raw policy and instruction bodies never cross this boundary.
type CompiledSource struct {
	Kind          policy.SourceKind `json:"kind"`
	Path          string            `json:"path"`
	ContentSHA256 string            `json:"content_sha256"`
	BlockID       string            `json:"block_id,omitempty"`
	LineStart     int               `json:"line_start,omitempty"`
}

// CompileRepoPolicy is the public entrypoint. It runs the full
// pipeline (discover -> load -> parse -> serialize) and writes the
// lockfile under .reconc/policy.lock.json.
//
// Errors propagate as their typed values from the underlying layers
// (PolicySourceError from ingest, RuleValidationError from parser).
// File system writes wrap the underlying error in a generic error
// (the lockfile-write step is too narrow to warrant its own type).
//
// The compilerVersion string is recorded in the lockfile and surfaced
// in the returned CompiledPolicy so callers can show "compiled with
// reconc X.Y.Z" diagnostics.
func CompileRepoPolicy(repoStartPath, compilerVersion string) (compiled *CompiledPolicy, err error) {
	bundle, err := ingest.LoadPolicySources(repoStartPath)
	if err != nil {
		return nil, err
	}

	// Advisory compile lock (W35). Prevents two `reconc compile`
	// invocations on the same repo from racing on the lockfile.
	// Best-effort: if the repo is not yet discovered we skip locking
	// (there's nothing to protect and the error surface is already
	// handled below).
	if bundle.RepoRoot != "" {
		release, lockErr := AcquireCompileLock(bundle.RepoRoot)
		if lockErr != nil {
			return nil, lockErr
		}
		defer func() {
			if releaseErr := release(); releaseErr != nil {
				compiled = nil
				err = errors.Join(err, fmt.Errorf("release compile lock: %w", releaseErr))
			}
		}()
	}
	compiled, body, err := renderPolicyBundle(bundle, compilerVersion)
	if err != nil {
		return nil, err
	}
	if err := writeLockfile(compiled.RepoRoot, body); err != nil {
		return nil, err
	}
	return compiled, nil
}

// RenderRepoPolicy runs the complete policy compiler without taking the
// publication lock or writing the generated lockfile. Callers use the exact
// returned bytes when they need to include policy compilation in a larger
// transaction whose journal and publication boundary they own.
func RenderRepoPolicy(repoStartPath, compilerVersion string) (*CompiledPolicy, []byte, error) {
	bundle, err := ingest.LoadPolicySources(repoStartPath)
	if err != nil {
		return nil, nil, err
	}
	return renderPolicyBundle(bundle, compilerVersion)
}

func renderPolicyBundle(bundle *ingest.SourceBundle, compilerVersion string) (*CompiledPolicy, []byte, error) {
	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		return nil, nil, err
	}
	customRuntimes, err := compileCustomRuntimes(bundle, parsed.MCP)
	if err != nil {
		return nil, nil, err
	}

	root := bundle.RepoRoot
	digest, err := computeSourceDigest(bundle)
	if err != nil {
		return nil, nil, &rerrors.LockfileError{Message: "compute source digest", Cause: err}
	}

	// Normalize discovery for the post-compile world: the lockfile
	// will exist immediately after this run, and the "lockfile not
	// found" warning would otherwise be stale. Compute once and
	// thread it through both the lockfile JSON and the returned
	// CompiledPolicy so they agree (which keeps the lockfile bytes
	// stable across re-compiles).
	compiledDiscovery := stripLockfileMissingWarning(bundle.Discovery)
	lp := LockfileRelativePath
	compiledDiscovery.LockfilePath = &lp

	conflicts := DetectConflicts(parsed.Rules)
	if conflicts == nil {
		conflicts = []Conflict{}
	}

	// Surface deprecated rules as warnings so compile output always
	// reminds the author they're sitting on legacy rules. Warnings
	// don't fail the compile; removal is user-driven.
	for _, r := range parsed.Rules {
		if !r.Deprecated {
			continue
		}
		w := "rule '" + r.ID + "' is deprecated"
		if r.DeprecatedSince != "" {
			w += " (since " + r.DeprecatedSince + ")"
		}
		if r.DeprecatedReplacedBy != "" {
			w += "; replaced by '" + r.DeprecatedReplacedBy + "'"
		}
		if r.DeprecatedReason != "" {
			w += ": " + r.DeprecatedReason
		}
		compiledDiscovery.Warnings = append(compiledDiscovery.Warnings, w)
	}
	compiledDiscovery.Warnings = append(compiledDiscovery.Warnings, braceVariableWarnings(parsed.Rules)...)

	payload := buildLockPayload(bundle, parsed, digest, compilerVersion, compiledDiscovery, customRuntimes)
	lockDigest, err := ComputeLockDigest(payload)
	if err != nil {
		return nil, nil, &rerrors.LockfileError{Message: "compute lockfile digest", Cause: err}
	}
	payload["lock_digest"] = lockDigest

	body, err := encodeLockfile(payload)
	if err != nil {
		return nil, nil, err
	}

	return &CompiledPolicy{
		RepoRoot:        root,
		LockfilePath:    LockfileRelativePath,
		CompilerVersion: compilerVersion,
		FormatVersion:   LockfileFormatVersion,
		SourceDigest:    digest,
		DefaultMode:     parsed.DefaultMode,
		RuleCount:       len(parsed.Rules),
		MCPToolCount:    mcpToolCount(parsed.MCP),
		SourceCount:     len(bundle.Sources),
		SourcePaths:     sourcePathsOf(bundle.Sources),
		CustomRuntimes:  customRuntimes,
		Warnings:        compiledDiscovery.Warnings,
		Conflicts:       conflicts,
		Discovery:       compiledDiscovery,
	}, body, nil
}

// ComputeSourceDigest hashes a canonicalized JSON view of the source
// bundle. Canonicalization uses sorted keys and the compact separator
// form so byte-identical inputs produce byte-identical digests across
// platforms.
//
// Exported so the runtime evaluator can verify a lockfile's
// source_digest against the current source state during freshness
// validation.
func ComputeSourceDigest(bundle *ingest.SourceBundle) (string, error) {
	return computeSourceDigest(bundle)
}

// computeSourceDigest is the internal implementation; ComputeSourceDigest
// is the exported wrapper.
func computeSourceDigest(bundle *ingest.SourceBundle) (string, error) {
	sources := make([]interface{}, 0, len(bundle.Sources))
	for _, source := range bundle.Sources {
		sources = append(sources, sourceToMap(source))
	}
	return computeSerializedSourceDigest(sources)
}

func computeSerializedSourceDigest(sources []interface{}) (string, error) {
	canonical := map[string]interface{}{
		"source_precedence": stringifyKinds(policy.SourcePrecedence()),
		"sources":           sources,
	}
	data, err := marshalCanonical(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// stringifyKinds turns a []SourceKind into a []string so it serializes
// as a clean JSON array of plain strings rather than any custom kind
// type wrapping.
func stringifyKinds(kinds []policy.SourceKind) []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return out
}

// marshalCanonical produces compact, sorted-key JSON used for the
// digest input. Standard json.Marshal already sorts map keys; we just
// strip whitespace.
func marshalCanonical(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ComputeLockDigest returns the canonical SHA-256 digest of the complete
// lockfile payload except for the lock_digest field itself. It binds embedded
// rules and discovery metadata in addition to the independently verified
// policy-source digest.
func ComputeLockDigest(payload map[string]interface{}) (string, error) {
	canonical := make(map[string]interface{}, len(payload))
	for key, value := range payload {
		if key != "lock_digest" {
			canonical[key] = value
		}
	}
	data, err := marshalCanonical(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// ValidateEmbeddedRules binds the compiled rule payload back to the parsed
// policy sources. This preserves fail-closed behavior for legacy lockfiles
// that acquire a self-digest during an in-memory format migration.
func ValidateEmbeddedRules(payload map[string]interface{}, parsed *parser.ParsedPolicy) error {
	expected := make([]interface{}, 0, len(parsed.Rules))
	for _, rule := range parsed.Rules {
		expected = append(expected, ruleToMap(rule))
	}
	expectedData, err := marshalCanonical(expected)
	if err != nil {
		return &rerrors.LockfileError{Message: "encode rules parsed from current policy sources", Cause: err}
	}
	actualData, err := marshalCanonical(payload["rules"])
	if err != nil {
		return &rerrors.LockfileError{Message: "encode embedded lockfile rules", Cause: err}
	}
	if !bytes.Equal(actualData, expectedData) {
		return &rerrors.LockfileError{Message: "compiled lockfile rules do not match the current policy sources"}
	}
	expectedMCP := interface{}(nil)
	if parsed.MCP != nil {
		expectedMCP = mcpToMap(*parsed.MCP)
	}
	actualMCP, present := payload["mcp"]
	if parsed.MCP == nil && present {
		return &rerrors.LockfileError{Message: "compiled lockfile MCP contract does not match the current policy sources"}
	}
	if parsed.MCP != nil && !present {
		return &rerrors.LockfileError{Message: "compiled lockfile MCP contract does not match the current policy sources"}
	}
	expectedMCPData, err := marshalCanonical(expectedMCP)
	if err != nil {
		return &rerrors.LockfileError{Message: "encode MCP contract parsed from current policy sources", Cause: err}
	}
	actualMCPData, err := marshalCanonical(actualMCP)
	if err != nil {
		return &rerrors.LockfileError{Message: "encode embedded lockfile MCP contract", Cause: err}
	}
	if !bytes.Equal(actualMCPData, expectedMCPData) {
		return &rerrors.LockfileError{Message: "compiled lockfile MCP contract does not match the current policy sources"}
	}
	return nil
}

// buildLockPayload assembles the full lockfile object that gets
// written to disk. Field order doesn't matter for output (the writer
// uses sort_keys), but inclusion does.
//
// The discovery argument is the POST-compile-normalized discovery
// (lockfile path set, missing-lockfile warning stripped) so that
// re-compiles produce byte-identical lockfiles.
func buildLockPayload(
	bundle *ingest.SourceBundle,
	parsed *parser.ParsedPolicy,
	digest string,
	compilerVersion string,
	discovery ingest.DiscoveryResult,
	customRuntimes []customruntime.Summary,
) map[string]interface{} {
	rulesOut := make([]interface{}, 0, len(parsed.Rules))
	for _, r := range parsed.Rules {
		rulesOut = append(rulesOut, ruleToMap(r))
	}

	sourcesOut := make([]interface{}, 0, len(bundle.Sources))
	for _, s := range bundle.Sources {
		sourcesOut = append(sourcesOut, sourceToMap(s))
	}

	payload := map[string]interface{}{
		"$schema":           LockfileSchema(),
		"compiler_version":  compilerVersion,
		"format_version":    LockfileFormatVersion,
		"repo_root":         PortableRepoRoot,
		"default_mode":      string(parsed.DefaultMode),
		"rule_count":        len(parsed.Rules),
		"source_count":      len(bundle.Sources),
		"source_digest":     digest,
		"source_precedence": stringifyKinds(policy.SourcePrecedence()),
		"discovery":         discoveryToMap(discovery),
		"sources":           sourcesOut,
		"rules":             rulesOut,
	}
	if len(customRuntimes) > 0 {
		out := make([]interface{}, 0, len(customRuntimes))
		for _, summary := range customRuntimes {
			degradedRoutes := append([]string{}, summary.DegradedRoutes...)
			out = append(out, map[string]interface{}{
				"name": summary.Name, "runtime": summary.Runtime,
				"manifest_digest": summary.ManifestDigest, "route_count": summary.RouteCount,
				"degraded_routes": degradedRoutes,
			})
		}
		payload["custom_runtimes"] = out
	}
	if parsed.MCP != nil {
		payload["mcp"] = mcpToMap(*parsed.MCP)
	}
	return payload
}

func compileCustomRuntimes(bundle *ingest.SourceBundle, mcp *policy.MCPPolicy) ([]customruntime.Summary, error) {
	summaries := []customruntime.Summary{}
	configured := map[string]struct{}{}
	for _, source := range bundle.Sources {
		if source.Kind != policy.SourceCustomRuntime {
			continue
		}
		manifest, err := customruntime.DecodeManifest([]byte(source.Content))
		if err != nil {
			return nil, &rerrors.PolicySourceError{Message: "invalid custom runtime " + source.Path, Cause: err}
		}
		if filepath.Base(source.Path) != manifest.Name+".json" {
			return nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("custom runtime %s must be named %s.json", source.Path, manifest.Name)}
		}
		if _, duplicate := configured[manifest.Runtime()]; duplicate {
			return nil, &rerrors.PolicySourceError{Message: "duplicate custom runtime identity " + manifest.Runtime()}
		}
		configured[manifest.Runtime()] = struct{}{}
		summaries = append(summaries, manifest.Summary())
	}
	if mcp != nil {
		for _, tool := range mcp.Tools {
			platform := string(tool.Platform)
			if strings.HasPrefix(platform, "custom:") {
				if _, ok := configured[platform]; !ok {
					return nil, &rerrors.PolicySourceError{Message: "MCP selector references unconfigured custom runtime " + platform}
				}
			}
		}
	}
	return summaries, nil
}

func mcpToolCount(contract *policy.MCPPolicy) int {
	if contract == nil {
		return 0
	}
	return len(contract.Tools)
}

func mcpToMap(contract policy.MCPPolicy) map[string]interface{} {
	tools := policy.SortedMCPTools(contract.Tools)
	toolsOut := make([]interface{}, 0, len(tools))
	for _, tool := range tools {
		entry := map[string]interface{}{
			"platform": string(tool.Platform),
			"tool":     tool.Tool,
			"effect":   string(tool.Effect),
		}
		if tool.ServerFingerprint != "" {
			entry["server_fingerprint"] = tool.ServerFingerprint
		}
		if len(tool.PathFields) > 0 {
			entry["path_fields"] = append([]string(nil), tool.PathFields...)
		}
		if tool.CommandField != "" {
			entry["command_field"] = tool.CommandField
		}
		if tool.SourcePath != "" {
			entry["source_path"] = tool.SourcePath
		}
		toolsOut = append(toolsOut, entry)
	}
	return map[string]interface{}{
		"unclassified": string(contract.Unclassified),
		"tools":        toolsOut,
	}
}

// ruleToMap converts a Rule to a generic map so json.Marshal applies
// the sort-keys formatter consistently. Empty slice fields become
// JSON null... actually Go's encoder writes nil slices as null, which
// would diverge from Python's omitempty behavior. We strip the keys
// instead so the output matches the design's "omit empty optional
// fields" rule.
func ruleToMap(r policy.Rule) map[string]interface{} {
	m := map[string]interface{}{
		"id":      r.ID,
		"kind":    string(r.Kind),
		"message": r.Message,
	}
	if r.Mode != "" {
		m["mode"] = string(r.Mode)
	}
	if len(r.Paths) > 0 {
		m["paths"] = r.Paths
	}
	if len(r.BeforePaths) > 0 {
		m["before_paths"] = r.BeforePaths
	}
	if len(r.WhenPaths) > 0 {
		m["when_paths"] = r.WhenPaths
	}
	if len(r.Commands) > 0 {
		m["commands"] = r.Commands
	}
	if len(r.Claims) > 0 {
		m["claims"] = r.Claims
	}
	if r.CommandMatch != "" {
		m["command_match"] = string(r.CommandMatch)
	}
	if len(r.RequiredFiles) > 0 {
		out := make([]interface{}, len(r.RequiredFiles))
		for i, rf := range r.RequiredFiles {
			entry := map[string]interface{}{"path": rf.Path}
			if rf.MaxAgeHours > 0 {
				entry["max_age_hours"] = rf.MaxAgeHours
			}
			if rf.Optional {
				entry["optional"] = true
			}
			out[i] = entry
		}
		m["required_files"] = out
	}
	if len(r.Evidence) > 0 {
		out := make([]interface{}, len(r.Evidence))
		for i, e := range r.Evidence {
			entry := map[string]interface{}{"file": e.File}
			if e.MustExist {
				entry["must_exist"] = true
			}
			if len(e.MustContain) > 0 {
				entry["must_contain"] = e.MustContain
			}
			if e.MustNotContain != "" {
				entry["must_not_contain"] = e.MustNotContain
			}
			if e.MaxLineCount > 0 {
				entry["max_line_count"] = e.MaxLineCount
			}
			if e.Optional {
				entry["optional"] = true
			}
			out[i] = entry
		}
		m["evidence"] = out
	}
	if len(r.Checks) > 0 {
		out := make([]interface{}, len(r.Checks))
		for i, c := range r.Checks {
			out[i] = checkToMap(c)
		}
		m["checks"] = out
	}
	if r.Script != "" {
		m["script"] = r.Script
	}
	if len(r.Args) > 0 {
		m["args"] = r.Args
	}
	if r.TimeoutSec > 0 {
		m["timeout_sec"] = r.TimeoutSec
	}
	if r.KillTimeoutSec > 0 {
		m["kill_timeout_sec"] = r.KillTimeoutSec
	}
	if len(r.Assurance) > 0 {
		out := make([]interface{}, len(r.Assurance))
		for i, gate := range r.Assurance {
			out[i] = assuranceGateToMap(gate)
		}
		m["assurance"] = out
	}
	if r.SourcePath != "" {
		m["source_path"] = r.SourcePath
	}
	if r.SourceBlockID != "" {
		m["source_block_id"] = r.SourceBlockID
	}
	// W31 deprecation fields -- only serialised when the rule is
	// actually deprecated (keeps clean rules' lockfile entries
	// unchanged and preserves byte-stability).
	if r.Deprecated {
		m["deprecated"] = true
		if r.DeprecatedReason != "" {
			m["deprecated_reason"] = r.DeprecatedReason
		}
		if r.DeprecatedSince != "" {
			m["deprecated_since"] = r.DeprecatedSince
		}
		if r.DeprecatedReplacedBy != "" {
			m["deprecated_replaced_by"] = r.DeprecatedReplacedBy
		}
	}
	// W17 monorepo scope fields -- only emitted when the rule was
	// declared inside a `scopes:` block. Global rules look exactly
	// the same as before, preserving lockfile byte stability for
	// non-monorepo repos.
	if len(r.ScopePaths) > 0 {
		m["scope_paths"] = r.ScopePaths
	}
	if r.ScopeID != "" {
		m["scope_id"] = r.ScopeID
	}
	return m
}

func assuranceGateToMap(g policy.AssuranceGate) map[string]interface{} {
	m := map[string]interface{}{"id": g.ID, "type": string(g.Type)}
	putStrings := func(key string, values []string) {
		if len(values) > 0 {
			m[key] = values
		}
	}
	putStrings("applicable_if", g.ApplicableIf)
	putStrings("scan_paths", g.ScanPaths)
	putStrings("exclude_paths", g.ExcludePaths)
	if len(g.Exemptions) > 0 {
		items := make([]interface{}, len(g.Exemptions))
		for i, exemption := range g.Exemptions {
			items[i] = map[string]interface{}{"path": exemption.Path, "reason": exemption.Reason}
		}
		m["exemptions"] = items
	}
	putStrings("allowed_root_entries", g.AllowedRootEntries)
	putStrings("required_root_entries", g.RequiredRootEntries)
	putStrings("forbidden_root_entries", g.ForbiddenRootEntries)
	putStrings("reserved_dirs", g.ReservedDirs)
	if g.AllowHiddenEntries {
		m["allow_hidden_entries"] = true
	}
	putStrings("allowed_extensions", g.AllowedExtensions)
	putStrings("manifest_paths", g.ManifestPaths)
	putStrings("manifest_markers", g.ManifestMarkers)
	putStrings("dependency_sections", g.DependencySections)
	putStrings("allowed_version_prefixes", g.AllowedVersionPrefixes)
	if g.PackageManager != "" {
		m["package_manager"] = g.PackageManager
	}
	putStrings("site_patterns", g.SitePatterns)
	putStrings("guard_markers", g.GuardMarkers)
	if g.MarkerWindowLines > 0 {
		m["marker_window_lines"] = g.MarkerWindowLines
	}
	putStrings("commands", g.Commands)
	if g.CommandPolicy != "" {
		m["command_policy"] = g.CommandPolicy
	}
	if g.ProofFile != "" {
		m["proof_file"] = g.ProofFile
	}
	if g.MinSamples > 0 {
		m["min_samples"] = g.MinSamples
	}
	if g.MaxAgeHours > 0 {
		m["max_age_hours"] = g.MaxAgeHours
	}
	return m
}

// checkToMap serializes one composite-rule sub-check, omitting fields
// that aren't relevant for its kind (omitempty contract).
func checkToMap(c policy.Check) map[string]interface{} {
	m := map[string]interface{}{"kind": string(c.Kind)}
	if c.Path != "" {
		m["path"] = c.Path
	}
	if c.MaxAgeHours > 0 {
		m["max_age_hours"] = c.MaxAgeHours
	}
	if c.File != "" {
		m["file"] = c.File
	}
	if c.MustExist {
		m["must_exist"] = true
	}
	if len(c.MustContain) > 0 {
		m["must_contain"] = c.MustContain
	}
	if c.MustNotContain != "" {
		m["must_not_contain"] = c.MustNotContain
	}
	if c.MaxLineCount > 0 {
		m["max_line_count"] = c.MaxLineCount
	}
	if c.Script != "" {
		m["script"] = c.Script
	}
	if len(c.Args) > 0 {
		m["args"] = c.Args
	}
	if c.TimeoutSec > 0 {
		m["timeout_sec"] = c.TimeoutSec
	}
	if len(c.Paths) > 0 {
		m["paths"] = c.Paths
	}
	if len(c.BeforePaths) > 0 {
		m["before_paths"] = c.BeforePaths
	}
	if len(c.WhenPaths) > 0 {
		m["when_paths"] = c.WhenPaths
	}
	if len(c.Commands) > 0 {
		m["commands"] = c.Commands
	}
	if len(c.Claims) > 0 {
		m["claims"] = c.Claims
	}
	if c.CommandMatch != "" {
		m["command_match"] = string(c.CommandMatch)
	}
	if c.Optional {
		m["optional"] = true
	}
	return m
}

// sourceToMap converts private source input into public provenance.
func sourceToMap(s policy.PolicySource) map[string]interface{} {
	contentDigest := sha256.Sum256([]byte(s.Content))
	m := map[string]interface{}{
		"kind":           string(s.Kind),
		"path":           s.Path,
		"content_sha256": hex.EncodeToString(contentDigest[:]),
	}
	if s.BlockID != "" {
		m["block_id"] = s.BlockID
	}
	if s.LineStart != 0 {
		m["line_start"] = s.LineStart
	}
	return m
}

// discoveryToMap renders the discovery result with pointer fields expanded
// (nil -> absent key) for a stable lockfile shape.
func discoveryToMap(d ingest.DiscoveryResult) map[string]interface{} {
	m := map[string]interface{}{
		"start_path":        PortableRepoRoot,
		"repo_root":         PortableRepoRoot,
		"discovered":        d.Discovered,
		"config_candidates": d.ConfigCandidates,
		"policy_paths":      d.PolicyPaths,
		"warnings":          d.Warnings,
	}
	if d.ClaudePath != nil {
		m["claude_path"] = *d.ClaudePath
	}
	if d.AgentsPath != nil {
		m["agents_path"] = *d.AgentsPath
	}
	if d.StartMDPath != nil {
		m["start_md_path"] = *d.StartMDPath
	}
	if d.ConfigPath != nil {
		m["config_path"] = *d.ConfigPath
	}
	if d.LockfilePath != nil {
		m["lockfile_path"] = *d.LockfilePath
	}
	return m
}

// ValidateLockfileEnvelope verifies current lockfile metadata independent of
// policy contents. Callers must migrate the payload before invoking it.
func ValidateLockfileEnvelope(payload map[string]interface{}) error {
	formatVersion, _ := payload["format_version"].(string)
	if formatVersion != LockfileFormatVersion {
		return &rerrors.LockfileError{Message: "compiled lockfile format_version does not match this checker; re-run `reconc refresh`"}
	}
	schemaURL, _ := payload["$schema"].(string)
	if schemaURL != DefaultLockfileSchema && schemaURL != LockfileSchema() {
		return &rerrors.LockfileError{Message: "compiled lockfile schema does not match this checker; re-run `reconc refresh`"}
	}
	if root, _ := payload["repo_root"].(string); root != PortableRepoRoot {
		return &rerrors.LockfileError{Message: "compiled lockfile repo_root must use the portable '.' marker; re-run `reconc refresh`"}
	}
	discovery, ok := payload["discovery"].(map[string]interface{})
	if !ok {
		return &rerrors.LockfileError{Message: "compiled lockfile discovery must contain an object; re-run `reconc refresh`"}
	}
	for _, field := range []string{"repo_root", "start_path"} {
		if value, _ := discovery[field].(string); value != PortableRepoRoot {
			return &rerrors.LockfileError{Message: "compiled lockfile discovery." + field + " must use the portable '.' marker; re-run `reconc refresh`"}
		}
	}
	if err := validateCompiledSources(payload); err != nil {
		return err
	}
	storedDigest, _ := payload["lock_digest"].(string)
	decodedDigest, err := hex.DecodeString(storedDigest)
	if err != nil || len(decodedDigest) != sha256.Size {
		return &rerrors.LockfileError{Message: "compiled lockfile lock_digest is missing or invalid; re-run `reconc refresh`"}
	}
	computedDigest, err := ComputeLockDigest(payload)
	if err != nil {
		return &rerrors.LockfileError{Message: "compute compiled lockfile digest", Cause: err}
	}
	if storedDigest != computedDigest {
		return &rerrors.LockfileError{Message: "compiled lockfile payload digest does not match its contents; re-run `reconc refresh`"}
	}
	return nil
}

func validateCompiledSources(payload map[string]interface{}) error {
	raw, ok := payload["sources"].([]interface{})
	if !ok {
		return &rerrors.LockfileError{Message: "compiled lockfile sources must contain a list; re-run `reconc refresh`"}
	}
	for index, item := range raw {
		source, ok := item.(map[string]interface{})
		if !ok {
			return &rerrors.LockfileError{Message: fmt.Sprintf("compiled lockfile sources[%d] must contain an object; re-run `reconc refresh`", index)}
		}
		kind, _ := source["kind"].(string)
		if !sourceKindValid(policy.SourceKind(kind)) {
			return &rerrors.LockfileError{Message: fmt.Sprintf("compiled lockfile sources[%d].kind is invalid; re-run `reconc refresh`", index)}
		}
		sourcePath, _ := source["path"].(string)
		if !portableSourcePath(sourcePath) {
			return &rerrors.LockfileError{Message: fmt.Sprintf("compiled lockfile sources[%d].path must be portable; re-run `reconc refresh`", index)}
		}
		digest, _ := source["content_sha256"].(string)
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return &rerrors.LockfileError{Message: fmt.Sprintf("compiled lockfile sources[%d].content_sha256 is invalid; re-run `reconc refresh`", index)}
		}
		if _, leaked := source["content"]; leaked {
			return &rerrors.LockfileError{Message: fmt.Sprintf("compiled lockfile sources[%d] contains a raw source body; re-run `reconc refresh`", index)}
		}
	}
	return nil
}

var windowsAbsolutePathPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func portableSourcePath(value string) bool {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, `\`) || windowsAbsolutePathPattern.MatchString(cleaned) {
		return false
	}
	for _, component := range strings.FieldsFunc(cleaned, func(r rune) bool { return r == '/' || r == '\\' }) {
		if component == ".." {
			return false
		}
	}
	return true
}

func sourceKindValid(kind policy.SourceKind) bool {
	if kind == policy.SourceCustomRuntime {
		return true
	}
	for _, candidate := range policy.SourcePrecedence() {
		if kind == candidate {
			return true
		}
	}
	return false
}

// writeLockfile materializes payload at $repoRoot/.reconc/policy.lock.json
// with sorted keys (Go's json.Marshal already does this for maps) and
// 2-space indentation, terminated by a single newline.
//
// MkdirAll handles a missing .reconc/ directory; existing files are replaced
// with temp-file-then-rename so readers never observe a truncated lockfile.
func encodeLockfile(payload map[string]interface{}) ([]byte, error) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "marshal lockfile", Cause: err}
	}
	return append(data, '\n'), nil
}

func writeLockfile(repoRoot string, body []byte) error {
	lockDir := filepath.Join(repoRoot, ".reconc")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return &rerrors.LockfileError{Message: "create .reconc/", Cause: err}
	}
	full := filepath.Join(lockDir, "policy.lock.json")
	if _, err := atomicfile.WriteIfChanged(full, body, 0o644); err != nil {
		return &rerrors.LockfileError{Message: "write lockfile", Cause: err}
	}
	return nil
}

// stripLockfileMissingWarning returns a copy of the discovery result
// with the "compiled lockfile not found" warning removed, since after
// a successful compile that warning is stale.
func stripLockfileMissingWarning(d ingest.DiscoveryResult) ingest.DiscoveryResult {
	out := d
	out.Warnings = make([]string, 0, len(d.Warnings))
	for _, w := range d.Warnings {
		if strings.Contains(w, "lockfile not found") {
			continue
		}
		out.Warnings = append(out.Warnings, w)
	}
	return out
}

// braceVariableRegex matches a single-identifier brace group like
// {task_id}. Alternations such as {js,ts} contain a comma and never
// match.
var braceVariableRegex = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// templateCaptureKinds are the rule kinds whose glob fields compile
// {var} patterns into template captures. Every other kind matches globs
// through doublestar, where {task_id} is a one-element brace group
// matching the LITERAL text "task_id" - almost never what the author
// meant.
var templateCaptureKinds = map[policy.Kind]struct{}{
	policy.KindRequireFreshFile: {},
	policy.KindRequireEvidence:  {},
	policy.KindRequireScript:    {},
	policy.KindAllOf:            {},
	policy.KindAnyOf:            {},
	policy.KindNot:              {},
}

// braceVariableWarnings flags {identifier} brace groups in glob fields
// of kinds without template capture, so a misauthored rule fails loud
// at compile time instead of silently matching one literal path.
func braceVariableWarnings(rules []policy.Rule) []string {
	type globField struct {
		name     string
		patterns []string
	}
	warnings := []string{}
	for _, r := range rules {
		fields := []globField{{"scope_paths", r.ScopePaths}}
		if _, captures := templateCaptureKinds[r.Kind]; !captures {
			fields = append(fields,
				globField{"when_paths", r.WhenPaths},
				globField{"paths", r.Paths},
				globField{"before_paths", r.BeforePaths},
			)
		}
		for _, field := range fields {
			for _, pattern := range field.patterns {
				if match := braceVariableRegex.FindString(pattern); match != "" {
					warnings = append(warnings, fmt.Sprintf(
						"rule '%s': %s pattern %q uses %s, but kind %s does not capture template variables; doublestar matches it as the literal text %q",
						r.ID, field.name, pattern, match, r.Kind, strings.Trim(match, "{}")))
				}
			}
		}
	}
	return warnings
}

// sourcePathsOf returns the path strings of every source in the bundle.
// Used for surfacing "what got compiled" in the CompiledPolicy.
func sourcePathsOf(sources []policy.PolicySource) []string {
	out := make([]string, len(sources))
	for i, s := range sources {
		out[i] = s.Path
	}
	return out
}
