package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestScanEmptyRepoProducesNoSuggestions(t *testing.T) {
	repo := t.TempDir()
	r := mustScan(t, repo)
	if len(r.Suggestions) != 0 {
		t.Errorf("expected 0 suggestions for empty repo, got %d", len(r.Suggestions))
	}
	if len(r.Detected) != 0 {
		t.Errorf("expected 0 detected, got %v", r.Detected)
	}
}

func TestScanNodeRepoWithTestScript(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "package.json"), `{"scripts":{"test":"vitest","lint":"eslint ."}}`)
	mustWrite(t, filepath.Join(repo, "bun.lockb"), "")

	r := mustScan(t, repo)
	ids := collectIDs(r)
	for _, want := range []string{"adopt-js-tests", "adopt-js-lint"} {
		if !containsString(ids, want) {
			t.Errorf("expected suggestion %q; got %v", want, ids)
		}
	}
	// Bun runner should be detected (bun.lockb present).
	for _, s := range r.Suggestions {
		if s.ID == "adopt-js-tests" && (len(s.Commands) == 0 || !strings.HasPrefix(s.Commands[0], "bun ")) {
			t.Errorf("expected 'bun ' runner for JS tests, got %v", s.Commands)
		}
	}
	// All command-based rules must carry when_paths.
	for _, s := range r.Suggestions {
		if s.Kind == "require_command" && len(s.WhenPaths) == 0 {
			t.Errorf("require_command %s is missing when_paths", s.ID)
		}
	}
}

func TestScanNodeRepoRequiresRealScriptsAndUnambiguousManager(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "package.json"), `{"packageManager":"pnpm@10.0.0","scripts":{"test":"vitest","lint":" ","build":"vite build","typecheck":"tsc --noEmit"}}`)
	mustWrite(t, filepath.Join(repo, "tsconfig.build.json"), "{}")

	report := mustScan(t, repo)
	ids := collectIDs(report)
	for _, want := range []string{"adopt-js-tests", "adopt-js-build", "adopt-ts-typecheck"} {
		if !containsString(ids, want) {
			t.Errorf("expected evidence-backed suggestion %q; got %v", want, ids)
		}
	}
	if containsString(ids, "adopt-js-lint") {
		t.Fatalf("empty lint script must not become a gate: %v", ids)
	}
	for _, suggestion := range report.Suggestions {
		if len(suggestion.Commands) > 0 && !strings.HasPrefix(suggestion.Commands[0], "pnpm run ") {
			t.Fatalf("metadata-selected command = %v", suggestion.Commands)
		}
	}
	if !containsString(report.Detected, "tsconfig.build.json") {
		t.Fatalf("TypeScript config evidence = %v", report.Detected)
	}
}

func TestScanNodeRepoDoesNotGuessManagerOrInventTypecheck(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "package.json"), `{"scripts":{"test":"node --test"}}`)
	mustWrite(t, filepath.Join(repo, "tsconfig.json"), "{}")

	report := mustScan(t, repo)
	if len(report.Suggestions) != 0 {
		t.Fatalf("manager-less package must not receive guessed commands: %+v", report.Suggestions)
	}
	if !containsString(report.Detected, "tsconfig.json") {
		t.Fatalf("tsconfig should remain visible evidence: %v", report.Detected)
	}
}

func TestScanNodeRepoSurfacesManagerConflictWithoutChoosing(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "package.json"), `{"packageManager":"pnpm@10.0.0","scripts":{"test":"vitest"}}`)
	mustWrite(t, filepath.Join(repo, "package-lock.json"), "{}")

	report := mustScan(t, repo)
	if len(report.Ambiguities) != 1 || !strings.Contains(report.Ambiguities[0], "npm, pnpm") {
		t.Fatalf("ambiguities = %v", report.Ambiguities)
	}
	if len(report.Suggestions) != 0 {
		t.Fatalf("ambiguous manager must not produce a command: %+v", report.Suggestions)
	}
	for _, rendered := range []string{RenderText(report), RenderYAML(report)} {
		if !strings.Contains(rendered, "npm, pnpm") || !strings.Contains(strings.ToLower(rendered), "review") {
			t.Fatalf("ambiguity missing from output:\n%s", rendered)
		}
	}
}

func TestScanPythonRepoWithRuffAndPytest(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "pyproject.toml"), "[tool.ruff]\n[tool.pytest.ini_options]\n[tool.mypy]\n")
	r := mustScan(t, repo)
	ids := collectIDs(r)
	for _, want := range []string{"adopt-py-ruff", "adopt-py-pytest", "adopt-py-mypy"} {
		if !containsString(ids, want) {
			t.Errorf("expected suggestion %q; got %v", want, ids)
		}
	}
	if len(r.PackSuggestions) != 1 || r.PackSuggestions[0].Name != "python-assurance" {
		t.Fatalf("expected review-only python-assurance recommendation, got %+v", r.PackSuggestions)
	}
}

func TestScanRustRepoSuggestsCargoTestAndClippy(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "Cargo.toml"), "[package]\nname = \"demo\"\nversion = \"0.1.0\"\n")
	r := mustScan(t, repo)
	ids := collectIDs(r)
	for _, want := range []string{"adopt-rust-test", "adopt-rust-clippy"} {
		if !containsString(ids, want) {
			t.Errorf("expected suggestion %q; got %v", want, ids)
		}
	}
	if len(r.PackSuggestions) != 1 || r.PackSuggestions[0].Name != "rust-assurance" {
		t.Fatalf("expected review-only rust-assurance recommendation, got %+v", r.PackSuggestions)
	}
}

func TestScanGoRepoSuggestsTestAndVet(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "go.mod"), "module demo\ngo 1.22\n")
	r := mustScan(t, repo)
	ids := collectIDs(r)
	for _, want := range []string{"adopt-go-test", "adopt-go-vet"} {
		if !containsString(ids, want) {
			t.Errorf("expected suggestion %q; got %v", want, ids)
		}
	}
	if len(r.PackSuggestions) != 1 || r.PackSuggestions[0].Name != "go-assurance" {
		t.Fatalf("expected review-only go-assurance recommendation, got %+v", r.PackSuggestions)
	}
}

func TestScanSuggestsPortableAssurancePacksFromNestedEvidence(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "scripts", "check.sh"), "#!/bin/sh\n")
	mustWrite(t, filepath.Join(repo, "native", "main.cpp"), "int main() { return 0; }\n")
	mustWrite(t, filepath.Join(repo, "java", "pom.xml"), "<project/>\n")
	mustWrite(t, filepath.Join(repo, "php", "composer.json"), "{}\n")
	mustWrite(t, filepath.Join(repo, "dotnet", "App.csproj"), "<Project/>\n")

	report := mustScan(t, repo)
	names := make([]string, 0, len(report.PackSuggestions))
	for _, suggestion := range report.PackSuggestions {
		names = append(names, suggestion.Name)
		if len(suggestion.Evidence) == 0 {
			t.Errorf("pack suggestion %s has no evidence", suggestion.Name)
		}
	}
	for _, expected := range []string{"cpp-assurance", "csharp-assurance", "java-assurance", "php-assurance", "shell-assurance"} {
		if !containsString(names, expected) {
			t.Fatalf("missing %s suggestion: %+v", expected, report.PackSuggestions)
		}
	}
}

func TestScanSuggestsFrameworkAndAdditionalLanguagePacksFromNestedEvidence(t *testing.T) {
	t.Setenv("PATH", "")
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "web", "next", "package.json"), "{\"dependencies\":{\"next\":\"16.0.0\"}}\n")
	mustWrite(t, filepath.Join(repo, "web", "svelte", "package.json"), "{\"dependencies\":{\"svelte\":\"5.0.0\"}}\n")
	mustWrite(t, filepath.Join(repo, "native", "build.zig"), "const std = @import(\"std\");\n")
	mustWrite(t, filepath.Join(repo, "services", "elixir", "mix.exs"), "defmodule Demo.MixProject do\nend\n")
	mustWrite(t, filepath.Join(repo, "scripts", "check.ps1"), "exit 0\n")

	report := mustScan(t, repo)
	names := make([]string, 0, len(report.PackSuggestions))
	for _, suggestion := range report.PackSuggestions {
		names = append(names, suggestion.Name)
		if len(suggestion.Evidence) == 0 {
			t.Errorf("pack suggestion %s has no evidence", suggestion.Name)
		}
	}
	for _, expected := range []string{"elixir-assurance", "nextjs-assurance", "powershell-assurance", "svelte-assurance", "zig-assurance"} {
		if !containsString(names, expected) {
			t.Fatalf("missing %s suggestion: %+v", expected, report.PackSuggestions)
		}
	}
}

func TestScanCIAndGeneratedDirs(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), "on: push\n")
	if err := os.MkdirAll(filepath.Join(repo, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := mustScan(t, repo)
	ids := collectIDs(r)
	for _, want := range []string{"adopt-ci-green-gate", "adopt-generated-dist", "adopt-generated-generated"} {
		if !containsString(ids, want) {
			t.Errorf("expected suggestion %q; got %v", want, ids)
		}
	}
	// require_claim must have when_paths.
	for _, s := range r.Suggestions {
		if s.Kind == "require_claim" && len(s.WhenPaths) == 0 {
			t.Errorf("require_claim %s missing when_paths", s.ID)
		}
	}
}

func TestScanReportsMalformedManifestAndPartialWorkflowEnumeration(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "package.json"), "{")
	workflowDir := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxAdoptWorkflowEntries; index++ {
		mustWrite(t, filepath.Join(workflowDir, fmt.Sprintf("%04d.yml", index)), "on: push\n")
	}
	report := mustScan(t, repo)
	joined := strings.Join(report.Ambiguities, "\n")
	if !strings.Contains(joined, "package.json is malformed") ||
		!strings.Contains(joined, "could not be inspected completely") ||
		!strings.Contains(joined, "exceeds 4096 directory entries") {
		t.Fatalf("partial scan diagnostics = %v", report.Ambiguities)
	}
	if containsString(collectIDs(report), "adopt-ci-green-gate") {
		t.Fatalf("partial workflow scan produced a CI suggestion: %+v", report.Suggestions)
	}
}

func TestRenderYAMLEmpty(t *testing.T) {
	yaml := RenderYAML(Report{RepoRoot: "/x"})
	if !strings.Contains(yaml, "no suggestions") {
		t.Errorf("expected empty-state comment, got: %s", yaml)
	}
}

func TestRenderYAMLIncludesAllFields(t *testing.T) {
	r := Report{
		RepoRoot: "/demo",
		Suggestions: []Suggestion{
			{
				ID: "r1", Kind: "require_command", Mode: "warn",
				Message: "run tests", WhenPaths: []string{"**/*.go"},
				Commands: []string{"go test ./..."},
				Evidence: []string{"go.mod"}, Reason: "Go repo",
			},
			{
				ID: "r2", Kind: "deny_write", Mode: "warn",
				Message: "no build output edits", Paths: []string{"dist/**"},
				Evidence: []string{"dist/"}, Reason: "dist dir exists",
			},
		},
	}
	yaml := RenderYAML(r)
	for _, want := range []string{
		"- id: r1", "kind: require_command", "when_paths: [\"**/*.go\"]",
		"commands: [\"go test ./...\"]", "- id: r2", "paths: [\"dist/**\"]",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("RenderYAML missing %q; got:\n%s", want, yaml)
		}
	}
}

func TestApplyCreatesMissingConfig(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "go.mod"), "module demo\n")
	r := mustScan(t, repo)
	added, err := Apply(repo, r)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(added) == 0 {
		t.Fatal("expected at least one rule added")
	}
	data, err := os.ReadFile(filepath.Join(repo, ".reconc.yml"))
	if err != nil {
		t.Fatalf("read .reconc.yml: %v", err)
	}
	got := string(data)
	for _, want := range []string{"rules:", "- id: adopt-go-test", "when_paths: [\"**/*.go\"]"} {
		if !strings.Contains(got, want) {
			t.Errorf("Apply output missing %q; got:\n%s", want, got)
		}
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "go.mod"), "module demo\n")
	r := mustScan(t, repo)
	firstAdded, err := Apply(repo, r)
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	// Second apply must not duplicate.
	secondAdded, err := Apply(repo, r)
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if len(secondAdded) != 0 {
		t.Errorf("expected 0 rules added on second apply, got %v", secondAdded)
	}
	if len(firstAdded) == 0 {
		t.Errorf("expected at least one rule added on first apply")
	}
}

func TestApplyAppendsToExistingConfig(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "go.mod"), "module demo\n")
	// Pre-existing .reconc.yml with rules: key but no adopt-go-test.
	pre := "default_mode: warn\nrules:\n  - id: existing-rule\n    kind: deny_write\n    paths: ['secret/**']\n    mode: block\n    message: no\n"
	mustWrite(t, filepath.Join(repo, ".reconc.yml"), pre)
	r := mustScan(t, repo)
	_, err := Apply(repo, r)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".reconc.yml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "existing-rule") {
		t.Errorf("pre-existing rule should be preserved; got:\n%s", got)
	}
	if !strings.Contains(got, "adopt-go-test") {
		t.Errorf("new rule should be added; got:\n%s", got)
	}
	if strings.Contains(got, "go-assurance") {
		t.Errorf("adopt --apply must never add inferred packs; got:\n%s", got)
	}
}

func TestToJSON(t *testing.T) {
	r := Report{RepoRoot: "/x", Detected: []string{"go.mod"}, Suggestions: []Suggestion{{ID: "id1", Kind: "require_command"}}}
	data, err := ToJSON(r, false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"id":"id1"`) {
		t.Errorf("JSON missing id field: %s", s)
	}
}

func TestRenderTextPackOnlyDoesNotSuggestApplyingRules(t *testing.T) {
	report := Report{
		RepoRoot: "/repo",
		Detected: []string{"go.mod"},
		PackSuggestions: []PackSuggestion{{
			Name: "go-assurance", DetectedStack: "go", Reason: "Go assurance",
		}},
	}
	text := RenderText(report)
	if strings.Contains(text, "--apply") || !strings.Contains(text, "add selected names to extends manually") {
		t.Fatalf("pack-only next action must remain explicit and review-only:\n%s", text)
	}
	yaml := RenderYAML(report)
	if strings.Contains(yaml, "Paste the rule body") || !strings.Contains(yaml, "adopt --apply never changes extends") {
		t.Fatalf("pack-only YAML must not imply a rule body exists:\n%s", yaml)
	}
}

// -------- helpers -----------

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func collectIDs(r Report) []string {
	ids := make([]string, 0, len(r.Suggestions))
	for _, s := range r.Suggestions {
		ids = append(ids, s.ID)
	}
	return ids
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func mustScan(t *testing.T, repo string) Report {
	t.Helper()
	report, err := Scan(repo)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return report
}

func TestApplyAfterInitScaffoldProducesValidYAML(t *testing.T) {
	repo := t.TempDir()
	// The `reconc init` scaffold declares an inline empty list. Adopt
	// must convert it to block form, not append orphaned items.
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("default_mode: warn\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Scan(repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	added, err := Apply(repo, report)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(added) == 0 {
		t.Fatal("expected at least one suggestion for a Go repo")
	}
	body, err := os.ReadFile(filepath.Join(repo, ".reconc.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "rules: []") {
		t.Fatalf("inline empty list must be converted to block form:\n%s", body)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("adopted config must stay valid YAML: %v\n%s", err, body)
	}
	rules, ok := doc["rules"].([]interface{})
	if !ok || len(rules) != len(added) {
		t.Fatalf("expected %d parsed rules, got %#v", len(added), doc["rules"])
	}
}

func TestApplyInsertsBeforeTrailingTopLevelKeys(t *testing.T) {
	repo := t.TempDir()
	config := "default_mode: warn\nrules:\n  - id: existing\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\nextends:\n  - default\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Scan(repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, err := Apply(repo, report); err != nil {
		t.Fatalf("apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".reconc.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("config must stay valid YAML: %v\n%s", err, body)
	}
	// New items must land inside rules, not under extends.
	extends, ok := doc["extends"].([]interface{})
	if !ok || len(extends) != 1 {
		t.Fatalf("extends must stay a one-element list, got %#v", doc["extends"])
	}
	rules, ok := doc["rules"].([]interface{})
	if !ok || len(rules) < 2 {
		t.Fatalf("new rules must join the rules block, got %#v", doc["rules"])
	}
}

func TestApplyLineAnchoredIDDedup(t *testing.T) {
	repo := t.TempDir()
	// A rule id appearing inside a message must not suppress adoption,
	// and a longer id sharing the prefix must not either.
	config := "default_mode: warn\nrules:\n  - id: adopt-go-test-extended\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: 'mentions adopt-go-test inline'\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Scan(repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	added, err := Apply(repo, report)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	found := false
	for _, id := range added {
		if strings.HasPrefix(id, "adopt-go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("prefix-sharing id and message mention must not block adoption; added=%v", added)
	}
}

func TestApplyRecognizesQuotedRuleID(t *testing.T) {
	repo := t.TempDir()
	config := "default_mode: warn\nrules:\n  - id: \"adopt-go-test\"\n    kind: require_command\n    when_paths: ['**/*.go']\n    commands: ['go test ./...']\n    mode: warn\n    message: tests\n"
	mustWrite(t, filepath.Join(repo, ".reconc.yml"), config)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module x\n\ngo 1.26\n")
	report := mustScan(t, repo)
	added, err := Apply(repo, report)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(added, "adopt-go-test") {
		t.Fatalf("quoted existing ID was duplicated: %v", added)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".reconc.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "adopt-go-test") != 1 {
		t.Fatalf("quoted existing ID must remain unique:\n%s", body)
	}
}

func TestApplyRejectsInvalidExistingConfigWithoutMutation(t *testing.T) {
	repo := t.TempDir()
	configPath := filepath.Join(repo, ".reconc.yml")
	original := "default_mode: warn\nrules: []\nunknown_root_field: true\n"
	mustWrite(t, configPath, original)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module x\n\ngo 1.26\n")
	_, err := Apply(repo, mustScan(t, repo))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("invalid existing config must fail closed, got %v", err)
	}
	body, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != original {
		t.Fatalf("failed adoption mutated config:\n%s", body)
	}
}

func TestApplyRejectsDuplicateCandidateIDsWithoutMutation(t *testing.T) {
	repo := t.TempDir()
	configPath := filepath.Join(repo, ".reconc.yml")
	original := "default_mode: warn\nrules: []\n"
	mustWrite(t, configPath, original)
	report := Report{Suggestions: []Suggestion{
		{ID: "duplicate", Kind: "deny_write", Mode: "warn", Message: "one", Paths: []string{"one/**"}},
		{ID: "duplicate", Kind: "deny_write", Mode: "warn", Message: "two", Paths: []string{"two/**"}},
	}}
	if _, err := Apply(repo, report); err == nil || !strings.Contains(err.Error(), "duplicate rule id") {
		t.Fatalf("duplicate candidate IDs must fail closed, got %v", err)
	}
	body, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != original {
		t.Fatalf("failed adoption mutated config:\n%s", body)
	}
}
