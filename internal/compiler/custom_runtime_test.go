package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/runtime"
)

func TestCustomRuntimeIsValidatedCompiledAndBoundToFreshness(t *testing.T) {
	t.Parallel()
	repo := customRuntimeRepo(t, "local-agent")
	compiled, err := compiler.CompileRepoPolicy(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.CustomRuntimes) != 1 || compiled.CustomRuntimes[0].Runtime != "custom:local-agent" {
		t.Fatalf("unexpected custom runtime summary: %+v", compiled.CustomRuntimes)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".reconc", "policy.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]interface{}
	if err := json.Unmarshal(body, &lock); err != nil {
		t.Fatal(err)
	}
	if _, ok := lock["custom_runtimes"]; !ok {
		t.Fatal("compiled lock omits custom_runtimes")
	}
	recomputed, err := compiler.ComputeLockDigest(lock)
	if err != nil {
		t.Fatal(err)
	}
	if recomputed != lock["lock_digest"] {
		t.Fatalf("lock digest mismatch: stored=%v recomputed=%v", lock["lock_digest"], recomputed)
	}
	compiledEvaluator, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatalf("fresh custom runtime lock rejected: %v", err)
	}
	if digest, ok := compiledEvaluator.CustomRuntimeManifestDigest("custom:local-agent"); !ok || digest != compiled.CustomRuntimes[0].ManifestDigest {
		t.Fatalf("compiled runtime digest = %q, %v", digest, ok)
	}
	digests, err := runtime.LoadFreshCustomRuntimeManifestDigests(repo)
	if err != nil {
		t.Fatalf("load minimal runtime digests: %v", err)
	}
	if digest := digests["custom:local-agent"]; digest != compiled.CustomRuntimes[0].ManifestDigest {
		t.Fatalf("minimal runtime digest = %q, want %q", digest, compiled.CustomRuntimes[0].ManifestDigest)
	}
	manifestPath := filepath.Join(repo, ".reconc", "runtimes", "local-agent.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = []byte(strings.Replace(string(manifest), "Generic Local Agent", "Changed Local Agent", 1))
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repo); err == nil || !strings.Contains(err.Error(), "source_digest") {
		t.Fatalf("stale custom runtime did not fail closed: %v", err)
	}
	if _, err := runtime.LoadFreshCustomRuntimeManifestDigests(repo); err == nil || !strings.Contains(err.Error(), "source_digest") {
		t.Fatalf("minimal runtime digest lookup accepted stale manifest: %v", err)
	}
}

func TestCustomRuntimeFilenameAndMCPReferenceMustMatchCompiledIdentity(t *testing.T) {
	t.Parallel()
	repo := customRuntimeRepo(t, "wrong-name")
	if _, _, err := compiler.RenderRepoPolicy(repo, "test"); err == nil || !strings.Contains(err.Error(), "must be named local-agent.json") {
		t.Fatalf("filename mismatch error = %v", err)
	}

	repo = customRuntimeRepo(t, "local-agent")
	config := "default_mode: warn\nmcp:\n  unclassified: host\n  tools:\n    - platform: custom:missing\n      tool: read\n      effect: external\n"
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := compiler.RenderRepoPolicy(repo, "test"); err == nil || !strings.Contains(err.Error(), "unconfigured custom runtime") {
		t.Fatalf("unconfigured MCP runtime error = %v", err)
	}
}

func TestRuntimeRejectsRehashedCustomRuntimeSummaryTampering(t *testing.T) {
	t.Parallel()
	repo := customRuntimeRepo(t, "local-agent")
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".reconc", "policy.lock.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]interface{}
	if err := json.Unmarshal(body, &lock); err != nil {
		t.Fatal(err)
	}
	delete(lock, "custom_runtimes")
	digest, err := compiler.ComputeLockDigest(lock)
	if err != nil {
		t.Fatal(err)
	}
	lock["lock_digest"] = digest
	body, err = json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repo); err == nil || !strings.Contains(err.Error(), "summaries do not match") {
		t.Fatalf("rehashed custom runtime summary tampering error = %v", err)
	}
	if _, err := runtime.LoadFreshCustomRuntimeManifestDigests(repo); err == nil || !strings.Contains(err.Error(), "summaries do not match") {
		t.Fatalf("minimal runtime digest lookup accepted summary tampering: %v", err)
	}
}

func customRuntimeRepo(t *testing.T, filename string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("default_mode: warn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(repo, ".reconc", "runtimes")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("..", "customruntime", "testdata", "local-agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, filename+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}
