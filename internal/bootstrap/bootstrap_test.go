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
	if strings.Join(inspection.DetectedStacks, ",") != "bun,go" {
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

func TestManagedDocumentCandidatesKeepOneValidTitleAndRejectDuplicateMarkers(t *testing.T) {
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
	if !strings.HasPrefix(string(candidate), "# Repository instructions\n\n") || strings.Count(string(candidate), "# Repository instructions") != 1 {
		t.Fatalf("managed document candidate has invalid title structure:\n%s", candidate)
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
	t.Cleanup(func() { _ = directory.handle.Close() })
	if err := os.Remove(path); err != nil {
		t.Skipf("filesystem does not permit replacing an open directory: %v", err)
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
