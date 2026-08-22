package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/hooks"
)

func bootstrapTestHome(t *testing.T) {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
}

func TestInspectIsReadOnlyAndSuggestsApplicablePacks(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	writeBootstrapTestFile(t, repo, "go.mod", "module example\n", 0o644)
	writeBootstrapTestFile(t, repo, "package.json", "{}\n", 0o644)
	writeBootstrapTestFile(t, repo, "bun.lock", "lock\n", 0o644)
	writeBootstrapTestFile(t, repo, ".codex/config.toml", "[features]\nhooks = true\n", 0o644)
	before := bootstrapTreeSnapshot(t, repo)

	inspection, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	after := bootstrapTreeSnapshot(t, repo)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("inspect mutated the repository:\nbefore=%v\nafter=%v", before, after)
	}
	if strings.Join(inspection.DetectedStacks, ",") != "bun,go,javascript" {
		t.Fatalf("detected stacks = %v", inspection.DetectedStacks)
	}
	for _, expected := range []string{"bun-assurance", "go-assurance"} {
		if !containsString(inspection.PackSuggestions, expected) {
			t.Fatalf("inspection missing %s suggestion: %+v", expected, inspection)
		}
	}
	if !containsString(inspection.DetectedPlatforms, hooks.KindCodex) {
		t.Fatalf("inspection missed Codex config: %+v", inspection)
	}
	if inspection.DetectionState != "known" || len(inspection.StackEvidence) != 3 {
		t.Fatalf("inspection lost detection evidence: %+v", inspection)
	}
	if len(inspection.PackageManagers) != 2 {
		t.Fatalf("inspection package-manager evidence = %+v", inspection.PackageManagers)
	}
	foundCodexStatus := false
	for _, status := range inspection.PlatformStatuses {
		if status.Kind == hooks.KindCodex {
			foundCodexStatus = true
			if !containsString(status.Evidence, ".codex") || status.State == "" {
				t.Fatalf("Codex status lacks evidence or state: %+v", status)
			}
		}
	}
	if !foundCodexStatus {
		t.Fatalf("inspection lacks Codex platform status: %+v", inspection.PlatformStatuses)
	}
}

func TestInspectSuggestsPythonAndRustAssurancePacks(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	writeBootstrapTestFile(t, repo, "pyproject.toml", "[project]\nname = 'example'\n", 0o644)
	writeBootstrapTestFile(t, repo, "Cargo.toml", "[package]\nname = \"example\"\nversion = \"0.1.0\"\n", 0o644)

	inspection, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(inspection.DetectedStacks, ",") != "python,rust" {
		t.Fatalf("detected stacks = %v", inspection.DetectedStacks)
	}
	for _, expected := range []string{"python-assurance", "rust-assurance"} {
		if !containsString(inspection.PackSuggestions, expected) {
			t.Fatalf("inspection missing %s suggestion: %+v", expected, inspection)
		}
	}
}

func TestInspectSuggestsPortableAssurancePacksWithoutToolchains(t *testing.T) {
	bootstrapTestHome(t)
	t.Setenv("PATH", "")
	repo := t.TempDir()
	writeBootstrapTestFile(t, repo, "scripts/check.sh", "#!/bin/sh\n", 0o755)
	writeBootstrapTestFile(t, repo, "native/main.cpp", "int main() { return 0; }\n", 0o644)
	writeBootstrapTestFile(t, repo, "java/pom.xml", "<project/>\n", 0o644)
	writeBootstrapTestFile(t, repo, "php/composer.json", "{}\n", 0o644)
	writeBootstrapTestFile(t, repo, "dotnet/App.csproj", "<Project/>\n", 0o644)

	inspection, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	wantStacks := "cpp,csharp,java,php,shell"
	if strings.Join(inspection.DetectedStacks, ",") != wantStacks {
		t.Fatalf("detected stacks = %v, want %s", inspection.DetectedStacks, wantStacks)
	}
	for _, expected := range []string{"cpp-assurance", "csharp-assurance", "java-assurance", "php-assurance", "shell-assurance"} {
		if !containsString(inspection.PackSuggestions, expected) {
			t.Fatalf("inspection missing %s suggestion: %+v", expected, inspection)
		}
	}
}

func TestInspectSuggestsFrameworkAndAdditionalLanguagePacksWithoutToolchains(t *testing.T) {
	bootstrapTestHome(t)
	t.Setenv("PATH", "")
	repo := t.TempDir()
	writeBootstrapTestFile(t, repo, "web/next/package.json", "{\"dependencies\":{\"next\":\"16.0.0\"}}\n", 0o644)
	writeBootstrapTestFile(t, repo, "web/svelte/package.json", "{\"devDependencies\":{\"@sveltejs/kit\":\"2.0.0\"}}\n", 0o644)
	writeBootstrapTestFile(t, repo, "native/build.zig", "const std = @import(\"std\");\n", 0o644)
	writeBootstrapTestFile(t, repo, "services/elixir/mix.exs", "defmodule Demo.MixProject do\nend\n", 0o644)
	writeBootstrapTestFile(t, repo, "scripts/check.ps1", "exit 0\n", 0o644)

	inspection, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	wantStacks := "elixir,javascript,nextjs,powershell,svelte,zig"
	if strings.Join(inspection.DetectedStacks, ",") != wantStacks {
		t.Fatalf("detected stacks = %v, want %s", inspection.DetectedStacks, wantStacks)
	}
	for _, expected := range []string{"elixir-assurance", "nextjs-assurance", "powershell-assurance", "svelte-assurance", "zig-assurance"} {
		if !containsString(inspection.PackSuggestions, expected) {
			t.Fatalf("inspection missing %s suggestion: %+v", expected, inspection)
		}
	}
}

func TestInspectFirstTouchFixtureMatrixStaysEvidenceFocused(t *testing.T) {
	bootstrapTestHome(t)
	tests := []struct {
		name          string
		files         map[string]string
		wantStacks    string
		wantManagers  string
		wantPacks     string
		wantPlatforms string
		wantDetection string
		wantMarker    string
	}{
		{
			name: "go", files: map[string]string{"go.mod": "module example\n"},
			wantStacks: "go", wantManagers: "go-modules", wantPacks: "go-assurance", wantDetection: "known",
		},
		{
			name: "python", files: map[string]string{"pyproject.toml": "[project]\nname = 'example'\n", "uv.lock": "version = 1\n"},
			wantStacks: "python", wantManagers: "uv", wantPacks: "python-assurance", wantDetection: "known",
		},
		{
			name: "generic-node-typescript", files: map[string]string{
				"package.json": "{\"scripts\":{\"test\":\"node --test\"}}\n", "package-lock.json": "{}\n", "tsconfig.json": "{}\n",
			},
			wantStacks: "javascript,npm,typescript", wantManagers: "npm", wantPacks: "npm-assurance,typescript-assurance", wantDetection: "known",
		},
		{
			name: "pnpm-workspace", files: map[string]string{
				"package.json": "{\"packageManager\":\"pnpm@10.0.0\",\"private\":true}\n", "pnpm-lock.yaml": "lockfileVersion: '9.0'\n", "packages/api/package.json": "{\"scripts\":{\"test\":\"vitest\"}}\n",
			},
			wantStacks: "javascript,pnpm", wantManagers: "pnpm", wantPacks: "pnpm-assurance", wantDetection: "known",
		},
		{
			name: "yarn", files: map[string]string{"package.json": "{}\n", "yarn.lock": "# yarn lockfile\n"},
			wantStacks: "javascript,yarn", wantManagers: "yarn", wantPacks: "yarn-assurance", wantDetection: "known",
		},
		{
			name: "bun", files: map[string]string{"package.json": "{}\n", "bun.lock": "lock\n"},
			wantStacks: "bun,javascript", wantManagers: "bun", wantPacks: "bun-assurance", wantDetection: "known",
		},
		{
			name: "nextjs", files: map[string]string{"package.json": "{\"dependencies\":{\"next\":\"16.0.0\"}}\n", "package-lock.json": "{}\n"},
			wantStacks: "javascript,nextjs,npm", wantManagers: "npm", wantPacks: "nextjs-assurance,npm-assurance", wantDetection: "known",
		},
		{
			name: "svelte", files: map[string]string{"package.json": "{\"devDependencies\":{\"svelte\":\"5.0.0\"}}\n", "yarn.lock": "# yarn lockfile\n"},
			wantStacks: "javascript,svelte,yarn", wantManagers: "yarn", wantPacks: "svelte-assurance,yarn-assurance", wantDetection: "known",
		},
		{
			name: "ambiguous-node-manager", files: map[string]string{"package.json": "{}\n", "package-lock.json": "{}\n", "yarn.lock": "# yarn lockfile\n"},
			wantStacks: "javascript,npm,yarn", wantManagers: "npm,yarn", wantPacks: "npm-assurance,yarn-assurance", wantDetection: "ambiguous",
		},
		{
			name: "mixed", files: map[string]string{
				"services/api/go.mod": "module example/api\n", "services/jobs/pyproject.toml": "[project]\nname = 'jobs'\n",
			},
			wantStacks: "go,python", wantManagers: "go-modules", wantPacks: "go-assurance,python-assurance", wantDetection: "known",
		},
		{
			name: "no-agent", files: map[string]string{"go.mod": "module example\n"},
			wantStacks: "go", wantManagers: "go-modules", wantPacks: "go-assurance", wantDetection: "known",
		},
		{
			name: "pre-existing-reconc", files: map[string]string{
				".reconc.yml": "rules: []\n", "AGENTS.md": "# Existing contract\n", ".codex/config.toml": "[features]\nhooks = true\n",
			},
			wantPlatforms: hooks.KindCodex, wantDetection: "unknown", wantMarker: ".reconc.yml,AGENTS.md",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			for path, body := range test.files {
				writeBootstrapTestFile(t, repo, path, body, 0o644)
			}
			inspection, err := Inspect(repo)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(inspection.DetectedStacks, ","); got != test.wantStacks {
				t.Fatalf("stacks = %q, want %q", got, test.wantStacks)
			}
			managerNames := make([]string, 0, len(inspection.PackageManagers))
			for _, manager := range inspection.PackageManagers {
				managerNames = append(managerNames, manager.Name)
			}
			if got := strings.Join(managerNames, ","); got != test.wantManagers {
				t.Fatalf("package managers = %q, want %q", got, test.wantManagers)
			}
			if got := strings.Join(inspection.PackSuggestions, ","); got != test.wantPacks {
				t.Fatalf("pack suggestions = %q, want %q", got, test.wantPacks)
			}
			if got := strings.Join(inspection.DetectedPlatforms, ","); got != test.wantPlatforms {
				t.Fatalf("platforms = %q, want %q", got, test.wantPlatforms)
			}
			if inspection.DetectionState != test.wantDetection {
				t.Fatalf("detection state = %q, want %q", inspection.DetectionState, test.wantDetection)
			}
			if got := strings.Join(inspection.RepositoryMarkers, ","); got != test.wantMarker {
				t.Fatalf("repository markers = %q, want %q", got, test.wantMarker)
			}
		})
	}
}

func TestPlanIsDeterministicAndStrictlyLoadable(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	first, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileGoverned}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileGoverned}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("identical plan inputs are not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}
	planPath := filepath.Join(t.TempDir(), "bootstrap-plan.json")
	if action, err := WritePlan(planPath, first); err != nil || action != "created" {
		t.Fatalf("write plan: action=%s err=%v", action, err)
	}
	if action, err := WritePlan(planPath, first); err != nil || action != "unchanged" {
		t.Fatalf("idempotent plan write: action=%s err=%v", action, err)
	}
	if action, err := ReplacePlan(planPath, second); err != nil || action != "unchanged" {
		t.Fatalf("idempotent plan replace: action=%s err=%v", action, err)
	}
	loaded, err := LoadPlan(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PlanDigest != first.PlanDigest {
		t.Fatalf("loaded digest = %s, want %s", loaded.PlanDigest, first.PlanDigest)
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), `"profile": "governed"`, `"profile": "minimal"`, 1)
	tamperedPath := filepath.Join(t.TempDir(), "tampered.json")
	if err := os.WriteFile(tamperedPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPlan(tamperedPath); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered plan must fail its digest: %v", err)
	}
	oversizedPath := filepath.Join(t.TempDir(), "oversized.json")
	oversized := append(append([]byte{}, data...), []byte(strings.Repeat(" ", int(maxPlanBytes)))...)
	if err := os.WriteFile(oversizedPath, oversized, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPlan(oversizedPath); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized plan must fail before decoding: %v", err)
	}
	foreignPath := filepath.Join(t.TempDir(), "not-a-plan.json")
	if err := os.WriteFile(foreignPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReplacePlan(foreignPath, first); err == nil || !strings.Contains(err.Error(), "refuse to replace non-plan") {
		t.Fatalf("replace accepted an arbitrary file: %v", err)
	}

	invalidSelection := *first
	invalidSelection.Selection = first.Selection
	invalidSelection.Selection.Packs = []string{}
	invalidSelection.PlanDigest, err = computePlanDigest(&invalidSelection)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlan(&invalidSelection); err == nil || !strings.Contains(err.Error(), "default packs") {
		t.Fatalf("digest-valid plan cannot remove profile defaults: %v", err)
	}
}

func TestGovernedApplyVerifyAndRerunAreIdempotent(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	request := Request{
		RepoRoot: repo, Profile: ProfileGoverned,
		Hooks: []string{hooks.KindCodex, hooks.KindGitPreCommit},
	}
	plan, err := BuildPlan(request, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != ApplyComplete || len(report.Candidates) != 0 {
		t.Fatalf("unexpected apply report: %+v", report)
	}
	verification, err := Verify(plan)
	if err != nil || !verification.Valid {
		t.Fatalf("verify: %+v err=%v", verification, err)
	}
	for _, relative := range []string{
		".reconc.yml", ".reconc/policy.lock.json", "AGENTS.md", "docs/tasks.md",
		"docs/tasks/001-bootstrap-reconc.md", "docs/documentation.md", "start.md",
		".gitignore", hooks.WrapperPath, hooks.CodexHooksPath, ".codex/config.toml",
		hooks.GitPreCommitPath,
	} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("expected installed %s: %v", relative, err)
		}
	}
	codexConfig, err := os.ReadFile(filepath.Join(repo, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexConfig), "[features]\n") || !strings.Contains(string(codexConfig), "hooks = true") {
		t.Fatalf("Codex activation is not valid features TOML: %q", codexConfig)
	}
	ignoreBody, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"/tools/reconc/dist/", ".reconc/run/"} {
		if !strings.Contains(string(ignoreBody), expected) {
			t.Fatalf("governed bootstrap ignore block missing %q: body=%q", expected, ignoreBody)
		}
	}
	before := bootstrapTreeSnapshot(t, repo)
	secondPlan, err := BuildPlan(request, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range secondPlan.Actions {
		if action.State != ActionUnchanged {
			t.Fatalf("second plan should be unchanged: %+v", action)
		}
	}
	secondReport, err := Apply(secondPlan, "test-version")
	if err != nil || secondReport.Status != ApplyComplete || len(secondReport.Created) != 0 {
		t.Fatalf("idempotent apply: report=%+v err=%v", secondReport, err)
	}
	after := bootstrapTreeSnapshot(t, repo)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("idempotent apply changed repository bytes:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestAcceptManagedCandidatesPreservesUserBytesAndCompletesReplan(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	original := "# Team instructions\n\nKeep this exact.\n"
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{RepoRoot: repo, Profile: ProfileMinimal}
	plan, err := BuildPlan(request, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil || report.Status != ApplyDrift || !HasManagedCandidates(plan) {
		t.Fatalf("managed drift setup: report=%+v err=%v", report, err)
	}
	accepted, err := AcceptManagedCandidates(plan, report)
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted.Updated) != 1 || accepted.Updated[0] != "AGENTS.md" || len(accepted.RemovedCandidates) != 1 {
		t.Fatalf("managed acceptance report = %+v", accepted)
	}
	body, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), original) || !strings.Contains(string(body), agentBlockStart) {
		t.Fatalf("managed acceptance changed user bytes: %q", body)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(accepted.RemovedCandidates[0]))); !os.IsNotExist(err) {
		t.Fatalf("accepted candidate remains: %v", err)
	}

	replanned, err := BuildPlan(request, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	completed, err := Apply(replanned, "test-version")
	if err != nil || completed.Status != ApplyComplete {
		t.Fatalf("replanned bootstrap did not complete: report=%+v err=%v", completed, err)
	}
	if completed.Summary.Created == 0 || completed.Summary.Drifted != 0 || !completed.Summary.LivenessKnown || !strings.HasPrefix(completed.NextAction, "reconc check ") {
		t.Fatalf("completed bootstrap summary is not decision-complete: %+v", completed)
	}
}

func TestAcceptManagedCandidatesRefusesTargetDriftWithoutMutation(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, "AGENTS.md")
	if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil || report.Status != ApplyDrift {
		t.Fatalf("managed drift setup: report=%+v err=%v", report, err)
	}
	if err := os.WriteFile(target, []byte("changed after candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptManagedCandidates(plan, report); err == nil || !strings.Contains(err.Error(), "drifted since planning") {
		t.Fatalf("target drift acceptance error = %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "changed after candidate\n" {
		t.Fatalf("failed acceptance mutated user target: %q", body)
	}
}

func TestExistingProfileWiresWithoutOwningControlPlane(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	controlFiles := map[string]string{
		".reconc.yml":           "extends:\n  - default\n  - agent\nrules: []\n",
		"AGENTS.md":             "# Existing agent contract\n",
		"docs/documentation.md": "# Existing documentation\n",
		"docs/tasks.md":         "# Existing TASK control plane\n",
	}
	for path, body := range controlFiles {
		writeBootstrapTestFile(t, repo, path, body, 0o644)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test-version"); err != nil {
		t.Fatalf("compile existing policy: %v", err)
	}

	request := Request{RepoRoot: repo, Profile: ProfileExisting, Hooks: []string{hooks.KindCodex}}
	plan, err := BuildPlan(request, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if plan.CompileRequired || len(plan.BlockingIssues) != 0 {
		t.Fatalf("existing profile rejected fresh policy: %+v", plan)
	}
	for _, action := range plan.Actions {
		if _, owned := controlFiles[action.Path]; owned {
			t.Fatalf("existing profile tried to own %s", action.Path)
		}
	}
	report, err := Apply(plan, "test-version")
	if err != nil || report.Status != ApplyComplete {
		t.Fatalf("apply existing profile: report=%+v err=%v", report, err)
	}
	verification, err := Verify(plan)
	if err != nil || !verification.Valid {
		t.Fatalf("verify existing profile: verification=%+v err=%v", verification, err)
	}
	for path, want := range controlFiles {
		body, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(path)))
		if err != nil || string(body) != want {
			t.Fatalf("existing control file changed %s: body=%q err=%v", path, body, err)
		}
	}
	second, err := BuildPlan(request, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range second.Actions {
		if action.State != ActionUnchanged {
			t.Fatalf("existing-profile rerun drifted: %+v", action)
		}
	}
}

func TestExistingProfileRequiresFreshPolicyAndRejectsPacks(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileExisting}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if plan.CompileRequired || len(plan.BlockingIssues) != 1 || !strings.Contains(plan.BlockingIssues[0], "already compiled fresh policy") {
		t.Fatalf("missing-policy existing plan = %+v", plan)
	}
	if report, err := Apply(plan, "test-version"); err == nil || len(report.Created) != 0 {
		t.Fatalf("existing profile applied without policy: report=%+v err=%v", report, err)
	}
	if _, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileExisting, Packs: []string{"default"}}, "test-version"); err == nil || !strings.Contains(err.Error(), "cannot select packs") {
		t.Fatalf("existing profile accepted policy packs: %v", err)
	}

	staleRepo := t.TempDir()
	writeBootstrapTestFile(t, staleRepo, ".reconc.yml", "rules: []\n", 0o644)
	if _, err := compiler.CompileRepoPolicy(staleRepo, "test-version"); err != nil {
		t.Fatalf("compile stale-policy fixture: %v", err)
	}
	writeBootstrapTestFile(t, staleRepo, ".reconc.yml", "rules:\n  - id: changed\n    kind: deny_write\n    paths: [\"generated/**\"]\n", 0o644)
	writeBootstrapTestFile(t, staleRepo, hooks.WrapperPath, "custom wrapper\n", 0o755)
	stalePlan, err := BuildPlan(Request{RepoRoot: staleRepo, Profile: ProfileExisting}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if len(stalePlan.BlockingIssues) != 1 || !strings.Contains(stalePlan.BlockingIssues[0], "not fresh") {
		t.Fatalf("existing profile hid stale policy behind artifact conflict: %+v", stalePlan)
	}
}

func TestDriftCreatesCandidateWithoutInstallingOtherTargets(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	custom := "# User instructions\n\nNever overwrite me.\n"
	writeBootstrapTestFile(t, repo, "AGENTS.md", custom, 0o644)
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != ApplyDrift || len(report.Candidates) != 1 {
		t.Fatalf("expected one drift candidate: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc.yml")); !os.IsNotExist(err) {
		t.Fatalf("drift apply must not install unrelated targets: %v", err)
	}
	current, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil || string(current) != custom {
		t.Fatalf("existing AGENTS.md changed: err=%v content=%q", err, current)
	}
	candidate, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(report.Candidates[0])))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(candidate), custom) || !strings.Contains(string(candidate), agentBlockStart) {
		t.Fatalf("candidate did not preserve user content plus managed block:\n%s", candidate)
	}
	second, err := Apply(plan, "test-version")
	if err != nil || second.Status != ApplyDrift {
		t.Fatalf("candidate rerun should be stable: report=%+v err=%v", second, err)
	}
}

func TestManagedDocumentCandidatesKeepOneManagedBlockAndRejectDuplicateMarkers(t *testing.T) {
	bootstrapTestHome(t)
	emptyRepo := t.TempDir()
	writeBootstrapTestFile(t, emptyRepo, "AGENTS.md", "", 0o644)
	plan, err := BuildPlan(Request{RepoRoot: emptyRepo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil || report.Status != ApplyDrift || len(report.Candidates) != 1 {
		t.Fatalf("empty document drift: report=%+v err=%v", report, err)
	}
	candidate, err := os.ReadFile(filepath.Join(emptyRepo, filepath.FromSlash(report.Candidates[0])))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(candidate), agentBlockStart) != 1 || strings.Count(string(candidate), agentBlockEnd) != 1 {
		t.Fatalf("managed document candidate does not contain exactly one managed block:\n%s", candidate)
	}

	duplicateRepo := t.TempDir()
	duplicate := managedBlock(agentBlockStart, agentBlockEnd, renderAgentBlock()) + managedBlock(agentBlockStart, agentBlockEnd, renderAgentBlock())
	writeBootstrapTestFile(t, duplicateRepo, "AGENTS.md", duplicate, 0o644)
	if _, err := BuildPlan(Request{RepoRoot: duplicateRepo, Profile: ProfileMinimal}, "test-version"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate managed markers must fail closed: %v", err)
	}
}

func TestApplyRejectsStalePlanBeforeWriting(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	writeBootstrapTestFile(t, repo, "AGENTS.md", "arrived after plan\n", 0o644)
	before := bootstrapTreeSnapshot(t, repo)
	if _, err := Apply(plan, "test-version"); err == nil || !strings.Contains(err.Error(), "plan is stale") {
		t.Fatalf("stale plan must fail before writes: %v", err)
	}
	after := bootstrapTreeSnapshot(t, repo)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("stale-plan failure mutated repository: before=%v after=%v", before, after)
	}
}

func TestInjectedFailureRollsBackExactCreatedState(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileGoverned}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := apply(plan, "test-version", applyOptions{failAfter: 3})
	if err == nil || report.Status != ApplyRolledBack || len(report.RolledBack) != 3 {
		t.Fatalf("expected injected rollback: report=%+v err=%v", report, err)
	}
	if remaining := bootstrapTreeSnapshot(t, repo); len(remaining) != 0 {
		t.Fatalf("rollback did not restore empty repository: %v", remaining)
	}
}

func TestBinaryArtifactIsChecksumPinnedAndStableNamed(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	source := filepath.Join(t.TempDir(), "reconc-release")
	if err := os.WriteFile(source, []byte("binary-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection, err := BinarySelectionFor(source, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal, Binary: selection}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil || report.Status != ApplyComplete {
		t.Fatalf("binary apply: report=%+v err=%v", report, err)
	}
	name, err := StableBinaryName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "tools", "reconc", "dist", name)
	digest, err := fileSHA256(target)
	if err != nil || digest != selection.SHA256 {
		t.Fatalf("installed binary digest=%s want=%s err=%v", digest, selection.SHA256, err)
	}
	resolution := ResolveRepoBinary(repo, runtime.GOOS, runtime.GOARCH)
	if resolution.Path != target || resolution.Source != "tools-reconc-dist" {
		t.Fatalf("stable binary did not resolve first: %+v", resolution)
	}
}

func TestBinaryResolutionFailsClosedOnVersionAmbiguity(t *testing.T) {
	repo := t.TempDir()
	directory := filepath.Join(repo, "tools", "reconc", "dist")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"0.5.0", "0.6.0"} {
		name := "reconc-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		writeBootstrapTestFile(t, directory, name, version, 0o755)
	}
	resolution := ResolveRepoBinary(repo, runtime.GOOS, runtime.GOARCH)
	if resolution.Path != "" || !strings.Contains(resolution.Diagnostic, "ambiguous") || len(resolution.Candidates) != 2 {
		t.Fatalf("ambiguous versioned artifacts must fail closed: %+v", resolution)
	}
}

func TestBinaryResolutionRejectsRepoLocalSymlink(t *testing.T) {
	repo := t.TempDir()
	directory := filepath.Join(repo, "tools", "reconc", "dist")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "reconc")
	if err := os.WriteFile(external, []byte("external"), 0o755); err != nil {
		t.Fatal(err)
	}
	name, err := StableBinaryName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(directory, name)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	resolution := ResolveRepoBinary(repo, runtime.GOOS, runtime.GOARCH)
	if resolution.Path != "" {
		t.Fatalf("repo-local symlink must not resolve as an owned binary: %+v", resolution)
	}
}

func TestSymlinkTargetCannotBypassCandidateIsolation(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil || report.Status != ApplyDrift {
		t.Fatalf("symlink drift apply: report=%+v err=%v", report, err)
	}
	outsideBody, err := os.ReadFile(outside)
	if err != nil || string(outsideBody) != "outside\n" {
		t.Fatalf("outside symlink target changed: err=%v body=%q", err, outsideBody)
	}
	if _, err := os.Stat(filepath.Join(repo, ".reconc.yml")); !os.IsNotExist(err) {
		t.Fatalf("symlink conflict should stop target installation: %v", err)
	}
}

func TestStackSpecificPackRequiresDetectedApplicability(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	_, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal, Packs: []string{"go-assurance"}}, "test-version")
	if err == nil || !strings.Contains(err.Error(), "not applicable") {
		t.Fatalf("stack-specific pack without stack evidence must fail: %v", err)
	}
	writeBootstrapTestFile(t, repo, "go.mod", "module example\n", 0o644)
	if _, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal, Packs: []string{"go-assurance"}}, "test-version"); err != nil {
		t.Fatalf("go pack should apply after go.mod exists: %v", err)
	}
}

func TestFrameworkAndAdditionalLanguagePacksRequireDetectedApplicability(t *testing.T) {
	bootstrapTestHome(t)
	tests := []struct {
		name string
		pack string
		path string
		body string
	}{
		{name: "Next.js", pack: "nextjs-assurance", path: "package.json", body: "{\"dependencies\":{\"next\":\"16.0.0\"}}\n"},
		{name: "Svelte", pack: "svelte-assurance", path: "package.json", body: "{\"devDependencies\":{\"svelte\":\"5.0.0\"}}\n"},
		{name: "Zig", pack: "zig-assurance", path: "build.zig", body: "const std = @import(\"std\");\n"},
		{name: "Elixir", pack: "elixir-assurance", path: "mix.exs", body: "defmodule Demo.MixProject do\nend\n"},
		{name: "PowerShell", pack: "powershell-assurance", path: "scripts/check.ps1", body: "exit 0\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			request := Request{RepoRoot: repo, Profile: ProfileMinimal, Packs: []string{test.pack}}
			if _, err := BuildPlan(request, "test-version"); err == nil || !strings.Contains(err.Error(), "not applicable") {
				t.Fatalf("%s without stack evidence must fail: %v", test.pack, err)
			}
			writeBootstrapTestFile(t, repo, test.path, test.body, 0o644)
			if _, err := BuildPlan(request, "test-version"); err != nil {
				t.Fatalf("%s should apply after stack evidence exists: %v", test.pack, err)
			}
		})
	}
}

func TestNodeAndTypeScriptPacksRequireDetectedApplicability(t *testing.T) {
	bootstrapTestHome(t)
	tests := []struct {
		name string
		pack string
		path string
		body string
	}{
		{name: "npm", pack: "npm-assurance", path: "package-lock.json", body: "{}\n"},
		{name: "pnpm", pack: "pnpm-assurance", path: "pnpm-lock.yaml", body: "lockfileVersion: '9.0'\n"},
		{name: "Yarn", pack: "yarn-assurance", path: "yarn.lock", body: "# yarn lockfile\n"},
		{name: "TypeScript", pack: "typescript-assurance", path: "tsconfig.json", body: "{}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			request := Request{RepoRoot: repo, Profile: ProfileMinimal, Packs: []string{test.pack}}
			if _, err := BuildPlan(request, "test-version"); err == nil || !strings.Contains(err.Error(), "not applicable") {
				t.Fatalf("%s without stack evidence must fail: %v", test.pack, err)
			}
			if test.pack != "typescript-assurance" {
				writeBootstrapTestFile(t, repo, "package.json", "{}\n", 0o644)
			}
			writeBootstrapTestFile(t, repo, test.path, test.body, 0o644)
			if _, err := BuildPlan(request, "test-version"); err != nil {
				t.Fatalf("%s should apply after stack evidence exists: %v", test.pack, err)
			}
		})
	}
}

func TestRollbackRefusesExternallyReplacedDirectory(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "owned")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	directory, err := captureCreatedDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove captured directory: %v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = rollbackCreated(repo, nil, []createdDirectory{directory})
	if err == nil || !strings.Contains(err.Error(), "externally replaced directory") {
		t.Fatalf("rollback must refuse a replacement directory: %v", err)
	}
	if current, statErr := os.Stat(path); statErr != nil || !current.IsDir() {
		t.Fatalf("replacement directory was removed: info=%v err=%v", current, statErr)
	}
}

func writeBootstrapTestFile(t *testing.T, root, relative, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func bootstrapTreeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	entries := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		kind := fileKind(info)
		digest := ""
		if kind == "file" {
			digest, err = fileSHA256(path)
			if err != nil {
				return err
			}
		}
		entries = append(entries, filepath.ToSlash(relative)+"|"+kind+"|"+digest)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	return entries
}
