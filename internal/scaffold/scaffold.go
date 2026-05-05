// Package scaffold writes a minimal reconc policy configuration into
// a fresh repo.
//
// The scaffolder is the implementation behind `reconc init`. It is
// deliberately conservative: it never overwrites an existing
// .reconc.yml unless the caller passes Force=true, and it never
// overwrites AGENTS.md (or CLAUDE.md) under any circumstances.
//
// Uses typed results, explicit error returns, and no hidden global state.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/presets"
)

// CompilerConfigFilename is the canonical name of the per-repo policy
// config that `reconc init` writes.
const CompilerConfigFilename = ".reconc.yml"

// AgentsFilename is the cross-platform agent context file. Reconc
// writes a minimal stub if no entry file exists at all (CLAUDE.md /
// AGENTS.md / start.md).
const AgentsFilename = "AGENTS.md"

// Report describes the outcome of one Initialize() call. Every path
// recorded is repo-relative POSIX so JSON output is stable across
// platforms.
type Report struct {
	RepoRoot   string   `json:"repo_root"`
	Presets    []string `json:"presets"`
	Created    []string `json:"created"`
	Updated    []string `json:"updated"`
	Skipped    []string `json:"skipped"`
	NextAction string   `json:"next_action"`
}

// Options controls Initialize behavior.
type Options struct {
	// Presets is the list of bundled (or user) presets to include in
	// the generated config's `extends:` block. Empty defaults to
	// ["default", "agent"].
	Presets []string
	// Force overwrites an existing .reconc.yml. AGENTS.md is never
	// overwritten regardless of this flag.
	Force bool
}

// Initialize scaffolds .reconc.yml (and a stub AGENTS.md if no entry
// file exists) inside repoRoot.
//
// Validation rules:
//   - Every preset name in opts.Presets must resolve via presets.List()
//   - Empty preset names are rejected
//   - Existing .reconc.yml triggers an error unless opts.Force is true
//
// Returns *PolicySourceError on validation failure or IO failure;
// successful run returns a populated *Report.
func Initialize(repoRoot string, opts Options) (*Report, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("repo path does not exist: %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repo path is not a directory: %s", root)
	}

	requested := opts.Presets
	if len(requested) == 0 {
		requested = []string{"default", "agent"}
	}
	resolved, err := validatePresets(requested)
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(root, CompilerConfigFilename)
	agentsPath := filepath.Join(root, AgentsFilename)
	claudePath := filepath.Join(root, "CLAUDE.md")
	startPath := filepath.Join(root, "start.md")

	report := &Report{
		RepoRoot: root,
		Presets:  resolved,
		Created:  []string{},
		Updated:  []string{},
		Skipped:  []string{},
	}

	configContent := renderCompilerConfig(resolved)
	if exists, regular := pathState(configPath); exists {
		if !regular {
			return nil, &rerrors.PolicySourceError{
				Message: CompilerConfigFilename + " exists at " + configPath + " but is not a regular file",
			}
		}
		if !opts.Force {
			return nil, &rerrors.PolicySourceError{
				Message: CompilerConfigFilename + " already exists at " + configPath +
					"; pass Force=true (or `--force` from the CLI) to overwrite",
			}
		}
		if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
			return nil, &rerrors.PolicySourceError{Message: "write " + CompilerConfigFilename, Cause: err}
		}
		report.Updated = append(report.Updated, CompilerConfigFilename)
	} else {
		if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
			return nil, &rerrors.PolicySourceError{Message: "write " + CompilerConfigFilename, Cause: err}
		}
		report.Created = append(report.Created, CompilerConfigFilename)
	}

	// Entry-file stub: only write AGENTS.md when no entry file is
	// present at all (legacy CLAUDE.md or start.md count as entries).
	hasEntry := exists(claudePath) || exists(agentsPath) || exists(startPath)
	switch {
	case exists(agentsPath):
		report.Skipped = append(report.Skipped, AgentsFilename)
	case hasEntry:
		// CLAUDE.md or start.md already present; don't add a redundant AGENTS.md.
		report.Skipped = append(report.Skipped, AgentsFilename)
	default:
		stub := renderAgentsStub(filepath.Base(root))
		if err := os.WriteFile(agentsPath, []byte(stub), 0o644); err != nil {
			return nil, &rerrors.PolicySourceError{Message: "write " + AgentsFilename, Cause: err}
		}
		report.Created = append(report.Created, AgentsFilename)
	}

	sort.Strings(report.Created)
	sort.Strings(report.Updated)
	sort.Strings(report.Skipped)
	report.NextAction = "Run `reconc compile " + root + "` to build the lockfile from the new config."
	return report, nil
}

// validatePresets confirms every preset name resolves and dedupes the
// resulting list. The order of resolution mirrors caller intent.
func validatePresets(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, &rerrors.PolicySourceError{
			Message: "at least one preset name is required (default: ['default'])",
		}
	}
	available, err := presets.List()
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "list bundled/user presets", Cause: err}
	}
	availSet := map[string]struct{}{}
	availNames := []string{}
	for _, p := range available {
		availSet[p.Name] = struct{}{}
		availNames = append(availNames, p.Name)
	}
	sort.Strings(availNames)

	out := []string{}
	seen := map[string]struct{}{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, &rerrors.PolicySourceError{
				Message: "preset names must be non-empty strings",
			}
		}
		if _, ok := availSet[name]; !ok {
			return nil, &rerrors.PolicySourceError{
				Message: "preset '" + name + "' is not available; known: " + strings.Join(availNames, ", "),
			}
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

func renderCompilerConfig(extends []string) string {
	var b strings.Builder
	b.WriteString("# Generated by `reconc init`.\n")
	b.WriteString("#\n")
	b.WriteString("# `extends:` merges rules from bundled or user preset packs into\n")
	b.WriteString("# this repo's compiled lockfile. Run `reconc preset list` to see\n")
	b.WriteString("# every available pack and `reconc preset show NAME` to inspect one.\n")
	b.WriteString("extends:\n")
	for _, name := range extends {
		b.WriteString("  - ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	b.WriteString("# Add repo-local rules below. They compose with the preset rules\n")
	b.WriteString("# listed above; duplicate rule IDs are a hard error.\n")
	b.WriteString("rules: []\n")
	return b.String()
}

func renderAgentsStub(repoName string) string {
	return "# " + repoName + "\n\n" +
		"This repository uses [`reconc`](https://reconc.dev) to compile its policy\n" +
		"into a versioned lockfile under `.reconc/policy.lock.json`.\n\n" +
		"Policy rules live in `.reconc.yml` and are inherited from the bundled\n" +
		"preset(s) listed under `extends:`. Add repo-local rules inline below in\n" +
		"a fenced `reconc` block if you need something the preset does not cover.\n\n" +
		"```reconc\nrules: []\n```\n"
}

// exists reports whether the path is present on disk (any file type).
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// pathState reports whether the path exists and, if so, whether it's
// a regular file (not a directory or symlink target).
func pathState(path string) (bool, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return false, false
	}
	return true, info.Mode().IsRegular()
}
