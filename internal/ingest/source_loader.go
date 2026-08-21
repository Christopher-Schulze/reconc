package ingest

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/customruntime"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/presets"
)

// GlobalPolicyFilename is the filename for the user-level global policy
// loaded into every repo's compile.
const GlobalPolicyFilename = "global-policy.yml"

// GlobalPolicySourcePath is the portable provenance identifier written into
// compiled policy metadata. The physical RECONC_HOME path is private state.
const GlobalPolicySourcePath = "global:" + GlobalPolicyFilename

const (
	maxPolicySourceBytes     = 8 << 20
	maxPolicyBundleBytes     = 64 << 20
	maxPolicySources         = 4096
	maxInlineBlocksPerSource = 512
	maxCustomRuntimes        = 32
	maxRuntimeDirEntries     = 4096
)

// SourceBundle is the ordered set of policy sources discovered for a
// repository, plus the discovery metadata that produced them.
//
// Order matters: it is the foundation of the SHA-256 source digest the
// compiler computes, so any reshuffling changes lockfile bytes.
type SourceBundle struct {
	RepoRoot  string                `json:"repo_root"`
	Discovery DiscoveryResult       `json:"discovery"`
	Sources   []policy.PolicySource `json:"sources"`

	policyIncludePatterns []string
}

// PolicyIncludePatterns returns a defensive copy of the validated, sorted,
// unique glob recipe used to construct this bundle's policy-file tier.
func (b *SourceBundle) PolicyIncludePatterns() []string {
	if b == nil {
		return nil
	}
	return append([]string(nil), b.policyIncludePatterns...)
}

// LoadPolicySources is the second stage of the compile pipeline. Given
// a path inside (or at) a repo, it discovers the repo root, then loads
// every canonical policy source into a deterministically-ordered
// SourceBundle.
//
// The order matches the precedence chain in policy.SourcePrecedence():
//
//	global -> claude_md -> agents_md -> start_md -> inline_block ->
//	compiler_config -> preset -> policy_file -> custom_runtime
//
// Inline blocks retain their parent-file order inside the one inline_block
// precedence tier. Custom runtime manifests are identity inputs after policy
// sources; they do not author rules.
//
// Returns *PolicySourceError for malformed YAML or unsafe include
// patterns; *PresetNotFoundError when an extends entry doesn't resolve;
// underlying error wrapped for IO failures.
func LoadPolicySources(repoStartPath string) (*SourceBundle, error) {
	context, err := NewSourceLoadContext(repoStartPath)
	if err != nil {
		return nil, err
	}
	return LoadPolicySourcesWithContext(context)
}

// LoadPolicySourcesWithContext loads one previously discovered, identity-bound
// source snapshot. The context is validated before and after all reads.
func LoadPolicySourcesWithContext(context *SourceLoadContext) (*SourceBundle, error) {
	if context == nil {
		return nil, &rerrors.PolicySourceError{Message: "policy source load context is nil"}
	}
	discovery := context.Discovery
	if !discovery.Discovered {
		warning := "no policy markers discovered"
		if len(discovery.Warnings) > 0 {
			warning = discovery.Warnings[0]
		}
		return nil, &rerrors.PolicySourceError{Message: warning}
	}

	root := discovery.RepoRoot
	if err := context.Validate(); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "validate policy source snapshot", Cause: err}
	}
	sources := []policy.PolicySource{}

	// 1. Global policy (lowest precedence, applies to every repo).
	if gs, err := loadGlobalPolicySource(); err != nil {
		return nil, err
	} else if gs != nil {
		sources = append(sources, *gs)
	}

	// 2-4. Context files in the exact low-to-high order declared by
	// policy.SourcePrecedence. Their rule-bearing inline blocks are collected
	// into the later inline_block tier rather than interleaved with prose.
	inlineSources := []policy.PolicySource{}
	if discovery.ClaudePath != nil {
		ss, err := loadEntryFileWithBlocks(root, *discovery.ClaudePath, policy.SourceClaudeMD)
		if err != nil {
			return nil, err
		}
		sources = append(sources, ss[0])
		inlineSources = append(inlineSources, ss[1:]...)
	}
	if discovery.AgentsPath != nil {
		ss, err := loadEntryFileWithBlocks(root, *discovery.AgentsPath, policy.SourceAgentsMD)
		if err != nil {
			return nil, err
		}
		sources = append(sources, ss[0])
		inlineSources = append(inlineSources, ss[1:]...)
	}
	if discovery.StartMDPath != nil {
		ss, err := loadEntryFileWithBlocks(root, *discovery.StartMDPath, policy.SourceStartMD)
		if err != nil {
			return nil, err
		}
		sources = append(sources, ss[0])
		inlineSources = append(inlineSources, ss[1:]...)
	}
	sources = append(sources, inlineSources...)

	// 5. .reconc.yml compiler config + extends + include.
	includePatterns := append([]string(nil), DefaultPolicyGlobs...)
	presetNames := []string{}

	if discovery.ConfigPath != nil {
		configText, err := readRepositorySource(root, *discovery.ConfigPath)
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
		configDocument, err := decodeYAMLMapping(string(configText), *discovery.ConfigPath)
		if err != nil {
			return nil, err
		}
		extra, err := loadIncludePatternsDocument(configDocument, *discovery.ConfigPath)
		if err != nil {
			return nil, err
		}
		includePatterns = append(includePatterns, extra...)

		names, err := loadPresetNamesDocument(configDocument, *discovery.ConfigPath)
		if err != nil {
			return nil, err
		}
		presetNames = names
	}

	// 6. Preset packs referenced via extends:.
	if err := presets.ValidateSelection(presetNames); err != nil {
		return nil, err
	}
	presetSources, err := loadPresetSources(presetNames)
	if err != nil {
		return nil, err
	}
	sources = append(sources, presetSources...)

	// 7. Policy file fragments (sorted, deduplicated).
	includePatterns = sortedUniquePolicyGlobPatterns(includePatterns)
	fragmentSources, fragmentWarnings, err := loadPolicyFragmentSourcesWithDefaults(root, includePatterns, context.defaultMatches)
	if err != nil {
		return nil, err
	}
	sources = append(sources, fragmentSources...)
	discovery.Warnings = append(discovery.Warnings, fragmentWarnings...)

	// 8. Declarative custom runtime manifests. They are not policy YAML, but
	// their exact bytes participate in the same source identity.
	runtimeSources, err := LoadCustomRuntimeSources(root)
	if err != nil {
		return nil, err
	}
	sources = append(sources, runtimeSources...)

	if err := validatePolicySourceBounds(sources); err != nil {
		return nil, err
	}
	if err := context.Validate(); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "policy source snapshot changed while loading", Cause: err}
	}
	return &SourceBundle{
		RepoRoot:              root,
		Discovery:             discovery,
		Sources:               sources,
		policyIncludePatterns: append([]string(nil), includePatterns...),
	}, nil
}

// LoadCustomRuntimeSources reads repository-owned declarative manifests in a
// deterministic order. Symlinks and non-regular JSON entries are rejected so
// the compiled identity always refers to bytes physically owned by the repo.
func LoadCustomRuntimeSources(root string) ([]policy.PolicySource, error) {
	directory := filepath.Join(root, ".reconc", "runtimes")
	entries, err := boundedio.ReadDirNoSymlink(directory, maxRuntimeDirEntries)
	if os.IsNotExist(err) {
		return []policy.PolicySource{}, nil
	}
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "read custom runtime directory", Cause: err}
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if len(paths) >= maxCustomRuntimes {
			return nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("custom runtime directory exceeds %d manifests", maxCustomRuntimes)}
		}
		paths = append(paths, filepath.ToSlash(filepath.Join(".reconc", "runtimes", entry.Name())))
	}
	sort.Strings(paths)
	sources := make([]policy.PolicySource, 0, len(paths))
	for _, rel := range paths {
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil {
			return nil, &rerrors.PolicySourceError{Message: "stat custom runtime " + rel, Cause: err}
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, &rerrors.PolicySourceError{Message: "custom runtime " + rel + " must be a non-symlink regular file"}
		}
		body, err := readRepositorySource(root, rel)
		if err != nil {
			return nil, &rerrors.PolicySourceError{Message: "read custom runtime " + rel, Cause: err}
		}
		if len(body) > customruntime.MaxManifestBytes {
			return nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("custom runtime %s exceeds %d bytes", rel, customruntime.MaxManifestBytes)}
		}
		sources = append(sources, policy.PolicySource{Kind: policy.SourceCustomRuntime, Path: rel, Content: string(body)})
	}
	return sources, nil
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
	data, err := boundedio.ReadFile(path, maxPolicySourceBytes)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "read global policy", Cause: err}
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	return &policy.PolicySource{
		Kind:    policy.SourceGlobal,
		Path:    GlobalPolicySourcePath,
		Content: string(data),
	}, nil
}

// loadEntryFileWithBlocks reads the named context file (relative to
// root) and returns the file-as-source plus every inline ```reconc
// fenced block found inside.
func loadEntryFileWithBlocks(root, relPath string, kind policy.SourceKind) ([]policy.PolicySource, error) {
	data, err := readRepositorySource(root, relPath)
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
	blocks, err := ScanInlinePolicyBlocks(relPath, text)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "extract inline policy blocks from " + relPath, Cause: err}
	}
	out = append(out, blocks...)
	return out, nil
}

// ScanInlinePolicyBlocks is the authoritative scanner for fenced reconc
// policy blocks. Both compilation and diagnostics consume these exact source
// records so fence recognition, normalized content, order, and provenance
// cannot diverge.
func ScanInlinePolicyBlocks(relPath, text string) ([]policy.PolicySource, error) {
	out := make([]policy.PolicySource, 0, 4)
	lineStartOffset := 0
	lineNumber := 1
	for lineStartOffset < len(text) {
		line, nextOffset := inlineLine(text, lineStartOffset)
		if !isInlineOpeningLine(line) {
			lineStartOffset = nextOffset
			lineNumber++
			continue
		}
		contentStart := nextOffset
		contentOffset := contentStart
		contentLine := lineNumber + 1
		for contentOffset < len(text) {
			candidate, candidateNext := inlineLine(text, contentOffset)
			if !isInlineClosingLine(candidate) {
				contentOffset = candidateNext
				contentLine++
				continue
			}
			if len(out) >= maxInlineBlocksPerSource {
				return nil, fmt.Errorf("inline policy source %s exceeds %d blocks", relPath, maxInlineBlocksPerSource)
			}
			content := strings.TrimSpace(text[contentStart:contentOffset]) + "\n"
			out = append(out, policy.PolicySource{
				Kind:      policy.SourceInlineBlock,
				Path:      relPath,
				Content:   content,
				BlockID:   fmt.Sprintf("%s:%d", relPath, lineNumber),
				LineStart: lineNumber,
			})
			lineStartOffset = candidateNext
			lineNumber = contentLine + 1
			break
		}
		if contentOffset >= len(text) {
			lineStartOffset = nextOffset
			lineNumber++
		}
	}
	return out, nil
}

func inlineLine(text string, start int) (string, int) {
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		line := strings.TrimSuffix(text[start:], "\r")
		return line, len(text)
	}
	end += start
	line := strings.TrimSuffix(text[start:end], "\r")
	return line, end + 1
}

func isInlineOpeningLine(line string) bool {
	if !strings.HasPrefix(line, "```reconc") {
		return false
	}
	return strings.Trim(line[len("```reconc"):], " \t") == ""
}

func isInlineClosingLine(line string) bool {
	return strings.HasPrefix(line, "```") && strings.Trim(line[3:], " \t") == ""
}

// loadIncludePatterns parses the `include:` field of a compiler config
// document into a sanitized list of repo-relative glob patterns.
func loadIncludePatterns(configText, context string) ([]string, error) {
	doc, err := decodeYAMLMapping(configText, context)
	if err != nil {
		return nil, err
	}
	return loadIncludePatternsDocument(doc, context)
}

func loadIncludePatternsDocument(doc map[string]interface{}, context string) ([]string, error) {
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
		if path.IsAbs(normalized) || filepath.IsAbs(normalized) || strings.Contains(normalized, "..") {
			return nil, &rerrors.PolicySourceError{
				Message: "include patterns must stay within the repo root",
			}
		}
		out = append(out, normalized)
	}
	if err := validatePolicyGlobPatterns(out); err != nil {
		return nil, &rerrors.PolicySourceError{Message: err.Error()}
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
	return loadPresetNamesDocument(doc, context)
}

func loadPresetNamesDocument(doc map[string]interface{}, context string) ([]string, error) {
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

func loadPolicyFragmentSourcesWithDefaults(root string, patterns []string, defaultMatches map[string][]string) ([]policy.PolicySource, []string, error) {
	if err := validatePolicyGlobPatterns(patterns); err != nil {
		return nil, nil, &rerrors.PolicySourceError{Message: err.Error()}
	}
	// Dedupe + sort patterns first so glob expansion is deterministic.
	uniquePatterns := sortedUniquePolicyGlobPatterns(patterns)

	seen := map[string]struct{}{}
	out := []policy.PolicySource{}
	var totalBytes int64
	for _, pattern := range uniquePatterns {
		matches := []string{}
		if cached, ok := defaultMatches[pattern]; ok {
			for _, rel := range cached {
				matches = append(matches, filepath.Join(root, filepath.FromSlash(rel)))
			}
		} else {
			var err error
			matches, err = boundedPolicyGlob(root, pattern)
			if err != nil {
				return nil, nil, &rerrors.PolicySourceError{
					Message: "expand include pattern " + pattern,
					Cause:   err,
				}
			}
			sort.Strings(matches)
		}
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
			data, err := readRepositorySource(root, rel)
			if err != nil {
				return nil, nil, &rerrors.PolicySourceError{
					Message: "read policy fragment " + rel,
					Cause:   err,
				}
			}
			totalBytes += int64(len(data))
			if len(out) >= maxPolicySources || totalBytes > maxPolicyBundleBytes {
				return nil, nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("policy fragments exceed %d sources or %d total bytes", maxPolicySources, maxPolicyBundleBytes)}
			}
			out = append(out, policy.PolicySource{
				Kind:    policy.SourcePolicyFile,
				Path:    rel,
				Content: string(data),
			})
		}
	}
	return out, []string{}, nil
}

func sortedUniquePolicyGlobPatterns(patterns []string) []string {
	patternSet := map[string]struct{}{}
	for _, p := range patterns {
		patternSet[p] = struct{}{}
	}
	uniquePatterns := make([]string, 0, len(patternSet))
	for p := range patternSet {
		uniquePatterns = append(uniquePatterns, p)
	}
	sort.Strings(uniquePatterns)
	return uniquePatterns
}

// pathOutsideRoot reports whether resolved lies outside resolvedRoot.
func pathOutsideRoot(resolvedRoot, resolved string) (bool, error) {
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return false, err
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// readRepositorySource reads one regular policy source only when both its
// lexical path and resolved filesystem identity remain inside root. Resolving
// before and after the read detects path swaps instead of digesting bytes from
// a different target than the validated source.
func readRepositorySource(root, rel string) ([]byte, error) {
	if filepath.IsAbs(rel) {
		return nil, fmt.Errorf("%s must be repository-relative", rel)
	}
	rootIdentity, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	before, err := pathidentity.ResolveExisting(full)
	if err != nil {
		return nil, fmt.Errorf("resolve source %s: %w", rel, err)
	}
	outside, err := pathOutsideRoot(rootIdentity, before)
	if err != nil {
		return nil, fmt.Errorf("validate source %s containment: %w", rel, err)
	}
	if outside {
		return nil, fmt.Errorf("source %s resolves outside the repository root", rel)
	}
	info, err := os.Stat(before)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source %s must resolve to a regular file", rel)
	}
	data, openedIdentity, err := boundedio.ReadFileSnapshot(before, maxPolicySourceBytes)
	if err != nil {
		return nil, err
	}
	after, err := pathidentity.ResolveExisting(full)
	if err != nil {
		return nil, fmt.Errorf("revalidate source %s: %w", rel, err)
	}
	if before != after {
		return nil, fmt.Errorf("source %s changed filesystem identity while being read", rel)
	}
	afterRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("revalidate repository root for %s: %w", rel, err)
	}
	if rootIdentity != afterRoot {
		return nil, fmt.Errorf("repository root changed filesystem identity while reading %s", rel)
	}
	afterInfo, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("revalidate source identity %s: %w", rel, err)
	}
	if !sameSourceInfo(openedIdentity, afterInfo) {
		return nil, fmt.Errorf("source %s changed opened filesystem identity while being read", rel)
	}
	return data, nil
}

func validatePolicySourceBounds(sources []policy.PolicySource) error {
	if len(sources) > maxPolicySources {
		return &rerrors.PolicySourceError{Message: fmt.Sprintf("policy bundle contains %d sources; maximum is %d", len(sources), maxPolicySources)}
	}
	var total int64
	for _, source := range sources {
		total += int64(len(source.Content))
		if len(source.Content) > maxPolicySourceBytes || total > maxPolicyBundleBytes {
			return &rerrors.PolicySourceError{Message: fmt.Sprintf("policy bundle exceeds %d bytes per source or %d total bytes", maxPolicySourceBytes, maxPolicyBundleBytes)}
		}
	}
	return nil
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
