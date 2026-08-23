// Package adopt scans a repository for existing tooling and emits
// reconc rule suggestions. The agent (or user) can then paste the
// suggested YAML into .reconc.yml, or use `reconc adopt --apply` to
// append them automatically.
//
// Detection is a best-effort: we look for common marker files
// (package.json, pyproject.toml, Cargo.toml, go.mod, .github/workflows/)
// and emit a small set of high-confidence rule suggestions. We never
// emit destructive-looking rules (e.g. forbid_command); the goal is to
// get a new repo to useful behavioral coverage without the user writing YAML.
package adopt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/bootstrap"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/stackdetect"
)

const (
	maxAdoptManifestBytes   = 1 << 20
	maxAdoptConfigBytes     = 8 << 20
	maxAdoptWorkflowEntries = 4096
)

// Suggestion is one rule the detector wants to propose. Kept small +
// explicit so the CLI can render it as YAML, as a plain human list, or
// as a JSON payload for an agent.
type Suggestion struct {
	// ID is the rule id suggested for the new rule (unique within a
	// typical preset namespace; user can override).
	ID string `json:"id"`
	// Kind mirrors policy.Kind strings (deny_write, require_command,
	// require_claim, etc.). Kept as string to avoid a circular import.
	Kind string `json:"kind"`
	// Mode is warn|block; adopt is conservative and defaults to warn so
	// the initial adoption doesn't immediately break workflows.
	Mode string `json:"mode"`
	// Message is the human-facing violation message.
	Message string `json:"message"`
	// Paths is used for deny_write / require_read rules.
	Paths []string `json:"paths,omitempty"`
	// WhenPaths scopes when the rule applies (required by
	// require_command / require_claim / couple_change / require_fresh_file).
	WhenPaths []string `json:"when_paths,omitempty"`
	// Commands is used for require_command rules.
	Commands []string `json:"commands,omitempty"`
	// Claims is used for require_claim rules.
	Claims []string `json:"claims,omitempty"`
	// Evidence is the marker file(s) that triggered this suggestion,
	// for explainability in text output.
	Evidence []string `json:"evidence"`
	// Reason is a short human explanation of WHY this rule is suggested.
	Reason string `json:"reason"`
}

// Report groups all suggestions for a single adopt run.
type Report struct {
	RepoRoot        string           `json:"repo_root"`
	Detected        []string         `json:"detected"`
	Ambiguities     []string         `json:"ambiguities"`
	Suggestions     []Suggestion     `json:"suggestions"`
	PackSuggestions []PackSuggestion `json:"pack_suggestions"`
}

// PackSuggestion is a stack-evidenced preset recommendation. It is never
// consumed by Apply; adding a pack to extends remains an explicit decision.
type PackSuggestion struct {
	Name          string   `json:"name"`
	DetectedStack string   `json:"detected_stack"`
	Evidence      []string `json:"evidence"`
	Reason        string   `json:"reason"`
	Capabilities  []string `json:"capabilities"`
}

// Scan inspects repoRoot for common tooling and returns a deterministic
// Report. Never mutates the repository.
func Scan(repoRoot string) (Report, error) {
	r := Report{
		RepoRoot:        repoRoot,
		Detected:        []string{},
		Ambiguities:     []string{},
		Suggestions:     []Suggestion{},
		PackSuggestions: []PackSuggestion{},
	}
	detection, err := stackdetect.Detect(repoRoot)
	if err != nil {
		return Report{}, err
	}
	r.Ambiguities = append(r.Ambiguities, detection.Ambiguities...)

	// --- JS / TS ---
	if exists(filepath.Join(repoRoot, "package.json")) {
		r.Detected = append(r.Detected, "package.json")
		pkgData, readErr := boundedio.ReadRegularFile(filepath.Join(repoRoot, "package.json"), maxAdoptManifestBytes)
		var manifest packageJSONDocument
		if readErr != nil {
			r.Ambiguities = append(r.Ambiguities, "package.json could not be inspected: "+readErr.Error())
		} else if err := json.Unmarshal(pkgData, &manifest); err != nil {
			r.Ambiguities = append(r.Ambiguities, "package.json is malformed: "+err.Error())
		} else {
			if runner, unambiguous := detectRootJSRunner(detection.PackageManagers); unambiguous {
				for _, script := range []string{"test", "lint", "build", "typecheck"} {
					if strings.TrimSpace(manifest.Scripts[script]) == "" {
						continue
					}
					r.Suggestions = append(r.Suggestions, packageScriptSuggestion(runner, script))
				}
			}
		}
	}
	if config := rootTypeScriptConfig(repoRoot); config != "" {
		r.Detected = append(r.Detected, config)
	}

	// --- Python ---
	if exists(filepath.Join(repoRoot, "pyproject.toml")) {
		r.Detected = append(r.Detected, "pyproject.toml")
		py, readErr := boundedio.ReadRegularFile(filepath.Join(repoRoot, "pyproject.toml"), maxAdoptManifestBytes)
		if readErr != nil {
			r.Ambiguities = append(r.Ambiguities, "pyproject.toml could not be inspected: "+readErr.Error())
			py = nil
		}
		if contains(py, "ruff") {
			r.Suggestions = append(r.Suggestions, Suggestion{
				ID:        "adopt-py-ruff",
				Kind:      "require_command",
				Mode:      "warn",
				Message:   "Run ruff before declaring done.",
				WhenPaths: []string{"**/*.py"},
				Commands:  []string{"ruff check ."},
				Evidence:  []string{"pyproject.toml"},
				Reason:    "pyproject.toml mentions ruff",
			})
		}
		if contains(py, "pytest") {
			r.Suggestions = append(r.Suggestions, Suggestion{
				ID:        "adopt-py-pytest",
				Kind:      "require_command",
				Mode:      "warn",
				Message:   "Run pytest before declaring done.",
				WhenPaths: []string{"**/*.py"},
				Commands:  []string{"pytest -q"},
				Evidence:  []string{"pyproject.toml"},
				Reason:    "pyproject.toml mentions pytest",
			})
		}
		if contains(py, "mypy") {
			r.Suggestions = append(r.Suggestions, Suggestion{
				ID:        "adopt-py-mypy",
				Kind:      "require_command",
				Mode:      "warn",
				Message:   "Run mypy before declaring done.",
				WhenPaths: []string{"**/*.py"},
				Commands:  []string{"mypy ."},
				Evidence:  []string{"pyproject.toml"},
				Reason:    "pyproject.toml mentions mypy",
			})
		}
	}
	// --- Rust ---
	if exists(filepath.Join(repoRoot, "Cargo.toml")) {
		r.Detected = append(r.Detected, "Cargo.toml")
		r.Suggestions = append(r.Suggestions, Suggestion{
			ID:        "adopt-rust-test",
			Kind:      "require_command",
			Mode:      "warn",
			Message:   "Run cargo test before declaring done.",
			WhenPaths: []string{"**/*.rs"},
			Commands:  []string{"cargo test"},
			Evidence:  []string{"Cargo.toml"},
			Reason:    "Cargo.toml is present",
		})
		r.Suggestions = append(r.Suggestions, Suggestion{
			ID:        "adopt-rust-clippy",
			Kind:      "require_command",
			Mode:      "warn",
			Message:   "Run cargo clippy with -D warnings before declaring done.",
			WhenPaths: []string{"**/*.rs"},
			Commands:  []string{"cargo clippy -- -D warnings"},
			Evidence:  []string{"Cargo.toml"},
			Reason:    "Cargo.toml is present; clippy-clean is standard",
		})
	}

	// --- Go ---
	if exists(filepath.Join(repoRoot, "go.mod")) {
		r.Detected = append(r.Detected, "go.mod")
		r.Suggestions = append(r.Suggestions, Suggestion{
			ID:        "adopt-go-test",
			Kind:      "require_command",
			Mode:      "warn",
			Message:   "Run go test ./... before declaring done.",
			WhenPaths: []string{"**/*.go"},
			Commands:  []string{"go test ./..."},
			Evidence:  []string{"go.mod"},
			Reason:    "go.mod is present",
		})
		r.Suggestions = append(r.Suggestions, Suggestion{
			ID:        "adopt-go-vet",
			Kind:      "require_command",
			Mode:      "warn",
			Message:   "Run go vet ./... before declaring done.",
			WhenPaths: []string{"**/*.go"},
			Commands:  []string{"go vet ./..."},
			Evidence:  []string{"go.mod"},
			Reason:    "go.mod is present",
		})
	}

	// --- GitHub Actions / CI ---
	ciPath := filepath.Join(repoRoot, ".github", "workflows")
	if isDir(ciPath) {
		entries, readErr := boundedio.ReadDirNoSymlink(ciPath, maxAdoptWorkflowEntries)
		if readErr != nil {
			r.Ambiguities = append(r.Ambiguities, ".github/workflows could not be inspected completely: "+readErr.Error())
		}
		hasCI := false
		for _, e := range entries {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
				hasCI = true
				break
			}
		}
		if hasCI {
			r.Detected = append(r.Detected, ".github/workflows/")
			r.Suggestions = append(r.Suggestions, Suggestion{
				ID:        "adopt-ci-green-gate",
				Kind:      "require_claim",
				Mode:      "warn",
				Message:   "Assert ci-green before merging; CI is the source of truth for passing tests.",
				WhenPaths: []string{"**"},
				Claims:    []string{"ci-green"},
				Evidence:  []string{".github/workflows/"},
				Reason:    ".github/workflows/ contains CI configuration",
			})
		}
	}

	// --- Generated / build artifacts ---
	// Only suggest deny_write if the dir actually exists AND is not
	// .gitignore-absent (a real generated tree on disk).
	for _, d := range []string{"dist", "build", "generated"} {
		full := filepath.Join(repoRoot, d)
		if isDir(full) {
			r.Detected = append(r.Detected, d+"/")
			r.Suggestions = append(r.Suggestions, Suggestion{
				ID:       "adopt-generated-" + d,
				Kind:     "deny_write",
				Mode:     "warn",
				Message:  "Do not hand-edit files under " + d + "/; they are build output.",
				Paths:    []string{d + "/**"},
				Evidence: []string{d + "/"},
				Reason:   d + "/ appears to be a build-output directory",
			})
		}
	}

	packs, err := presets.SuggestForStacks(detection.Stacks)
	if err != nil {
		return Report{}, err
	}
	for _, metadata := range packs {
		matchedStack := matchingManifestStack(metadata.Manifest.Stacks, detection.Stacks)
		capabilities := make([]string, 0, len(metadata.Manifest.Capabilities))
		for _, capability := range metadata.Manifest.Capabilities {
			capabilities = append(capabilities, capability.ID)
		}
		r.PackSuggestions = append(r.PackSuggestions, PackSuggestion{
			Name:          metadata.Name,
			DetectedStack: matchedStack,
			Evidence:      detection.Evidence[matchedStack],
			Reason:        metadata.Manifest.Summary,
			Capabilities:  capabilities,
		})
	}
	return r, nil
}

// RenderYAML emits a YAML snippet suitable for pasting into .reconc.yml
// under `rules:`. Deterministic output (suggestions are already in
// scan-order, which is stable).
func RenderYAML(r Report) string {
	if len(r.Suggestions) == 0 && len(r.PackSuggestions) == 0 && len(r.Ambiguities) == 0 {
		return "# reconc adopt: no suggestions for this repo.\n"
	}
	var b strings.Builder
	b.WriteString("# reconc adopt suggestions for ")
	b.WriteString(r.RepoRoot)
	b.WriteString("\n")
	for _, ambiguity := range r.Ambiguities {
		b.WriteString("# REVIEW REQUIRED: ")
		b.WriteString(ambiguity)
		b.WriteString("\n")
	}
	if len(r.Ambiguities) > 0 {
		b.WriteString("# Reconc did not infer package-manager commands for ambiguous package boundaries.\n\n")
	}
	if len(r.Suggestions) > 0 {
		b.WriteString("# Paste the rule body under the `rules:` key of .reconc.yml.\n")
		b.WriteString("# Start in warn mode; switch to block once green.\n\n")
	}
	if len(r.PackSuggestions) > 0 {
		b.WriteString("# Review-only pack suggestions; adopt --apply never changes extends:\n")
		b.WriteString("# extends: [")
		for index, suggestion := range r.PackSuggestions {
			if index > 0 {
				b.WriteString(", ")
			}
			b.WriteString(suggestion.Name)
		}
		b.WriteString("]\n\n")
	}
	b.WriteString(RenderRulesYAML(r.Suggestions))
	return b.String()
}

// RenderRulesYAML renders only the structured rule items from a detector
// report. Callers that need an embeddable policy document use this surface
// instead of extracting rule text from RenderYAML's human-facing prose.
func RenderRulesYAML(suggestions []Suggestion) string {
	var b strings.Builder
	for _, s := range suggestions {
		b.WriteString("  - id: ")
		b.WriteString(s.ID)
		b.WriteString("\n    kind: ")
		b.WriteString(s.Kind)
		b.WriteString("\n    mode: ")
		b.WriteString(s.Mode)
		b.WriteString("\n    message: ")
		b.WriteString(quoteYAML(s.Message))
		b.WriteString("\n")
		if len(s.Paths) > 0 {
			b.WriteString("    paths: [")
			b.WriteString(joinQuoted(s.Paths))
			b.WriteString("]\n")
		}
		if len(s.WhenPaths) > 0 {
			b.WriteString("    when_paths: [")
			b.WriteString(joinQuoted(s.WhenPaths))
			b.WriteString("]\n")
		}
		if len(s.Commands) > 0 {
			b.WriteString("    commands: [")
			b.WriteString(joinQuoted(s.Commands))
			b.WriteString("]\n")
		}
		if len(s.Claims) > 0 {
			b.WriteString("    claims: [")
			b.WriteString(joinQuoted(s.Claims))
			b.WriteString("]\n")
		}
		b.WriteString("    # evidence: ")
		b.WriteString(strings.Join(s.Evidence, ", "))
		b.WriteString("\n")
		b.WriteString("    # reason: ")
		b.WriteString(s.Reason)
		b.WriteString("\n\n")
	}
	return b.String()
}

// RenderText emits a compact human-readable summary. Used as the
// default `reconc adopt` output.
func RenderText(r Report) string {
	var b strings.Builder
	if len(r.Suggestions) == 0 && len(r.PackSuggestions) == 0 && len(r.Ambiguities) == 0 {
		b.WriteString("reconc adopt: no conventions detected in ")
		b.WriteString(r.RepoRoot)
		b.WriteString("\n")
		return b.String()
	}
	b.WriteString("Detected conventions in ")
	b.WriteString(r.RepoRoot)
	b.WriteString(":\n")
	for _, d := range r.Detected {
		b.WriteString("  - ")
		b.WriteString(d)
		b.WriteString("\n")
	}
	if len(r.Ambiguities) > 0 {
		b.WriteString("\nReview required:\n")
		for _, ambiguity := range r.Ambiguities {
			b.WriteString("  - ")
			b.WriteString(ambiguity)
			b.WriteString("; no package-manager command was inferred for that boundary.\n")
		}
	}
	if len(r.PackSuggestions) > 0 {
		b.WriteString("\nSuggested policy packs (review only; never auto-applied):\n")
		for _, suggestion := range r.PackSuggestions {
			b.WriteString("  - ")
			b.WriteString(suggestion.Name)
			b.WriteString(" (")
			b.WriteString(suggestion.DetectedStack)
			b.WriteString("): ")
			b.WriteString(suggestion.Reason)
			b.WriteString("\n")
		}
	}
	if len(r.Suggestions) > 0 {
		b.WriteString("\nSuggested rules (")
		b.WriteString(strconv.Itoa(len(r.Suggestions)))
		b.WriteString(" total, all warn-mode):\n\n")
	}
	for i, s := range r.Suggestions {
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". ")
		b.WriteString(s.ID)
		b.WriteString(" (")
		b.WriteString(s.Kind)
		b.WriteString(")\n     ")
		b.WriteString(s.Reason)
		b.WriteString("\n")
		if len(s.Commands) > 0 {
			b.WriteString("     -> ")
			b.WriteString(strings.Join(s.Commands, " | "))
			b.WriteString("\n")
		}
		if len(s.Paths) > 0 {
			b.WriteString("     -> paths: ")
			b.WriteString(strings.Join(s.Paths, ", "))
			b.WriteString("\n")
		}
		if len(s.Claims) > 0 {
			b.WriteString("     -> claim: ")
			b.WriteString(strings.Join(s.Claims, ", "))
			b.WriteString("\n")
		}
	}
	b.WriteString("\nNext steps:\n")
	if len(r.PackSuggestions) > 0 {
		b.WriteString("  - Review pack capabilities, then add selected names to extends manually.\n")
	}
	if len(r.Suggestions) > 0 {
		b.WriteString("  - Preview YAML:  reconc adopt ")
		b.WriteString(r.RepoRoot)
		b.WriteString(" --yaml\n")
		b.WriteString("  - Apply rules to .reconc.yml: reconc adopt ")
		b.WriteString(r.RepoRoot)
		b.WriteString(" --apply\n")
	}
	b.WriteString("  - JSON for agents: reconc adopt ")
	b.WriteString(r.RepoRoot)
	b.WriteString(" --json\n")
	return b.String()
}

// Apply inserts generated suggestion YAML into .reconc.yml's `rules:`
// block (or creates a minimal scaffold if the file is absent). Returns
// the list of rule ids actually written (skipping any that already
// exist with the same id to keep the operation idempotent). The write
// is atomic; an unwritable shape (inline non-empty rules list) fails
// loud instead of producing invalid YAML.
func Apply(repoRoot string, r Report) (added []string, err error) {
	err = bootstrap.WithRepositoryTransaction(repoRoot, func(root string) error {
		var applyErr error
		added, applyErr = applyLocked(root, r)
		return applyErr
	})
	return added, err
}

func applyLocked(repoRoot string, r Report) (added []string, err error) {
	configPath := filepath.Join(repoRoot, ".reconc.yml")
	existing, readErr := boundedio.ReadRegularFile(configPath, maxAdoptConfigBytes)
	missing := os.IsNotExist(readErr)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}
	existingIDs, err := validateAdoptConfig(string(existing))
	if err != nil {
		return nil, fmt.Errorf("validate existing .reconc.yml: %w", err)
	}

	var items strings.Builder
	for _, s := range r.Suggestions {
		if _, exists := existingIDs[s.ID]; exists {
			continue
		}
		items.WriteString("  - id: ")
		items.WriteString(s.ID)
		items.WriteString("\n    kind: ")
		items.WriteString(s.Kind)
		items.WriteString("\n    mode: ")
		items.WriteString(s.Mode)
		items.WriteString("\n    message: ")
		items.WriteString(quoteYAML(s.Message))
		items.WriteString("\n")
		if len(s.Paths) > 0 {
			items.WriteString("    paths: [")
			items.WriteString(joinQuoted(s.Paths))
			items.WriteString("]\n")
		}
		if len(s.WhenPaths) > 0 {
			items.WriteString("    when_paths: [")
			items.WriteString(joinQuoted(s.WhenPaths))
			items.WriteString("]\n")
		}
		if len(s.Commands) > 0 {
			items.WriteString("    commands: [")
			items.WriteString(joinQuoted(s.Commands))
			items.WriteString("]\n")
		}
		if len(s.Claims) > 0 {
			items.WriteString("    claims: [")
			items.WriteString(joinQuoted(s.Claims))
			items.WriteString("]\n")
		}
		added = append(added, s.ID)
	}

	if len(added) == 0 {
		return nil, nil
	}

	content, err := renderConfigWithRules(string(existing), items.String())
	if err != nil {
		return nil, err
	}
	if _, err := validateAdoptConfig(content); err != nil {
		return nil, fmt.Errorf("validate candidate .reconc.yml: %w", err)
	}
	current, currentErr := boundedio.ReadRegularFile(configPath, maxAdoptConfigBytes)
	if currentErr != nil && !os.IsNotExist(currentErr) {
		return nil, fmt.Errorf("revalidate .reconc.yml before adopt publication: %w", currentErr)
	}
	if missing != os.IsNotExist(currentErr) || !bytes.Equal(current, existing) {
		return nil, fmt.Errorf(".reconc.yml changed during adopt; rerun adopt against the current repository state")
	}
	if _, err := atomicfile.WriteIfChanged(configPath, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return added, nil
}

// topLevelKeyRegex matches a YAML top-level mapping key at column 0.
var topLevelKeyRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*:`)

// renderConfigWithRules inserts rendered rule items into the config's
// rules block. It converts an inline empty `rules: []` (the `reconc
// init` scaffold form) to block form, appends a `rules:` key when none
// exists, and inserts at the END of the rules block so top-level keys
// following it stay untouched.
func renderConfigWithRules(existing, items string) (string, error) {
	if strings.TrimSpace(existing) == "" {
		return "# .reconc.yml -- generated by `reconc adopt`.\n" +
			"# Start with warn-mode rules; switch to block once CI is green.\n\n" +
			"default_mode: warn\nrules:\n" + items, nil
	}
	lines := strings.Split(strings.TrimRight(existing, "\n"), "\n")
	rulesIdx := -1
	for i, line := range lines {
		if !strings.HasPrefix(line, "rules:") {
			continue
		}
		remainder := strings.TrimSpace(strings.TrimPrefix(line, "rules:"))
		switch remainder {
		case "":
			// Block form; append into it.
		case "[]":
			lines[i] = "rules:"
		default:
			return "", fmt.Errorf(".reconc.yml declares an inline rules list (`rules: %s`); convert it to block form before running `reconc adopt --apply`", remainder)
		}
		rulesIdx = i
		break
	}
	if rulesIdx < 0 {
		lines = append(lines, "rules:")
		rulesIdx = len(lines) - 1
	}
	insertAt := len(lines)
	for i := rulesIdx + 1; i < len(lines); i++ {
		if topLevelKeyRegex.MatchString(lines[i]) {
			insertAt = i
			break
		}
	}
	out := make([]string, 0, len(lines)+8)
	out = append(out, lines[:insertAt]...)
	out = append(out, strings.Split(strings.TrimRight(items, "\n"), "\n")...)
	out = append(out, lines[insertAt:]...)
	return strings.Join(out, "\n") + "\n", nil
}

func validateAdoptConfig(content string) (map[string]struct{}, error) {
	if strings.TrimSpace(content) == "" {
		content = "rules: []\n"
	}
	bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{{
		Kind:    policy.SourceCompilerConfig,
		Path:    ".reconc.yml",
		Content: content,
	}}}
	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(parsed.Rules))
	for _, rule := range parsed.Rules {
		ids[rule.ID] = struct{}{}
	}
	return ids, nil
}

// ToJSON serialises a Report for machine consumption.
func ToJSON(r Report, indent bool) ([]byte, error) {
	if indent {
		return json.MarshalIndent(r, "", "  ")
	}
	return json.Marshal(r)
}

// -------- helpers (tiny, stdlib-free-ish where useful) ---------------

func exists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func isDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func contains(haystack []byte, needle string) bool {
	return bytes.Contains(haystack, []byte(needle))
}

type packageJSONDocument struct {
	Scripts map[string]string `json:"scripts"`
}

func detectRootJSRunner(packageManagers map[string][]string) (string, bool) {
	managers := []string{}
	for _, manager := range []string{"bun", "npm", "pnpm", "yarn"} {
		for _, evidence := range packageManagers[manager] {
			if !strings.Contains(filepath.ToSlash(evidence), "/") {
				managers = append(managers, manager)
				break
			}
		}
	}
	if len(managers) != 1 {
		return "", false
	}
	return managers[0] + " run", true
}

func packageScriptSuggestion(runner, script string) Suggestion {
	labels := map[string]struct {
		id      string
		message string
		reason  string
	}{
		"test":      {id: "adopt-js-tests", message: "Run the JS/TS test suite before declaring done.", reason: "package.json declares a non-empty 'test' script"},
		"lint":      {id: "adopt-js-lint", message: "Run the JS/TS linter before declaring done.", reason: "package.json declares a non-empty 'lint' script"},
		"build":     {id: "adopt-js-build", message: "Run the JS/TS build before declaring done.", reason: "package.json declares a non-empty 'build' script"},
		"typecheck": {id: "adopt-ts-typecheck", message: "Run the TypeScript typecheck before declaring done.", reason: "package.json declares a non-empty 'typecheck' script"},
	}
	label := labels[script]
	return Suggestion{
		ID: label.id, Kind: "require_command", Mode: "warn", Message: label.message,
		WhenPaths: []string{"**/*.{js,jsx,ts,tsx,mjs,cjs,mts,cts}"}, Commands: []string{runner + " " + script},
		Evidence: []string{"package.json"}, Reason: label.reason,
	}
}

func rootTypeScriptConfig(repoRoot string) string {
	paths := []string{}
	if exists(filepath.Join(repoRoot, "tsconfig.json")) {
		paths = append(paths, filepath.Join(repoRoot, "tsconfig.json"))
	}
	variants, err := filepath.Glob(filepath.Join(repoRoot, "tsconfig.*.json"))
	if err == nil {
		paths = append(paths, variants...)
	}
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		if exists(path) {
			names = append(names, filepath.Base(path))
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func matchingManifestStack(selectors, detected []string) string {
	for _, selector := range selectors {
		for _, stack := range detected {
			if strings.EqualFold(selector, stack) {
				return stack
			}
		}
	}
	return ""
}

func quoteYAML(s string) string {
	return strconv.Quote(s)
}

func joinQuoted(xs []string) string {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = quoteYAML(x)
	}
	return strings.Join(quoted, ", ")
}
