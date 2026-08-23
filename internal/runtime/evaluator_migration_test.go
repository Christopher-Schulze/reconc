package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func setLegacyV1SourceDigest(tb testing.TB, payload map[string]interface{}) {
	tb.Helper()
	canonical, err := json.Marshal(map[string]interface{}{
		"source_precedence": payload["source_precedence"],
		"sources":           payload["sources"],
	})
	if err != nil {
		tb.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	payload["source_digest"] = hex.EncodeToString(digest[:])
}

func rewriteLockfile(t *testing.T, repo string, mutate func(map[string]interface{})) {
	t.Helper()
	lockfilePath := filepath.Join(repo, ".reconc", "policy.lock.json")
	data, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal lockfile: %v", err)
	}
	mutate(payload)
	updated, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal lockfile: %v", err)
	}
	updated = append(updated, '\n')
	if err := os.WriteFile(lockfilePath, updated, 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
}

func TestCheckRejectsOldLockfileWithoutRegisteredMigration(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n")

	rewriteLockfile(t, repo, func(payload map[string]interface{}) {
		payload["format_version"] = "0"
	})

	_, err := CheckRepoPolicy(repo, Empty())
	if err == nil {
		t.Fatal("expected error for old lockfile without migration")
	}
	if !strings.Contains(err.Error(), "no migration registered from format_version 0 to "+compiler.LockfileFormatVersion) {
		t.Fatalf("expected missing-migration error, got: %v", err)
	}
}

func TestCheckAcceptsMigratedV1LockfileFromEquivalentCheckout(t *testing.T) {
	withRECONCHome(t)
	policyText := "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n"
	repoA := makeRepo(t, "# project\n", "", policyText)
	repoB := makeRepo(t, "# project\n", "", policyText)

	lockfileA := filepath.Join(repoA, ".reconc", "policy.lock.json")
	data, err := os.ReadFile(lockfileA)
	if err != nil {
		t.Fatalf("read source lockfile: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode source lockfile: %v", err)
	}
	payload["$schema"] = compiler.LegacyLockfileSchemaV1
	payload["format_version"] = "1"
	delete(payload, "actions")
	payload["repo_root"] = repoA
	payload["sources"] = []interface{}{
		map[string]interface{}{
			"kind":    "agents_md",
			"path":    "AGENTS.md",
			"content": "# project\n",
		},
		map[string]interface{}{
			"kind":    "policy_file",
			"path":    "policies/rules.yml",
			"content": policyText,
		},
	}
	setLegacyV1SourceDigest(t, payload)
	discovery, ok := payload["discovery"].(map[string]interface{})
	if !ok {
		t.Fatalf("discovery has type %T", payload["discovery"])
	}
	discovery["repo_root"] = repoA
	discovery["start_path"] = filepath.Join(repoA, "subdir")
	legacy, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("encode legacy lockfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoB, ".reconc", "policy.lock.json"), append(legacy, '\n'), 0o644); err != nil {
		t.Fatalf("write legacy lockfile: %v", err)
	}

	report, err := CheckRepoPolicy(repoB, Empty())
	if err != nil {
		t.Fatalf("migrated equivalent checkout rejected: %v", err)
	}
	if !report.OK || report.Decision != DecisionPass {
		t.Fatalf("migrated equivalent checkout decision=%s ok=%v", report.Decision, report.OK)
	}
}

func TestDecodeLockfileRejectsTamperedV1Sources(t *testing.T) {
	payload := legacyV1IntegrityPayload(t)
	payload["sources"].([]interface{})[0].(map[string]interface{})["content"] = "tampered\n"
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeLockfile(body); err == nil || !strings.Contains(err.Error(), "source_digest") {
		t.Fatalf("runtime accepted tampered format-1 sources: %v", err)
	}
}

func legacyV1IntegrityPayload(tb testing.TB) map[string]interface{} {
	tb.Helper()
	payload := map[string]interface{}{
		"$schema": compiler.LegacyLockfileSchemaV1, "format_version": "1",
		"repo_root": "/tmp/reconc-legacy", "default_mode": "warn",
		"rule_count": 0, "source_count": 1,
		"source_precedence": []interface{}{
			"global", "claude_md", "agents_md", "start_md", "inline_block",
			"compiler_config", "preset", "policy_file",
		},
		"discovery": map[string]interface{}{
			"repo_root": "/tmp/reconc-legacy", "start_path": "/tmp/reconc-legacy",
			"discovered": true, "config_candidates": []interface{}{},
			"policy_paths": []interface{}{}, "warnings": []interface{}{},
		},
		"sources": []interface{}{map[string]interface{}{
			"kind": "agents_md", "path": "AGENTS.md", "content": "# project\n",
		}},
		"rules": []interface{}{},
	}
	setLegacyV1SourceDigest(tb, payload)
	return payload
}

func TestCheckRejectsCurrentLockfileWithPhysicalRoot(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	rewriteLockfile(t, repo, func(payload map[string]interface{}) {
		payload["repo_root"] = repo
	})

	_, err := CheckRepoPolicy(repo, Empty())
	if err == nil || !strings.Contains(err.Error(), "portable '.' marker") {
		t.Fatalf("expected non-portable identity rejection, got %v", err)
	}
}

func TestLoadLockfileAppliesRegisteredMigration(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n")

	rewriteLockfile(t, repo, func(payload map[string]interface{}) {
		payload["format_version"] = "0"
	})

	orig := compiler.Migrations
	defer func() { compiler.Migrations = orig }()
	compiler.Migrations = []compiler.Migration{
		{
			FromVersion: "0",
			ToVersion:   compiler.LockfileFormatVersion,
			Apply: func(payload map[string]interface{}) (map[string]interface{}, error) {
				payload["migrated_test_flag"] = true
				return payload, nil
			},
		},
	}

	payload, err := loadLockfile(repo)
	if err != nil {
		t.Fatalf("loadLockfile: %v", err)
	}
	if payload["format_version"] != compiler.LockfileFormatVersion {
		t.Fatalf("expected migrated format_version %q, got %v", compiler.LockfileFormatVersion, payload["format_version"])
	}
	if payload["migrated_test_flag"] != true {
		t.Fatalf("expected migration to mutate payload, got %v", payload["migrated_test_flag"])
	}
}

func TestDecodeLockfileCachesTypedPartsForCurrentAndMigratedLocks(t *testing.T) {
	withRECONCHome(t)
	policyText := "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n"
	repo := makeRepo(t, "# project\n", "", policyText)
	current, err := os.ReadFile(filepath.Join(repo, ".reconc", "policy.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	assertDecodedLockfilePartsMatchFallback(t, current, false)

	var legacy map[string]interface{}
	if err := json.Unmarshal(current, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["$schema"] = compiler.LegacyLockfileSchemaV1
	legacy["format_version"] = "1"
	legacy["repo_root"] = repo
	delete(legacy, "actions")
	legacy["sources"] = []interface{}{
		map[string]interface{}{"kind": "agents_md", "path": "AGENTS.md", "content": "# project\n"},
		map[string]interface{}{"kind": "policy_file", "path": "policies/rules.yml", "content": policyText},
	}
	setLegacyV1SourceDigest(t, legacy)
	discovery := legacy["discovery"].(map[string]interface{})
	discovery["repo_root"] = repo
	discovery["start_path"] = repo
	legacyBytes, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	assertDecodedLockfilePartsMatchFallback(t, legacyBytes, true)
}

func TestCurrentLockfileKeepsLargePlansOutOfInterfaceTree(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n")
	body, err := os.ReadFile(filepath.Join(repo, ".reconc", "policy.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := decodeLockfile(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.payload["rules"].(json.RawMessage); !ok {
		t.Fatalf("current rules were boxed into %T", lock.payload["rules"])
	}
	if _, ok := lock.payload["actions"].(json.RawMessage); !ok {
		t.Fatalf("current actions were boxed into %T", lock.payload["actions"])
	}
	if lock.envelope == nil || len(lock.rules) != 1 || lock.actions == nil {
		t.Fatalf("typed current lock parts are incomplete: envelope=%p rules=%d actions=%p", lock.envelope, len(lock.rules), lock.actions)
	}
}

func assertDecodedLockfilePartsMatchFallback(t *testing.T, body []byte, wantMigrated bool) {
	t.Helper()
	lock, err := decodeLockfile(body)
	if err != nil {
		t.Fatal(err)
	}
	if lock.migrated != wantMigrated {
		t.Fatalf("migrated=%t, want %t", lock.migrated, wantMigrated)
	}
	if len(lock.rulesJSON) == 0 || len(lock.actionsJSON) == 0 || lock.actions == nil {
		t.Fatalf("decoded lockfile did not retain typed parts: rules=%d actions=%d compiled=%p", len(lock.rulesJSON), len(lock.actionsJSON), lock.actions)
	}
	prepared, err := compileRuntimePlanWithParts(lock.payload, lock.rulesJSON, lock.actionsJSON, lock.actions)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := compileRuntimePlan(lock.payload)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.defaultMode != fallback.defaultMode || prepared.sourceDigest != fallback.sourceDigest ||
		prepared.lockDigest != fallback.lockDigest || prepared.sourceCount != fallback.sourceCount ||
		!reflect.DeepEqual(prepared.rules, fallback.rules) ||
		!reflect.DeepEqual(prepared.sources, fallback.sources) ||
		!reflect.DeepEqual(prepared.actions.Plan(), fallback.actions.Plan()) {
		t.Fatalf("prepared typed plan drifted from fallback:\nprepared=%+v\nfallback=%+v", prepared, fallback)
	}
}
