// Package ingest is the first stage of the compile pipeline. Given a
// path inside (or at) a repo, it walks up until it finds a reconc
// "policy marker" and returns a DiscoveryResult describing what was
// found and what is missing.
//
// Policy markers (any of which makes a directory count as a repo root):
//
//   - AGENTS.md
//   - start.md
//   - CLAUDE.md (legacy, still recognized)
//   - .reconc.yml / .reconc.yaml
//   - policies/*.yml or policies/*.yaml
//
// Discovery is pure: no network, no global state, no mutation. It is
// the same predicate all CLI commands agree on so doctor, compile,
// check, ci, explain, fix, etc. all land on the same repo root.
package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ConfigCandidates is the accepted compiler-config filenames (preferred
// first). A repo may have at most one; multiple triggers a warning.
var ConfigCandidates = []string{".reconc.yml", ".reconc.yaml"}

// DefaultPolicyGlobs is the list of glob patterns scanned for additional
// policy fragment files beneath the discovered repo root.
var DefaultPolicyGlobs = []string{"policies/*.yml", "policies/*.yaml"}

// LockfilePath is the repo-relative path where the compiled lockfile lives.
const LockfilePath = ".reconc/policy.lock.json"

// DiscoveryResult is the full discovery outcome.
//
// Pointer fields are nil when the corresponding marker was not found;
// this distinguishes "not present" from "present but empty".
type DiscoveryResult struct {
	// StartPath is the absolute path discovery was initiated from.
	StartPath string `json:"start_path"`
	// RepoRoot is the nearest ancestor directory containing a policy
	// marker (or the original start path if nothing was found).
	RepoRoot string `json:"repo_root"`
	// Discovered is true when at least one policy marker was located.
	Discovered bool `json:"discovered"`
	// ClaudePath, AgentsPath, StartMDPath, ConfigPath are repo-relative
	// (or nil if not present).
	ClaudePath   *string `json:"claude_path,omitempty"`
	AgentsPath   *string `json:"agents_path,omitempty"`
	StartMDPath  *string `json:"start_md_path,omitempty"`
	ConfigPath   *string `json:"config_path,omitempty"`
	LockfilePath *string `json:"lockfile_path,omitempty"`

	// ConfigCandidates lists every compiler-config file actually present
	// (in ConfigCandidates order). Multiple = warning.
	ConfigCandidates []string `json:"config_candidates"`

	// PolicyPaths lists every fragment file matched by DefaultPolicyGlobs.
	PolicyPaths []string `json:"policy_paths"`

	// Warnings surfaces actionable drift (missing lockfile, missing
	// policy fragments, multiple config files, etc.) without failing.
	Warnings []string `json:"warnings"`
}

// DiscoverPolicyRepo walks up from startPath until a policy marker is
// found, or the filesystem root is reached.
//
// Returns (result, nil) when discovery succeeds (even if Discovered is
// false). Returns (zero, error) only for IO-level failures where
// discovery could not even be attempted (e.g. startPath does not exist).
func DiscoverPolicyRepo(startPath string) (DiscoveryResult, error) {
	abs, err := filepath.Abs(startPath)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("resolve start path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("start path not found: %s: %w", abs, err)
	}
	cursor := abs
	if !info.IsDir() {
		cursor = filepath.Dir(abs)
	}

	for {
		result, found := inspectDirectory(cursor, abs)
		if found {
			return result, nil
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			// reached filesystem root; no marker found
			return DiscoveryResult{
				StartPath:        abs,
				RepoRoot:         cursor,
				Discovered:       false,
				ConfigCandidates: []string{},
				PolicyPaths:      []string{},
				Warnings: []string{
					"no CLAUDE.md, AGENTS.md, start.md, compiler config, or policy fragments found while searching parent directories",
				},
			}, nil
		}
		cursor = parent
	}
}

// inspectDirectory checks one directory for policy markers. Returns
// (result, true) when at least one marker was found; (zero, false) when
// none were present.
func inspectDirectory(dir, originalStart string) (DiscoveryResult, bool) {
	claude := filepathIfRegular(dir, "CLAUDE.md")
	agents := filepathIfRegular(dir, "AGENTS.md")
	startMD := filepathIfRegular(dir, "start.md")

	configs := []string{}
	for _, name := range ConfigCandidates {
		if isRegularFile(filepath.Join(dir, name)) {
			configs = append(configs, name)
		}
	}

	policies := listPolicyFragments(dir)

	hasMarker := claude != nil || agents != nil || startMD != nil ||
		len(configs) > 0 || len(policies) > 0
	if !hasMarker {
		return DiscoveryResult{}, false
	}

	lockfile := filepath.Join(dir, LockfilePath)
	var lockfilePath *string
	if isRegularFile(lockfile) {
		p := LockfilePath
		lockfilePath = &p
	}

	warnings := []string{}
	if len(configs) > 1 {
		warnings = append(warnings, "multiple compiler config files found; using .reconc.yml precedence")
	}
	if claude == nil && agents == nil && startMD == nil {
		warnings = append(warnings, "no CLAUDE.md / AGENTS.md / start.md entry file found; compiling config and policy fragments only")
	}
	if len(policies) == 0 {
		warnings = append(warnings, "no policy fragments discovered under policies/*.yml or policies/*.yaml")
	}
	if lockfilePath == nil {
		warnings = append(warnings, "compiled lockfile not found at "+LockfilePath)
	}

	var preferredConfig *string
	if len(configs) > 0 {
		c := configs[0]
		preferredConfig = &c
	}

	return DiscoveryResult{
		StartPath:        originalStart,
		RepoRoot:         dir,
		Discovered:       true,
		ClaudePath:       claude,
		AgentsPath:       agents,
		StartMDPath:      startMD,
		ConfigPath:       preferredConfig,
		ConfigCandidates: configs,
		PolicyPaths:      policies,
		LockfilePath:     lockfilePath,
		Warnings:         warnings,
	}, true
}

// filepathIfRegular returns a pointer to name (not a full path) when the
// file exists and is regular; nil otherwise. The returned string is
// repo-relative by convention so callers can render it without leaking
// the absolute path.
func filepathIfRegular(dir, name string) *string {
	if isRegularFile(filepath.Join(dir, name)) {
		out := name
		return &out
	}
	return nil
}

// isRegularFile reports whether path exists and is a regular file.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// listPolicyFragments returns the repo-relative (POSIX-style) paths of
// every file matching DefaultPolicyGlobs under dir. Sorted for
// deterministic output.
func listPolicyFragments(dir string) []string {
	seen := map[string]struct{}{}
	for _, pattern := range DefaultPolicyGlobs {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			continue // malformed glob is a programmer bug; skip silently
		}
		for _, m := range matches {
			if !isRegularFile(m) {
				continue
			}
			rel, err := filepath.Rel(dir, m)
			if err != nil {
				continue
			}
			seen[filepath.ToSlash(rel)] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
