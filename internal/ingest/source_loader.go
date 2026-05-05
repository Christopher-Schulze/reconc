package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/presets"
)

// GlobalPolicyFilename is the filename for the user-level global policy
// loaded into every repo's compile.
const GlobalPolicyFilename = "global-policy.yml"

// inlineBlockRegex matches fenced ```reconc ... ``` blocks inside
// markdown context files. Block content is captured group 1.
//
// Pattern allows trailing whitespace after the language tag and either
// LF or CRLF line endings. Fences must start at the beginning of a
// line; this is a simple-but-robust approximation of CommonMark fenced
// code block parsing sufficient for our extraction.
var inlineBlockRegex = regexp.MustCompile("(?ms)^```reconc[ \\t]*\\r?\\n(.*?)\\r?\\n```")

// SourceBundle is the ordered set of policy sources discovered for a
// repository, plus the discovery metadata that produced them.
//
// Order matters: it is the foundation of the SHA-256 source digest the
// compiler computes, so any reshuffling changes lockfile bytes.
type SourceBundle struct {
	RepoRoot  string                `json:"repo_root"`
	Discovery DiscoveryResult       `json:"discovery"`
	Sources   []policy.PolicySource `json:"sources"`
}

// LoadPolicySources is the second stage of the compile pipeline. Given
// a path inside (or at) a repo, it discovers the repo root, then loads
// every canonical policy source into a deterministically-ordered
// SourceBundle.
//
// The order matches the precedence chain in policy.SourcePrecedence():
//
//	global -> agents_md -> inline_block -> claude_md -> inline_block ->
//	start_md -> compiler_config -> preset -> policy_file
//
// (When both AGENTS.md and CLAUDE.md exist, AGENTS.md takes precedence;
// each contributes its own kind tag and any inline blocks found.)
//
// Returns *PolicySourceError for malformed YAML or unsafe include
// patterns; *PresetNotFoundError when an extends entry doesn't resolve;
// underlying error wrapped for IO failures.
func LoadPolicySources(repoStartPath string) (*SourceBundle, error) {
	discovery, err := DiscoverPolicyRepo(repoStartPath)
	if err != nil {
		return nil, err
	}
	if !discovery.Discovered {
		warning := "no policy markers discovered"
		if len(discovery.Warnings) > 0 {
			warning = discovery.Warnings[0]
		}
		return nil, &rerrors.PolicySourceError{Message: warning}
	}

	root := discovery.RepoRoot
	sources := []policy.PolicySource{}

	// 1. Global policy (lowest precedence, applies to every repo).
	if gs, err := loadGlobalPolicySource(); err != nil {
		return nil, err
	} else if gs != nil {
		sources = append(sources, *gs)
	}

	// 2. AGENTS.md context file + inline blocks.
	if discovery.AgentsPath != nil {
		ss, err := loadEntryFileWithBlocks(root, *discovery.AgentsPath, policy.SourceAgentsMD)
		if err != nil {
			return nil, err
		}
		sources = append(sources, ss...)
	}

	// 3. CLAUDE.md context file + inline blocks (legacy still supported).
	if discovery.ClaudePath != nil {
		ss, err := loadEntryFileWithBlocks(root, *discovery.ClaudePath, policy.SourceClaudeMD)
		if err != nil {
			return nil, err
		}
		sources = append(sources, ss...)
	}

	// 4. start.md context file + inline blocks.
	if discovery.StartMDPath != nil {
		ss, err := loadEntryFileWithBlocks(root, *discovery.StartMDPath, policy.SourceStartMD)
		if err != nil {
			return nil, err
		}
		sources = append(sources, ss...)
	}

	// 5. .reconc.yml compiler config + extends + include.
	includePatterns := append([]string(nil), DefaultPolicyGlobs...)
	presetNames := []string{}

	if discovery.ConfigPath != nil {
		configPath := filepath.Join(root, *discovery.ConfigPath)
		configText, err := os.ReadFile(configPath)
		if err != nil {
			return nil, &rerrors.PolicySourceError{
				Message: "read compiler config " + *discovery.ConfigPath,
				Cause:   err,
			}
		}
		sources = append(sources, policy.PolicySource{
			Kind:    policy.SourceCompilerConfig,
			Path:    *discovery.ConfigPath,
			Content: string(configText),
		})
		extra, err := loadIncludePatterns(string(configText), *discovery.ConfigPath)
		if err != nil {
			return nil, err
		}
		includePatterns = append(includePatterns, extra...)

		names, err := loadPresetNames(string(configText), *discovery.ConfigPath)
		if err != nil {
			return nil, err
		}
		presetNames = names
	}

	// 6. Preset packs referenced via extends:.
	presetSources, err := loadPresetSources(presetNames)
	if err != nil {
		return nil, err
	}
	sources = append(sources, presetSources...)

	// 7. Policy file fragments (sorted, deduplicated).
	fragmentSources, err := loadPolicyFragmentSources(root, includePatterns)
	if err != nil {
		return nil, err
	}
	sources = append(sources, fragmentSources...)

	return &SourceBundle{
		RepoRoot:  root,
		Discovery: discovery,
		Sources:   sources,
	}, nil
}

// loadGlobalPolicySource reads ~/.reconc/global-policy.yml (or whatever
// $RECONC_HOME points to). Returns nil source when the file doesn't
// exist or is empty - both are valid "no global policy" states.
func loadGlobalPolicySource() (*policy.PolicySource, error) {
	path := filepath.Join(presets.Home(), GlobalPolicyFilename)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &rerrors.PolicySourceError{Message: "stat global policy", Cause: err}
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "read global policy", Cause: err}
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	return &policy.PolicySource{
		Kind:    policy.SourceGlobal,
		Path:    path,
		Content: string(data),
	}, nil
}

// loadEntryFileWithBlocks reads the named context file (relative to
// root) and returns the file-as-source plus every inline ```reconc
// fenced block found inside.
func loadEntryFileWithBlocks(root, relPath string, kind policy.SourceKind) ([]policy.PolicySource, error) {
	full := filepath.Join(root, relPath)
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, &rerrors.PolicySourceError{
			Message: "read context file " + relPath,
			Cause:   err,
		}
	}
	text := string(data)
	out := []policy.PolicySource{
		{Kind: kind, Path: relPath, Content: text},
	}
	out = append(out, extractInlineBlocks(relPath, text)...)
	return out, nil
}

// extractInlineBlocks scans markdown text for fenced ```reconc blocks
// and returns each as a PolicySource with provenance pointing back to
// the source line.
func extractInlineBlocks(relPath, text string) []policy.PolicySource {
	matches := inlineBlockRegex.FindAllStringSubmatchIndex(text, -1)
	out := make([]policy.PolicySource, 0, len(matches))
	for _, m := range matches {
		// m[0]/m[1] = whole match; m[2]/m[3] = group 1 (content)
		blockStart := m[0]
		contentStart, contentEnd := m[2], m[3]
		lineStart := strings.Count(text[:blockStart], "\n") + 1
		content := strings.TrimSpace(text[contentStart:contentEnd]) + "\n"
		out = append(out, policy.PolicySource{
			Kind:      policy.SourceInlineBlock,
			Path:      relPath,
			Content:   content,
			BlockID:   fmt.Sprintf("%s:%d", relPath, lineStart),
			LineStart: lineStart,
		})
	}
	return out
}

// loadIncludePatterns parses the `include:` field of a compiler config
// document into a sanitized list of repo-relative glob patterns.
func loadIncludePatterns(configText, context string) ([]string, error) {
	doc, err := decodeYAMLMapping(configText, context)
	if err != nil {
		return nil, err
	}
	raw, ok := doc["include"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, &rerrors.PolicySourceError{Message: "include must be a list of glob strings"}
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		str, ok := item.(string)
		if !ok || strings.TrimSpace(str) == "" {
			return nil, &rerrors.PolicySourceError{
				Message: fmt.Sprintf("include[%d] must be a non-empty glob string", i),
			}
		}
		normalized := strings.TrimSpace(str)
		if filepath.IsAbs(normalized) || strings.Contains(normalized, "..") {
			return nil, &rerrors.PolicySourceError{
				Message: "include patterns must stay within the repo root",
			}
		}
		out = append(out, normalized)
	}
	return out, nil
}

// loadPresetNames parses the `extends:` field of a compiler config
// document into a deduplicated list of preset names.
func loadPresetNames(configText, context string) ([]string, error) {
	doc, err := decodeYAMLMapping(configText, context)
	if err != nil {
		return nil, err
	}
	raw, ok := doc["extends"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, &rerrors.PolicySourceError{Message: "extends must be a list of preset name strings"}
	}
	out := make([]string, 0, len(list))
	seen := map[string]struct{}{}
	for i, item := range list {
		str, ok := item.(string)
		if !ok || strings.TrimSpace(str) == "" {
			return nil, &rerrors.PolicySourceError{
				Message: fmt.Sprintf("extends[%d] must be a non-empty preset name string", i),
			}
		}
		cleaned := strings.TrimSpace(str)
		if strings.HasPrefix(cleaned, "preset:") {
			cleaned = strings.TrimSpace(strings.TrimPrefix(cleaned, "preset:"))
			if cleaned == "" {
				return nil, &rerrors.PolicySourceError{
					Message: fmt.Sprintf("extends[%d] is missing a preset name after 'preset:' prefix", i),
				}
			}
		}
		if _, dup := seen[cleaned]; dup {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out, nil
}

// loadPresetSources resolves each preset name through the presets
// package and wraps the YAML content in a PolicySource.
func loadPresetSources(names []string) ([]policy.PolicySource, error) {
	out := make([]policy.PolicySource, 0, len(names))
	for _, name := range names {
		content, err := presets.Load(name)
		if err != nil {
			return nil, err
		}
		out = append(out, policy.PolicySource{
			Kind:    policy.SourcePreset,
			Path:    "preset:" + name,
			Content: content,
			BlockID: name,
		})
	}
	return out, nil
}

// loadPolicyFragmentSources walks the merged include patterns and
// loads each unique repo-relative file as a policy_file source.
// Fragments are returned in sorted order for determinism.
func loadPolicyFragmentSources(root string, patterns []string) ([]policy.PolicySource, error) {
	// Dedupe + sort patterns first so glob expansion is deterministic.
	patternSet := map[string]struct{}{}
	for _, p := range patterns {
		patternSet[p] = struct{}{}
	}
	uniquePatterns := make([]string, 0, len(patternSet))
	for p := range patternSet {
		uniquePatterns = append(uniquePatterns, p)
	}
	sort.Strings(uniquePatterns)

	seen := map[string]struct{}{}
	out := []policy.PolicySource{}
	for _, pattern := range uniquePatterns {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, &rerrors.PolicySourceError{
				Message: "expand include pattern " + pattern,
				Cause:   err,
			}
		}
		sort.Strings(matches)
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			rel, err := filepath.Rel(root, match)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if _, dup := seen[rel]; dup {
				continue
			}
			seen[rel] = struct{}{}
			data, err := os.ReadFile(match)
			if err != nil {
				return nil, &rerrors.PolicySourceError{
					Message: "read policy fragment " + rel,
					Cause:   err,
				}
			}
			out = append(out, policy.PolicySource{
				Kind:    policy.SourcePolicyFile,
				Path:    rel,
				Content: string(data),
			})
		}
	}
	return out, nil
}

// decodeYAMLMapping parses raw YAML into a map[string]interface{}.
// Empty input is normalized to an empty map. Non-mapping documents
// raise a PolicySourceError.
func decodeYAMLMapping(raw, context string) (map[string]interface{}, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]interface{}{}, nil
	}
	var doc interface{}
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, &rerrors.PolicySourceError{
			Message: "invalid yaml in " + context,
			Cause:   err,
		}
	}
	if doc == nil {
		return map[string]interface{}{}, nil
	}
	mapping, ok := doc.(map[string]interface{})
	if !ok {
		return nil, &rerrors.PolicySourceError{
			Message: "expected a YAML mapping in " + context,
		}
	}
	return mapping, nil
}
