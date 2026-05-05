package compiler

import (
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

// withRECONCHome isolates RECONC_HOME so user-level state doesn't leak.
func withRECONCHome(t *testing.T) {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func TestCompileSimpleRepo(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, "policies/rules.yml",
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m1\n")

	compiled, err := CompileRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if compiled.RuleCount != 1 {
		t.Errorf("expected 1 rule, got %d", compiled.RuleCount)
	}
	if compiled.LockfilePath != ".reconc/policy.lock.json" {
		t.Errorf("expected lockfile path .reconc/policy.lock.json, got %s", compiled.LockfilePath)
	}

	// Lockfile actually written?
	full := filepath.Join(repo, ".reconc", "policy.lock.json")
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("lockfile is not valid JSON: %v", err)
	}
	if payload["$schema"] != LockfileSchema() {
		t.Errorf("expected $schema %q, got %v", LockfileSchema(), payload["$schema"])
	}
	if payload["format_version"] != LockfileFormatVersion {
		t.Errorf("expected format_version %q, got %v", LockfileFormatVersion, payload["format_version"])
	}
	if payload["compiler_version"] != "0.1.0-test" {
		t.Errorf("expected compiler_version 0.1.0-test, got %v", payload["compiler_version"])
	}
	if payload["rule_count"].(float64) != 1 {
		t.Errorf("expected rule_count 1, got %v", payload["rule_count"])
	}
	digest, ok := payload["source_digest"].(string)
	if !ok || len(digest) != 64 {
		t.Errorf("expected 64-char SHA-256 source_digest, got %v", payload["source_digest"])
	}
}

func TestCompileLockfileIsByteStable(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, "policies/rules.yml",
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m1\n")

	c1, err := CompileRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatalf("compile 1: %v", err)
	}
	bytes1, err := os.ReadFile(filepath.Join(repo, ".reconc", "policy.lock.json"))
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}

	// Recompile - should produce identical bytes.
	c2, err := CompileRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatalf("compile 2: %v", err)
	}
	bytes2, err := os.ReadFile(filepath.Join(repo, ".reconc", "policy.lock.json"))
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if string(bytes1) != string(bytes2) {
		t.Error("lockfile bytes differ between two compiles of identical sources")
	}
	if c1.SourceDigest != c2.SourceDigest {
		t.Errorf("source_digest differs: %s vs %s", c1.SourceDigest, c2.SourceDigest)
	}
}

func TestCompileCreatesReconcDirectory(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")

	if _, err := CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	info, err := os.Stat(filepath.Join(repo, ".reconc"))
	if err != nil {
		t.Fatalf("expected .reconc/ to exist after compile: %v", err)
	}
	if !info.IsDir() {
		t.Error(".reconc must be a directory")
	}
}

func TestCompileWithExtendsBundlesPresetRules(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, ".reconc.yml", "extends:\n  - default\n")

	compiled, err := CompileRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Default preset bundles 2 rules.
	if compiled.RuleCount != 2 {
		t.Errorf("expected 2 rules from default preset, got %d", compiled.RuleCount)
	}
}

func TestCompileFailsOnInvalidRule(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, "policies/bad.yml",
		"rules:\n  - id: x\n    kind: explode\n    paths: ['x']\n    mode: warn\n    message: x\n")

	_, err := CompileRepoPolicy(repo, "0.1.0-test")
	if err == nil {
		t.Fatal("expected error for invalid rule kind")
	}
	var rve *rerrors.RuleValidationError
	if !stderrors.As(err, &rve) {
		t.Errorf("expected *RuleValidationError, got %T", err)
	}
}

func TestCompileFailsOnMissingRepo(t *testing.T) {
	withRECONCHome(t)
	_, err := CompileRepoPolicy("/no/such/path/for/reconc/compile", "0.1.0-test")
	if err == nil {
		t.Fatal("expected error for missing repo path")
	}
}

func TestCompileFailsOnUndiscoveredRepo(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	// no markers
	_, err := CompileRepoPolicy(repo, "0.1.0-test")
	if err == nil {
		t.Fatal("expected error for repo without markers")
	}
	var pse *rerrors.PolicySourceError
	if !stderrors.As(err, &pse) {
		t.Errorf("expected *PolicySourceError, got %T", err)
	}
}

func TestCompileLockfileHasReconcSchema(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")

	if _, err := CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".reconc", "policy.lock.json"))
	if !strings.Contains(string(data), "reconc.dev/schemas/policy-lock/v1") {
		t.Errorf("lockfile must reference reconc.dev schema URL, got: %s", string(data))
	}
}

func TestCompileSuppressesLockfileMissingWarningAfterRun(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")

	compiled, err := CompileRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, w := range compiled.Warnings {
		if strings.Contains(w, "lockfile not found") {
			t.Errorf("post-compile warnings should not include lockfile-missing, got: %v", compiled.Warnings)
		}
	}
}

func TestCompileSourceDigestChangesWithSources(t *testing.T) {
	withRECONCHome(t)

	repo1 := t.TempDir()
	writeFile(t, repo1, "AGENTS.md", "# project\n")
	c1, err := CompileRepoPolicy(repo1, "0.1.0-test")
	if err != nil {
		t.Fatalf("compile 1: %v", err)
	}

	repo2 := t.TempDir()
	writeFile(t, repo2, "AGENTS.md", "# project\n")
	writeFile(t, repo2, "policies/extra.yml",
		"rules:\n  - id: extra\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n")
	c2, err := CompileRepoPolicy(repo2, "0.1.0-test")
	if err != nil {
		t.Fatalf("compile 2: %v", err)
	}

	if c1.SourceDigest == c2.SourceDigest {
		t.Error("digest should differ when source set differs")
	}
}

func TestCompileSourcePathsCapturesAllSources(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, ".reconc.yml", "extends:\n  - default\n")
	writeFile(t, repo, "policies/p1.yml", "rules: []\n")

	compiled, err := CompileRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if compiled.SourceCount < 4 {
		t.Errorf("expected at least 4 sources (agents_md + compiler_config + preset + policy_file), got %d (paths: %v)", compiled.SourceCount, compiled.SourcePaths)
	}
}

// --- W24: custom schema URL -----------------------------------------

func TestLockfileSchemaDefault(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	got := LockfileSchema()
	if got != DefaultLockfileSchema {
		t.Errorf("expected default schema, got %s", got)
	}
}

func TestLockfileSchemaHonorsEnvOverride(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://reconc.acme.com")
	got := LockfileSchema()
	want := "https://reconc.acme.com/schemas/policy-lock/v1"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestLockfileSchemaStripsTrailingSlash(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://acme.com/")
	got := LockfileSchema()
	want := "https://acme.com/schemas/policy-lock/v1"
	if got != want {
		t.Errorf("trailing slash should be stripped; got %q", got)
	}
}

func TestCompileWritesCustomSchemaURL(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://internal.corp")
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# t\n")
	writeFile(t, repo, "policies/rules.yml", "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: m\n")
	if _, err := CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, LockfileRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	want := "https://internal.corp/schemas/policy-lock/v1"
	if payload["$schema"] != want {
		t.Errorf("expected $schema %q, got %v", want, payload["$schema"])
	}
}

func TestComputeSourceDigestStableAndSensitive(t *testing.T) {
	base := &ingest.SourceBundle{
		Sources: []policy.PolicySource{
			{Kind: policy.SourcePolicyFile, Path: "policies/a.yml", Content: "rules: []\n"},
		},
	}
	if got := ComputeSourceDigest(base); got == "" {
		t.Fatal("expected non-empty digest")
	}
	if got1, got2 := ComputeSourceDigest(base), ComputeSourceDigest(base); got1 != got2 {
		t.Fatalf("digest must be stable for identical bundle: %s vs %s", got1, got2)
	}

	changed := &ingest.SourceBundle{
		Sources: []policy.PolicySource{
			{Kind: policy.SourcePolicyFile, Path: "policies/a.yml", Content: "rules:\n  - id: x\n"},
		},
	}
	if ComputeSourceDigest(base) == ComputeSourceDigest(changed) {
		t.Fatal("digest must change when source content changes")
	}
}

func TestCheckToMapCoversSpecializedFields(t *testing.T) {
	cases := []struct {
		name string
		in   policy.Check
		want map[string]interface{}
	}{
		{
			name: "fresh-file",
			in: policy.Check{
				Kind:        policy.KindRequireFreshFile,
				Path:        "docs/report.md",
				MaxAgeHours: 24,
				Optional:    true,
			},
			want: map[string]interface{}{"kind": "require_fresh_file", "path": "docs/report.md", "max_age_hours": 24, "optional": true},
		},
		{
			name: "evidence",
			in: policy.Check{
				Kind:           policy.KindRequireEvidence,
				File:           "docs/coverage.md",
				MustExist:      true,
				MustContain:    []string{"pass"},
				MustNotContain: "fail",
				MaxLineCount:   12,
			},
			want: map[string]interface{}{"kind": "require_evidence", "file": "docs/coverage.md", "must_exist": true, "must_contain": []string{"pass"}, "must_not_contain": "fail", "max_line_count": 12},
		},
		{
			name: "script",
			in: policy.Check{
				Kind:       policy.KindRequireScript,
				Script:     "scripts/check.sh",
				Args:       []string{"--fast"},
				TimeoutSec: 30,
			},
			want: map[string]interface{}{"kind": "require_script", "script": "scripts/check.sh", "args": []string{"--fast"}, "timeout_sec": 30},
		},
		{
			name: "claims-and-commands",
			in: policy.Check{
				Kind:     policy.KindRequireClaim,
				Claims:   []string{"ci-green"},
				Commands: []string{"go test ./..."},
				Paths:    []string{"src/**"},
			},
			want: map[string]interface{}{"kind": "require_claim", "claims": []string{"ci-green"}, "commands": []string{"go test ./..."}, "paths": []string{"src/**"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkToMap(tc.in)
			for key, wantValue := range tc.want {
				gotValue, ok := got[key]
				if !ok {
					t.Fatalf("missing key %q in map: %#v", key, got)
				}
				if gotJSON, err := json.Marshal(gotValue); err != nil {
					t.Fatalf("marshal got value: %v", err)
				} else if wantJSON, err := json.Marshal(wantValue); err != nil {
					t.Fatalf("marshal want value: %v", err)
				} else if string(gotJSON) != string(wantJSON) {
					t.Fatalf("key %q mismatch: got %s want %s", key, gotJSON, wantJSON)
				}
			}
		})
	}
}

func TestRuleToMapCoversOptionalFields(t *testing.T) {
	rule := policy.Rule{
		ID:      "all-fields",
		Kind:    policy.KindRequireScript,
		Message: "run the repo-local verifier",
		Mode:    policy.ModeBlock,
		Paths:   []string{"src/**"},
		BeforePaths: []string{
			"docs/**",
		},
		WhenPaths: []string{"scripts/**"},
		Commands:  []string{"go test ./..."},
		Claims:    []string{"ci-green"},
		RequiredFiles: []policy.RequiredFile{
			{Path: "docs/report.md", MaxAgeHours: 24, Optional: true},
		},
		Evidence: []policy.EvidenceCheck{
			{
				File:           "docs/evidence.md",
				MustExist:      true,
				MustContain:    []string{"PASS"},
				MustNotContain: "FAIL",
				MaxLineCount:   12,
				Optional:       true,
			},
		},
		Checks: []policy.Check{
			{
				Kind:        policy.KindRequireFreshFile,
				Path:        "docs/report.md",
				MaxAgeHours: 24,
				Optional:    true,
			},
		},
		Script:               "scripts/check.sh",
		Args:                 []string{"--fast"},
		TimeoutSec:           30,
		KillTimeoutSec:       5,
		SourcePath:           "policies/rules.yml",
		SourceBlockID:        "AGENTS.md:12",
		Deprecated:           true,
		DeprecatedReason:     "superseded",
		DeprecatedSince:      "2026-04-01",
		DeprecatedReplacedBy: "new-rule",
		ScopePaths:           []string{"apps/web/**"},
		ScopeID:              "web",
	}

	got := ruleToMap(rule)
	wantKeys := []string{
		"id",
		"kind",
		"message",
		"mode",
		"paths",
		"before_paths",
		"when_paths",
		"commands",
		"claims",
		"required_files",
		"evidence",
		"checks",
		"script",
		"args",
		"timeout_sec",
		"kill_timeout_sec",
		"source_path",
		"source_block_id",
		"deprecated",
		"deprecated_reason",
		"deprecated_since",
		"deprecated_replaced_by",
		"scope_paths",
		"scope_id",
	}
	for _, key := range wantKeys {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing key %q in %#v", key, got)
		}
	}

	requiredFiles, ok := got["required_files"].([]interface{})
	if !ok || len(requiredFiles) != 1 {
		t.Fatalf("required_files wrong shape: %#v", got["required_files"])
	}
	requiredFile, ok := requiredFiles[0].(map[string]interface{})
	if !ok {
		t.Fatalf("required_files[0] wrong type: %#v", requiredFiles[0])
	}
	if requiredFile["path"] != "docs/report.md" || requiredFile["max_age_hours"] != 24 || requiredFile["optional"] != true {
		t.Fatalf("required_files[0] wrong content: %#v", requiredFile)
	}

	evidence, ok := got["evidence"].([]interface{})
	if !ok || len(evidence) != 1 {
		t.Fatalf("evidence wrong shape: %#v", got["evidence"])
	}
	evidenceItem, ok := evidence[0].(map[string]interface{})
	if !ok {
		t.Fatalf("evidence[0] wrong type: %#v", evidence[0])
	}
	if evidenceItem["file"] != "docs/evidence.md" || evidenceItem["must_exist"] != true || evidenceItem["must_not_contain"] != "FAIL" || evidenceItem["max_line_count"] != 12 || evidenceItem["optional"] != true {
		t.Fatalf("evidence[0] wrong content: %#v", evidenceItem)
	}

	checks, ok := got["checks"].([]interface{})
	if !ok || len(checks) != 1 {
		t.Fatalf("checks wrong shape: %#v", got["checks"])
	}
	checkItem, ok := checks[0].(map[string]interface{})
	if !ok {
		t.Fatalf("checks[0] wrong type: %#v", checks[0])
	}
	if checkItem["kind"] != "require_fresh_file" || checkItem["path"] != "docs/report.md" || checkItem["max_age_hours"] != 24 || checkItem["optional"] != true {
		t.Fatalf("checks[0] wrong content: %#v", checkItem)
	}
}
