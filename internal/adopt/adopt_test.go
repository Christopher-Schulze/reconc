package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
