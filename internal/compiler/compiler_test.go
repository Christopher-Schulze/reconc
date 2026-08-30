package compiler

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
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
	if payload["rule_count"].(json.Number) != "1" {
		t.Errorf("expected rule_count 1, got %v", payload["rule_count"])
	}
	encoded := string(data)
	if strings.Contains(encoded, "# project") || strings.Contains(encoded, "kind: deny_write") || strings.Contains(encoded, `"content"`) {
		t.Fatalf("lockfile exposed raw source content:\n%s", encoded)
	}
	sources, ok := payload["sources"].([]interface{})
	if !ok || len(sources) == 0 {
		t.Fatalf("lockfile sources have unexpected shape: %#v", payload["sources"])
	}
	for index, item := range sources {
		source, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("sources[%d] has type %T", index, item)
		}
		if digest, _ := source["content_sha256"].(string); len(digest) != 64 {
			t.Fatalf("sources[%d].content_sha256 = %v", index, source["content_sha256"])
		}
	}
	digest, ok := payload["source_digest"].(string)
	if !ok || len(digest) != 64 {
		t.Errorf("expected 64-char SHA-256 source_digest, got %v", payload["source_digest"])
	}
	lockDigest, ok := payload["lock_digest"].(string)
	if !ok || len(lockDigest) != 64 {
		t.Errorf("expected 64-char SHA-256 lock_digest, got %v", payload["lock_digest"])
	}
	computedLockDigest, err := ComputeLockDigest(payload)
	if err != nil {
		t.Fatalf("compute lock digest: %v", err)
	}
	if lockDigest != computedLockDigest {
		t.Fatalf("stored lock digest %s does not bind emitted payload %s", lockDigest, computedLockDigest)
	}
}

func TestRenderRepoPolicyReturnsExactBytesWithoutWriting(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, "policies/rules.yml",
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m1\n")

	rendered, body, err := RenderRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if rendered.RepoRoot != repo || rendered.RuleCount != 1 || len(body) == 0 {
		t.Fatalf("rendered policy = %+v bytes=%d", rendered, len(body))
	}
	lockPath := filepath.Join(repo, ".reconc", "policy.lock.json")
	if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("in-memory render wrote %s: %v", lockPath, err)
	}

	if _, err := CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	published, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read published lockfile: %v", err)
	}
	if !bytes.Equal(body, published) {
		t.Fatal("in-memory render bytes differ from published compiler bytes")
	}
}

func TestEncodeLockfileEnforcesTheSharedReadBoundary(t *testing.T) {
	empty, err := json.MarshalIndent(map[string]interface{}{"filler": ""}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	overhead := len(empty) + 1
	for _, test := range []struct {
		name    string
		offset  int
		wantErr bool
	}{
		{name: "minus one", offset: -1},
		{name: "exact", offset: 0},
		{name: "plus one", offset: 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := int(MaxLockfileBytes) + test.offset
			body, err := encodeLockfile(map[string]interface{}{"filler": strings.Repeat("x", target-overhead)})
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "exceeds 16777216 bytes") {
					t.Fatalf("encode error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(body) != target {
				t.Fatalf("encoded bytes = %d, want %d", len(body), target)
			}
		})
	}
}

func TestEncodeCanonicalLockfileEnforcesTheSharedReadBoundary(t *testing.T) {
	digest := strings.Repeat("a", 64)
	empty, err := encodeCanonicalLockfile([]byte(`{"filler":""}`), digest)
	if err != nil {
		t.Fatal(err)
	}
	overhead := len(empty)
	for _, test := range []struct {
		name    string
		offset  int
		wantErr bool
	}{
		{name: "minus one", offset: -1},
		{name: "exact", offset: 0},
		{name: "plus one", offset: 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := int(MaxLockfileBytes) + test.offset
			canonical := []byte(`{"filler":"` + strings.Repeat("x", target-overhead) + `"}`)
			body, err := encodeCanonicalLockfile(canonical, digest)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "exceeds 16777216 bytes") {
					t.Fatalf("encode error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(body) != target {
				t.Fatalf("encoded bytes = %d, want %d", len(body), target)
			}
		})
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

func TestCompileLockfileSkipsUnchangedPublication(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	if _, err := CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatalf("compile 1: %v", err)
	}
	path := filepath.Join(repo, LockfileRelativePath)
	fixed := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, fixed, fixed); err != nil {
		t.Fatalf("set lockfile timestamp: %v", err)
	}
	if _, err := CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatalf("compile 2: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lockfile: %v", err)
	}
	if !info.ModTime().Equal(fixed) {
		t.Fatalf("unchanged compile republished lockfile: mtime=%s want=%s", info.ModTime(), fixed)
	}
}

func TestCompileLockfileIsPortableAcrossRepoRoots(t *testing.T) {
	withRECONCHome(t)
	createRepo := func() string {
		repo := t.TempDir()
		writeFile(t, repo, "AGENTS.md", "# project\n")
		writeFile(t, repo, ".reconc.yml", "default_mode: warn\n")
		writeFile(t, repo, "policies/rules.yml",
			"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m1\n")
		return repo
	}

	repoA := createRepo()
	repoB := createRepo()
	if _, err := CompileRepoPolicy(repoA, "0.1.0-test"); err != nil {
		t.Fatalf("compile repo A: %v", err)
	}
	if _, err := CompileRepoPolicy(repoB, "0.1.0-test"); err != nil {
		t.Fatalf("compile repo B: %v", err)
	}
	lockA, err := os.ReadFile(filepath.Join(repoA, LockfileRelativePath))
	if err != nil {
		t.Fatalf("read repo A lockfile: %v", err)
	}
	lockB, err := os.ReadFile(filepath.Join(repoB, LockfileRelativePath))
	if err != nil {
		t.Fatalf("read repo B lockfile: %v", err)
	}
	if !bytes.Equal(lockA, lockB) {
		t.Fatal("identical sources at different roots must produce byte-identical lockfiles")
	}
	if bytes.Contains(lockA, []byte(repoA)) || bytes.Contains(lockA, []byte(repoB)) {
		t.Fatal("portable lockfile must not contain a physical checkout root")
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(lockA, &payload); err != nil {
		t.Fatalf("decode portable lockfile: %v", err)
	}
	if payload["repo_root"] != PortableRepoRoot {
		t.Fatalf("repo_root=%v want %q", payload["repo_root"], PortableRepoRoot)
	}
	discovery, ok := payload["discovery"].(map[string]interface{})
	if !ok {
		t.Fatalf("discovery has unexpected type %T", payload["discovery"])
	}
	for _, field := range []string{"repo_root", "start_path"} {
		if discovery[field] != PortableRepoRoot {
			t.Fatalf("discovery.%s=%v want %q", field, discovery[field], PortableRepoRoot)
		}
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
	// The stack-neutral default owns only generated-output protection.
	if compiled.RuleCount != 1 {
		t.Errorf("expected 1 rule from default preset, got %d", compiled.RuleCount)
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
	if !strings.Contains(string(data), DefaultLockfileSchema) {
		t.Errorf("lockfile must reference the published schema URL, got: %s", string(data))
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

func TestRenderPreservesWarningContainingLockfileMissingText(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	bundle, err := ingest.LoadPolicySources(repo)
	if err != nil {
		t.Fatal(err)
	}
	customWarning := "custom warning: lockfile not found is quoted documentation"
	bundle.Discovery.Warnings = append(bundle.Discovery.Warnings, customWarning)
	originalWarnings := append([]string(nil), bundle.Discovery.Warnings...)

	compiled, _, err := renderPolicyBundle(bundle, "0.1.0-test")
	if err != nil {
		t.Fatal(err)
	}
	if compiled.LockfilePath != ingest.LockfilePath || compiled.Discovery.LockfilePath == nil || *compiled.Discovery.LockfilePath != ingest.LockfilePath {
		t.Fatalf("compiled lock paths disagree: %+v", compiled)
	}
	if !containsExactString(compiled.Warnings, customWarning) {
		t.Fatalf("custom warning was removed: %v", compiled.Warnings)
	}
	if containsExactString(compiled.Warnings, "compiled lockfile not found at "+ingest.LockfilePath) {
		t.Fatalf("owned missing warning survived: %v", compiled.Warnings)
	}
	if !reflect.DeepEqual(bundle.Discovery.Warnings, originalWarnings) || bundle.Discovery.LockfilePath != nil {
		t.Fatalf("render mutated bundle discovery: %+v", bundle.Discovery)
	}
}

func containsExactString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
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

func TestCompileGlobalPolicyProvenanceIsPortableAndPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	secretMarker := "private-global-source-comment"
	writeFile(t, home, "global-policy.yml", "# "+secretMarker+"\nrules:\n  - id: global\n    kind: deny_write\n    paths: ['private/**']\n    mode: warn\n    message: global rule\n")
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")

	if _, err := CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, LockfileRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(home)) || bytes.Contains(data, []byte(secretMarker)) || bytes.Contains(data, []byte(`"content"`)) {
		t.Fatalf("lockfile exposed global policy path or body:\n%s", data)
	}
	if !bytes.Contains(data, []byte(ingest.GlobalPolicySourcePath)) {
		t.Fatalf("lockfile missing logical global provenance:\n%s", data)
	}
}

func TestValidateLockfileEnvelopeRejectsRawOrUnsafeSourceProvenance(t *testing.T) {
	base := map[string]interface{}{
		"$schema":        LockfileSchema(),
		"format_version": LockfileFormatVersion,
		"repo_root":      PortableRepoRoot,
		"discovery": map[string]interface{}{
			"repo_root":  PortableRepoRoot,
			"start_path": PortableRepoRoot,
		},
		"sources": []interface{}{
			map[string]interface{}{
				"kind":           string(policy.SourcePolicyFile),
				"path":           "policies/rules.yml",
				"content_sha256": strings.Repeat("a", 64),
			},
		},
	}
	withDigest := func(payload map[string]interface{}) map[string]interface{} {
		digest, err := ComputeLockDigest(payload)
		if err != nil {
			t.Fatal(err)
		}
		payload["lock_digest"] = digest
		return payload
	}
	if err := ValidateLockfileEnvelope(withDigest(base)); err != nil {
		t.Fatalf("valid provenance rejected: %v", err)
	}
	for _, mutate := range []func(map[string]interface{}){
		func(payload map[string]interface{}) {
			payload["sources"].([]interface{})[0].(map[string]interface{})["content"] = "secret"
		},
		func(payload map[string]interface{}) {
			payload["sources"].([]interface{})[0].(map[string]interface{})["path"] = "../secret.yml"
		},
		func(payload map[string]interface{}) {
			payload["sources"].([]interface{})[0].(map[string]interface{})["content_sha256"] = "short"
		},
	} {
		body, err := json.Marshal(base)
		if err != nil {
			t.Fatal(err)
		}
		var candidate map[string]interface{}
		if err := json.Unmarshal(body, &candidate); err != nil {
			t.Fatal(err)
		}
		delete(candidate, "lock_digest")
		mutate(candidate)
		if err := ValidateLockfileEnvelope(withDigest(candidate)); err == nil {
			t.Fatalf("unsafe source provenance accepted: %#v", candidate["sources"])
		}
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
	want := "https://reconc.acme.com/schemas/policy-lock/v6"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestLockfileSchemaStripsTrailingSlash(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://acme.com/")
	got := LockfileSchema()
	want := "https://acme.com/schemas/policy-lock/v6"
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
	want := "https://internal.corp/schemas/policy-lock/v6"
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
	got, err := ComputeSourceDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("expected non-empty digest")
	}
	got1, err := ComputeSourceDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := ComputeSourceDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	if got1 != got2 {
		t.Fatalf("digest must be stable for identical bundle: %s vs %s", got1, got2)
	}

	changed := &ingest.SourceBundle{
		Sources: []policy.PolicySource{
			{Kind: policy.SourcePolicyFile, Path: "policies/a.yml", Content: "rules:\n  - id: x\n"},
		},
	}
	changedDigest, err := ComputeSourceDigest(changed)
	if err != nil {
		t.Fatal(err)
	}
	if got1 == changedDigest {
		t.Fatal("digest must change when source content changes")
	}

	ordered := &ingest.SourceBundle{Sources: []policy.PolicySource{
		{Kind: policy.SourceClaudeMD, Path: "CLAUDE.md", Content: "claude"},
		{Kind: policy.SourceAgentsMD, Path: "AGENTS.md", Content: "agents"},
	}}
	reordered := &ingest.SourceBundle{Sources: []policy.PolicySource{
		ordered.Sources[1], ordered.Sources[0],
	}}
	orderedDigest, err := ComputeSourceDigest(ordered)
	if err != nil {
		t.Fatal(err)
	}
	reorderedDigest, err := ComputeSourceDigest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if orderedDigest == reorderedDigest {
		t.Fatal("source digest did not bind the declared CLAUDE/AGENTS iteration order")
	}
}

func TestCompileSourceProvenanceFreezesRecordsOnce(t *testing.T) {
	bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{{
		Kind: policy.SourceAgentsMD, Path: "AGENTS.md", Content: "# agents\n",
	}}}
	provenance, err := compileSourceProvenance(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(provenance.records) != 1 || provenance.digest == "" {
		t.Fatalf("provenance = %#v", provenance)
	}
	originalRecord := provenance.records[0].(map[string]interface{})
	bundle.Sources[0].Content = "mutated after snapshot"
	if originalRecord["content_sha256"] == "" {
		t.Fatal("provenance record lost content digest")
	}
	updated, err := compileSourceProvenance(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if updated.digest == provenance.digest {
		t.Fatal("mutating source after snapshot must change a newly compiled digest")
	}
}

func TestNormalizeJSONValueWithBytesPreservesNumbersAndCanonicalBytes(t *testing.T) {
	value := map[string]interface{}{"b": json.Number("9007199254740993"), "a": "text"}
	normalized, rawBytes, err := normalizeJSONValueWithBytes(value)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := normalized.(map[string]interface{})["b"].(json.Number); !ok || got.String() != "9007199254740993" {
		t.Fatalf("normalized number = %#v", normalized)
	}
	encoded, err := encodeCanonicalJSON(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, rawBytes) {
		t.Fatalf("canonical bytes differ: got=%s want=%s", rawBytes, encoded)
	}
}

type normalizationMarshaler struct {
	body  string
	calls *int
}

func (m normalizationMarshaler) MarshalJSON() ([]byte, error) {
	(*m.calls)++
	return []byte(m.body), nil
}

func TestNormalizeLockPayloadCanonicalBytesFreezeCustomMarshalersOnce(t *testing.T) {
	calls := 0
	payload := map[string]interface{}{
		"z": json.Number("9007199254740993"),
		"custom": normalizationMarshaler{
			body: `{"z":2,"a":9007199254740993}`, calls: &calls,
		},
	}
	normalized, canonical, err := normalizeLockPayloadWithBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("custom marshaler calls = %d, want 1", calls)
	}
	if got, want := string(canonical), `{"custom":{"a":9007199254740993,"z":2},"z":9007199254740993}`; got != want {
		t.Fatalf("canonical lock payload = %s, want %s", got, want)
	}
	custom := normalized["custom"].(map[string]interface{})
	if number, ok := custom["a"].(json.Number); !ok || number.String() != "9007199254740993" {
		t.Fatalf("custom number = %#v", custom["a"])
	}
	wantDigest, err := ComputeLockDigest(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if got := digestCanonicalJSON(canonical); got != wantDigest {
		t.Fatalf("reused canonical digest = %s, reconstructed digest = %s", got, wantDigest)
	}
}

func TestCanonicalLockPayloadMatchesLegacyObjectKeyOrdering(t *testing.T) {
	for _, body := range []string{
		`{"z":1,"a":2}`,
		`{"a:b":1,"a\"":2,"a\\b":3}`,
		`{"é":1,"\u0080":2,"😀":3,"a":4}`,
	} {
		t.Run(body, func(t *testing.T) {
			payload := map[string]interface{}{
				"actions": normalizationMarshaler{body: body, calls: new(int)},
				"rules":   []interface{}{},
			}
			normalized, err := normalizeLockPayload(payload)
			if err != nil {
				t.Fatal(err)
			}
			want, err := encodeCanonicalJSON(normalized)
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := canonicalLockPayloadForEncoding(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("canonical payload ordering drifted:\ngot:  %s\nwant: %s", got, want)
			}
		})
	}
}

func TestEncodeCanonicalLockfileMatchesMapEncoding(t *testing.T) {
	calls := 0
	payload := map[string]interface{}{
		"format_version": LockfileFormatVersion,
		"nested": normalizationMarshaler{
			body:  `{"z":"<tag>&value","a":9007199254740993}`,
			calls: &calls,
		},
		"rule_count": json.Number("9007199254740993"),
		"rules":      []interface{}{},
	}
	normalized, canonical, err := normalizeLockPayloadWithBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("custom marshaler calls = %d, want 1", calls)
	}
	digest := digestCanonicalJSON(canonical)
	legacyPayload := make(map[string]interface{}, len(normalized)+1)
	for key, value := range normalized {
		legacyPayload[key] = value
	}
	legacyPayload["lock_digest"] = digest
	want, err := encodeLockfile(legacyPayload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := encodeCanonicalLockfile(canonical, digest)
	if err != nil {
		t.Fatal(err)
	}
	calls = 0
	productionCanonical, formattedSize, err := canonicalLockPayloadForEncoding(payload)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("production custom marshaler calls = %d, want 1", calls)
	}
	if !bytes.Equal(productionCanonical, canonical) {
		t.Fatalf("production canonical payload drifted:\ngot:  %s\nwant: %s", productionCanonical, canonical)
	}
	productionGot, err := encodeCanonicalLockfileWithSize(productionCanonical, digest, formattedSize)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(productionGot, want) {
		t.Fatalf("production lockfile encoding drifted:\ngot:  %s\nwant: %s", productionGot, want)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical lockfile encoding drifted:\ngot:  %s\nwant: %s", got, want)
	}
	if !bytes.HasSuffix(got, []byte("\n")) || bytes.HasSuffix(got, []byte("\n\n")) {
		t.Fatalf("lockfile newline contract drifted: %q", got[len(got)-2:])
	}
	if !bytes.Contains(got, []byte(`\u003ctag\u003e\u0026value`)) {
		t.Fatalf("lockfile HTML escaping drifted: %s", got)
	}
}

func TestInsertCanonicalStringField(t *testing.T) {
	for _, test := range []struct {
		name      string
		canonical string
		field     string
		want      string
		wantErr   bool
	}{
		{name: "empty", canonical: `{}`, field: "lock_digest", want: `{"lock_digest":"digest"}`},
		{name: "first", canonical: `{"z":2}`, field: "a", want: `{"a":"digest","z":2}`},
		{name: "middle", canonical: `{"a":1,"z":2}`, field: "m", want: `{"a":1,"m":"digest","z":2}`},
		{name: "last", canonical: `{"a":1}`, field: "z", want: `{"a":1,"z":"digest"}`},
		{name: "duplicate", canonical: `{"lock_digest":"old"}`, field: "lock_digest", wantErr: true},
		{name: "array", canonical: `[]`, field: "lock_digest", wantErr: true},
		{name: "truncated", canonical: `{"a":1`, field: "lock_digest", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := insertCanonicalStringField([]byte(test.canonical), test.field, "digest")
			if test.wantErr {
				if err == nil {
					t.Fatalf("insert accepted %q", test.canonical)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("inserted JSON = %s, want %s", got, test.want)
			}
		})
	}
}

func TestNormalizeLockPayloadRejectsCustomMarshalerTrailingData(t *testing.T) {
	calls := 0
	_, _, err := normalizeLockPayloadWithBytes(map[string]interface{}{
		"invalid": normalizationMarshaler{body: `{"valid":true} trailing`, calls: &calls},
	})
	if err == nil || calls != 1 {
		t.Fatalf("trailing custom JSON = calls %d, error %v", calls, err)
	}
}

func TestEncodeCanonicalJSONContract(t *testing.T) {
	t.Parallel()
	encoded, err := encodeCanonicalJSON(map[string]interface{}{"z": 2, "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"a":1,"z":2}`; got != want {
		t.Fatalf("canonical JSON = %q, want %q", got, want)
	}
	if _, err := encodeCanonicalJSON(map[string]interface{}{"unsupported": make(chan int)}); err == nil {
		t.Fatal("unsupported value did not propagate a JSON encoding error")
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

func TestCompileWarnsOnBraceVariableInNonCaptureKind(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, "policies/rules.yml",
		"rules:\n"+
			"  - id: literal-brace\n    kind: deny_write\n    paths: ['docs/{task_id}.md']\n    mode: block\n    message: m\n"+
			"  - id: alternation-ok\n    kind: deny_write\n    paths: ['src/**/*.{js,ts}']\n    mode: block\n    message: m\n"+
			"  - id: capture-ok\n    kind: require_fresh_file\n    when_paths: ['docs/todo/{task_id}.md']\n    required_files:\n      - path: 'docs/fidelity/{task_id}.json'\n        max_age_hours: 24\n    mode: block\n    message: m\n")

	compiled, err := CompileRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	hits := 0
	for _, w := range compiled.Warnings {
		if strings.Contains(w, "does not capture template variables") {
			hits++
			if !strings.Contains(w, "literal-brace") {
				t.Errorf("warning should name the offending rule: %s", w)
			}
		}
	}
	if hits != 1 {
		t.Fatalf("expected exactly one brace-variable warning (not for alternation or capture kinds), got %d: %v", hits, compiled.Warnings)
	}
}
