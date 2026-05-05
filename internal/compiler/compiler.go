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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
)

// LockfileFormatVersion is bumped whenever lockfile JSON changes shape
// in a non-additive way. Stays at "1" through W9-W51.
const LockfileFormatVersion = "1"

// DefaultLockfileSchema is the canonical JSON-schema URL recorded in every
// lockfile by default. Deployments can override the base via
// $RECONC_SCHEMA_BASE_URL; the reader still accepts this default.
const DefaultLockfileSchema = "https://reconc.dev/schemas/policy-lock/v1"

// LockfileSchema resolves the $schema URL to write into new lockfiles.
// Honors $RECONC_SCHEMA_BASE_URL (W24). Falls back to
// DefaultLockfileSchema when no override is set.
func LockfileSchema() string {
	if base := os.Getenv("RECONC_SCHEMA_BASE_URL"); base != "" {
		return strings.TrimRight(base, "/") + "/schemas/policy-lock/v1"
	}
	return DefaultLockfileSchema
}

// LockfileRelativePath is the repo-relative location of the compiled
// lockfile. Mirrored in ingest.LockfilePath for discovery.
const LockfileRelativePath = ".reconc/policy.lock.json"

// CompiledPolicy is the summary of a successful compile run plus the
// metadata needed to surface the result to the user.
type CompiledPolicy struct {
	RepoRoot        string      `json:"repo_root"`
	LockfilePath    string      `json:"lockfile_path"`
	CompilerVersion string      `json:"compiler_version"`
	FormatVersion   string      `json:"format_version"`
	SourceDigest    string      `json:"source_digest"`
	DefaultMode     policy.Mode `json:"default_mode"`
	RuleCount       int         `json:"rule_count"`
	SourceCount     int         `json:"source_count"`
	SourcePaths     []string    `json:"source_paths"`
	Warnings        []string    `json:"warnings"`
	// Conflicts lists static rule-pair inconsistencies detected at
	// compile time (duplicates, deny-vs-require contradictions, etc).
	// Empty slice when the ruleset is clean. Never nil.
	Conflicts []Conflict             `json:"conflicts"`
	Discovery ingest.DiscoveryResult `json:"discovery"`
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
func CompileRepoPolicy(repoStartPath, compilerVersion string) (*CompiledPolicy, error) {
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
		defer release()
	}
	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		return nil, err
	}

	root := bundle.RepoRoot
	digest := computeSourceDigest(bundle)

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

	payload := buildLockPayload(root, bundle, parsed, digest, compilerVersion, compiledDiscovery)

	if err := writeLockfile(root, payload); err != nil {
		return nil, err
	}

	return &CompiledPolicy{
		RepoRoot:        root,
		LockfilePath:    LockfileRelativePath,
		CompilerVersion: compilerVersion,
		FormatVersion:   LockfileFormatVersion,
		SourceDigest:    digest,
		DefaultMode:     parsed.DefaultMode,
		RuleCount:       len(parsed.Rules),
		SourceCount:     len(bundle.Sources),
		SourcePaths:     sourcePathsOf(bundle.Sources),
		Warnings:        compiledDiscovery.Warnings,
		Conflicts:       conflicts,
		Discovery:       compiledDiscovery,
	}, nil
}

// ComputeSourceDigest hashes a canonicalized JSON view of the source
// bundle. Canonicalization uses sorted keys and the compact separator
// form so byte-identical inputs produce byte-identical digests across
// platforms.
//
// Exported so the runtime evaluator can verify a lockfile's
// source_digest against the current source state during freshness
// validation.
func ComputeSourceDigest(bundle *ingest.SourceBundle) string {
	return computeSourceDigest(bundle)
}

// computeSourceDigest is the internal implementation; ComputeSourceDigest
// is the exported wrapper.
func computeSourceDigest(bundle *ingest.SourceBundle) string {
	canonical := map[string]interface{}{
		"source_precedence": stringifyKinds(policy.SourcePrecedence()),
		"sources":           bundle.Sources,
	}
	data, err := marshalCanonical(canonical)
	if err != nil {
		// Internal error: bundle structures are guaranteed-marshalable.
		// Returning empty digest would silently corrupt the lockfile;
		// panic surfaces the bug in tests.
		panic("internal: source digest marshal failed: " + err.Error())
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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

// buildLockPayload assembles the full lockfile object that gets
// written to disk. Field order doesn't matter for output (the writer
// uses sort_keys), but inclusion does.
//
// The discovery argument is the POST-compile-normalized discovery
// (lockfile path set, missing-lockfile warning stripped) so that
// re-compiles produce byte-identical lockfiles.
func buildLockPayload(
	repoRoot string,
	bundle *ingest.SourceBundle,
	parsed *parser.ParsedPolicy,
	digest string,
	compilerVersion string,
	discovery ingest.DiscoveryResult,
) map[string]interface{} {
	rulesOut := make([]interface{}, 0, len(parsed.Rules))
	for _, r := range parsed.Rules {
		rulesOut = append(rulesOut, ruleToMap(r))
	}

	sourcesOut := make([]interface{}, 0, len(bundle.Sources))
	for _, s := range bundle.Sources {
		sourcesOut = append(sourcesOut, sourceToMap(s))
	}

	return map[string]interface{}{
		"$schema":           LockfileSchema(),
		"compiler_version":  compilerVersion,
		"format_version":    LockfileFormatVersion,
		"repo_root":         repoRoot,
		"default_mode":      string(parsed.DefaultMode),
		"rule_count":        len(parsed.Rules),
		"source_count":      len(bundle.Sources),
		"source_digest":     digest,
		"source_precedence": stringifyKinds(policy.SourcePrecedence()),
		"discovery":         discoveryToMap(discovery),
		"sources":           sourcesOut,
		"rules":             rulesOut,
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
	if c.Optional {
		m["optional"] = true
	}
	return m
}

// sourceToMap mirrors ruleToMap for PolicySource: include only fields
// that are populated to keep the lockfile compact.
func sourceToMap(s policy.PolicySource) map[string]interface{} {
	m := map[string]interface{}{
		"kind":    string(s.Kind),
		"path":    s.Path,
		"content": s.Content,
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
		"start_path":        d.StartPath,
		"repo_root":         d.RepoRoot,
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

// writeLockfile materializes payload at $repoRoot/.reconc/policy.lock.json
// with sorted keys (Go's json.Marshal already does this for maps) and
// 2-space indentation, terminated by a single newline.
//
// MkdirAll handles a missing .reconc/ directory; existing files are
// overwritten atomically (via os.WriteFile's truncate-and-write).
func writeLockfile(repoRoot string, payload map[string]interface{}) error {
	lockDir := filepath.Join(repoRoot, ".reconc")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return &rerrors.LockfileError{Message: "create .reconc/", Cause: err}
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return &rerrors.LockfileError{Message: "marshal lockfile", Cause: err}
	}
	full := filepath.Join(lockDir, "policy.lock.json")
	if err := os.WriteFile(full, append(data, '\n'), 0o644); err != nil {
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

// sourcePathsOf returns the path strings of every source in the bundle.
// Used for surfacing "what got compiled" in the CompiledPolicy.
func sourcePathsOf(sources []policy.PolicySource) []string {
	out := make([]string, len(sources))
	for i, s := range sources {
		out[i] = s.Path
	}
	return out
}
