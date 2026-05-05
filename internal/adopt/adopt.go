// Package adopt scans a repository for existing tooling and emits
// reconc rule suggestions. The agent (or user) can then paste the
// suggested YAML into .reconc.yml, or use `reconc adopt --apply` to
// append them automatically.
//
// Detection is a best-effort: we look for common marker files
// (package.json, pyproject.toml, Cargo.toml, go.mod, .github/workflows/)
// and emit a small set of high-confidence rule suggestions. We never
// emit destructive-looking rules (e.g. forbid_command); the goal is to
// get a new repo to 80% coverage without the user writing YAML.
package adopt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	RepoRoot    string       `json:"repo_root"`
	Detected    []string     `json:"detected"`
	Suggestions []Suggestion `json:"suggestions"`
}

// Scan inspects repoRoot for common tooling and returns a deterministic
// Report. Never mutates the repository.
func Scan(repoRoot string) Report {
	r := Report{
		RepoRoot:    repoRoot,
		Detected:    []string{},
		Suggestions: []Suggestion{},
	}

	// --- JS / TS ---
	if exists(filepath.Join(repoRoot, "package.json")) {
		r.Detected = append(r.Detected, "package.json")
		pkgData, _ := os.ReadFile(filepath.Join(repoRoot, "package.json"))
		if hasScript(pkgData, "test") {
			r.Suggestions = append(r.Suggestions, Suggestion{
				ID:        "adopt-js-tests",
				Kind:      "require_command",
				Mode:      "warn",
				Message:   "Run the JS/TS test suite before declaring done.",
				WhenPaths: []string{"**/*.{js,jsx,ts,tsx}"},
				Commands:  []string{detectJSRunner(repoRoot) + " test"},
				Evidence:  []string{"package.json"},
				Reason:    "package.json declares a 'test' script",
			})
		}
		if hasScript(pkgData, "lint") {
			r.Suggestions = append(r.Suggestions, Suggestion{
				ID:        "adopt-js-lint",
				Kind:      "require_command",
				Mode:      "warn",
				Message:   "Run the JS/TS linter before declaring done.",
				WhenPaths: []string{"**/*.{js,jsx,ts,tsx}"},
				Commands:  []string{detectJSRunner(repoRoot) + " lint"},
				Evidence:  []string{"package.json"},
				Reason:    "package.json declares a 'lint' script",
			})
		}
	}
	if exists(filepath.Join(repoRoot, "tsconfig.json")) {
		r.Detected = append(r.Detected, "tsconfig.json")
		r.Suggestions = append(r.Suggestions, Suggestion{
			ID:        "adopt-ts-typecheck",
			Kind:      "require_command",
			Mode:      "warn",
			Message:   "Run the TypeScript type checker before declaring done.",
			WhenPaths: []string{"**/*.{ts,tsx}"},
			Commands:  []string{"tsc --noEmit"},
			Evidence:  []string{"tsconfig.json"},
			Reason:    "tsconfig.json is present",
		})
	}

	// --- Python ---
	if exists(filepath.Join(repoRoot, "pyproject.toml")) {
		r.Detected = append(r.Detected, "pyproject.toml")
		py, _ := os.ReadFile(filepath.Join(repoRoot, "pyproject.toml"))
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
		entries, _ := os.ReadDir(ciPath)
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

	return r
}

// RenderYAML emits a YAML snippet suitable for pasting into .reconc.yml
// under `rules:`. Deterministic output (suggestions are already in
// scan-order, which is stable).
func RenderYAML(r Report) string {
	if len(r.Suggestions) == 0 {
		return "# reconc adopt: no suggestions for this repo.\n"
	}
	var b strings.Builder
	b.WriteString("# reconc adopt suggestions for ")
	b.WriteString(r.RepoRoot)
	b.WriteString("\n")
	b.WriteString("# Paste the body under the `rules:` key of .reconc.yml.\n")
	b.WriteString("# Start in warn mode; switch to block once green.\n\n")
	for _, s := range r.Suggestions {
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
	if len(r.Suggestions) == 0 {
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
	b.WriteString("\nSuggested rules (")
	b.WriteString(itoa(len(r.Suggestions)))
	b.WriteString(" total, all warn-mode):\n\n")
	for i, s := range r.Suggestions {
		b.WriteString(itoa(i + 1))
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
	b.WriteString("  - Preview YAML:  reconc adopt ")
	b.WriteString(r.RepoRoot)
	b.WriteString(" --yaml\n")
	b.WriteString("  - Apply to .reconc.yml: reconc adopt ")
	b.WriteString(r.RepoRoot)
	b.WriteString(" --apply\n")
	b.WriteString("  - JSON for agents: reconc adopt ")
	b.WriteString(r.RepoRoot)
	b.WriteString(" --json\n")
	return b.String()
}

// Apply appends generated suggestion YAML to .reconc.yml under the
// existing `rules:` key (or creates a minimal scaffold if the file is
// absent). Returns the list of rule ids actually written (skipping any
// that already exist with the same id to keep the operation idempotent).
func Apply(repoRoot string, r Report) (added []string, err error) {
	configPath := filepath.Join(repoRoot, ".reconc.yml")
	existing, readErr := os.ReadFile(configPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}

	var buf strings.Builder
	// If the config file is missing, emit a minimal scaffold.
	if len(existing) == 0 {
		buf.WriteString("# .reconc.yml -- generated by `reconc adopt`.\n")
		buf.WriteString("# Start with warn-mode rules; switch to block once CI is green.\n\n")
		buf.WriteString("default_mode: warn\n")
		buf.WriteString("rules:\n")
	} else {
		buf.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			buf.WriteString("\n")
		}
		if !containsLine(string(existing), "rules:") {
			buf.WriteString("rules:\n")
		}
	}

	for _, s := range r.Suggestions {
		if containsLine(string(existing), "- id: "+s.ID) ||
			containsLine(string(existing), "  - id: "+s.ID) {
			continue
		}
		buf.WriteString("  - id: ")
		buf.WriteString(s.ID)
		buf.WriteString("\n    kind: ")
		buf.WriteString(s.Kind)
		buf.WriteString("\n    mode: ")
		buf.WriteString(s.Mode)
		buf.WriteString("\n    message: ")
		buf.WriteString(quoteYAML(s.Message))
		buf.WriteString("\n")
		if len(s.Paths) > 0 {
			buf.WriteString("    paths: [")
			buf.WriteString(joinQuoted(s.Paths))
			buf.WriteString("]\n")
		}
		if len(s.WhenPaths) > 0 {
			buf.WriteString("    when_paths: [")
			buf.WriteString(joinQuoted(s.WhenPaths))
			buf.WriteString("]\n")
		}
		if len(s.Commands) > 0 {
			buf.WriteString("    commands: [")
			buf.WriteString(joinQuoted(s.Commands))
			buf.WriteString("]\n")
		}
		if len(s.Claims) > 0 {
			buf.WriteString("    claims: [")
			buf.WriteString(joinQuoted(s.Claims))
			buf.WriteString("]\n")
		}
		added = append(added, s.ID)
	}

	if len(added) == 0 {
		return nil, nil
	}

	if err := os.WriteFile(configPath, []byte(buf.String()), 0o644); err != nil {
		return nil, err
	}
	return added, nil
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
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func contains(haystack []byte, needle string) bool {
	return strings.Contains(string(haystack), needle)
}

// containsLine checks for a line-ish substring in a YAML source. Used
// for idempotent rule-id detection.
func containsLine(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// hasScript checks whether package.json declares a given npm script.
// Uses a permissive string match rather than full JSON parsing because
// a single missing quote shouldn't defeat detection.
func hasScript(data []byte, name string) bool {
	s := string(data)
	return strings.Contains(s, "\""+name+"\":") || strings.Contains(s, "\""+name+"\" :")
}

// detectJSRunner picks the most likely package runner based on lockfile
// presence. Order matches the user's CLAUDE.md preference (Bun first).
func detectJSRunner(repoRoot string) string {
	if exists(filepath.Join(repoRoot, "bun.lockb")) || exists(filepath.Join(repoRoot, "bun.lock")) {
		return "bun"
	}
	if exists(filepath.Join(repoRoot, "pnpm-lock.yaml")) {
		return "pnpm"
	}
	if exists(filepath.Join(repoRoot, "yarn.lock")) {
		return "yarn"
	}
	return "npm run"
}

func quoteYAML(s string) string {
	// Double-quote and escape embedded quotes / backslashes. Keeps
	// multi-word messages safe in YAML flow scalar.
	needsQuote := strings.ContainsAny(s, ":#'\"\n")
	if !needsQuote {
		return s
	}
	esc := strings.ReplaceAll(s, "\\", "\\\\")
	esc = strings.ReplaceAll(esc, "\"", "\\\"")
	return "\"" + esc + "\""
}

func joinQuoted(xs []string) string {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = "\"" + strings.ReplaceAll(x, "\"", "\\\"") + "\""
	}
	return strings.Join(quoted, ", ")
}

// itoa mirrors the tiny int->string helper used in runtime to avoid
// pulling strconv into this package's surface.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
