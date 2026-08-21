package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/schema"
)

func TestMigrateLockfileCurrentVersionIsNoOp(t *testing.T) {
	payload := map[string]interface{}{"format_version": LockfileFormatVersion, "foo": "bar"}
	out, applied, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatalf("MigrateLockfile: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("expected no migrations applied, got %d", len(applied))
	}
	if out["foo"] != "bar" {
		t.Errorf("payload should be unchanged for current version; got %v", out)
	}
}

func TestMigrateLockfileV1ToPortableV2(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	payload := legacyV1Payload()
	const historicalDigest = "449d143020f0c1cc7a3c395b1b31c2ec1c0bef2e94b001a79529e9bbe5e93083"
	if payload["source_digest"] != historicalDigest {
		t.Fatalf("historical format-1 source digest = %v, want %s", payload["source_digest"], historicalDigest)
	}

	out, applied, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatalf("MigrateLockfile: %v", err)
	}
	if len(applied) != 5 || applied[0].FromVersion != "1" || applied[len(applied)-1].ToVersion != LockfileFormatVersion {
		t.Fatalf("unexpected migration chain: %+v", applied)
	}
	if out["$schema"] != DefaultLockfileSchema {
		t.Fatalf("schema=%v want %q", out["$schema"], DefaultLockfileSchema)
	}
	if out["repo_root"] != PortableRepoRoot {
		t.Fatalf("repo_root=%v want %q", out["repo_root"], PortableRepoRoot)
	}
	discovery, ok := out["discovery"].(map[string]interface{})
	if !ok {
		t.Fatalf("discovery has unexpected type %T", out["discovery"])
	}
	if discovery["repo_root"] != PortableRepoRoot || discovery["start_path"] != PortableRepoRoot {
		t.Fatalf("discovery paths were not made portable: %v", discovery)
	}
	if payload["repo_root"] != "/tmp/original-checkout" {
		t.Fatal("migration must not mutate the caller payload")
	}
}

func TestMigrateLockfileV1RejectsSourceTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{name: "missing digest", mutate: func(payload map[string]interface{}) { delete(payload, "source_digest") }},
		{name: "malformed digest", mutate: func(payload map[string]interface{}) { payload["source_digest"] = "nope" }},
		{name: "uppercase digest", mutate: func(payload map[string]interface{}) {
			payload["source_digest"] = strings.ToUpper(payload["source_digest"].(string))
		}},
		{name: "precedence", mutate: func(payload map[string]interface{}) { payload["source_precedence"].([]interface{})[0] = "agents_md" }},
		{name: "source order", mutate: func(payload map[string]interface{}) {
			sources := payload["sources"].([]interface{})
			payload["sources"] = []interface{}{sources[1], sources[0]}
		}},
		{name: "source kind", mutate: mutateLegacyV1Source("kind", "claude_md")},
		{name: "source path", mutate: mutateLegacyV1Source("path", "CLAUDE.md")},
		{name: "source body", mutate: mutateLegacyV1Source("content", "changed\n")},
		{name: "block id", mutate: mutateLegacyV1Source("block_id", "changed")},
		{name: "line start", mutate: mutateLegacyV1Source("line_start", json.Number("8"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := legacyV1PayloadWithInlineBlock()
			test.mutate(payload)
			if _, _, err := MigrateLockfile(payload); err == nil || !strings.Contains(err.Error(), "source_digest") {
				t.Fatalf("tampered format-1 source payload accepted: %v", err)
			}
		})
	}
}

func legacyV1Payload() map[string]interface{} {
	payload := map[string]interface{}{
		"$schema":        LegacyLockfileSchemaV1,
		"format_version": "1",
		"repo_root":      "/tmp/original-checkout",
		"discovery": map[string]interface{}{
			"repo_root":  "/tmp/original-checkout",
			"start_path": "/tmp/original-checkout/subdir",
			"discovered": true,
		},
		"source_precedence": legacyV1SourcePrecedence(),
		"sources": []interface{}{
			map[string]interface{}{
				"kind":    string(policy.SourceAgentsMD),
				"path":    "AGENTS.md",
				"content": "# project\n",
			},
		},
	}
	payload["source_digest"] = legacyV1SourceDigest(payload)
	return payload
}

func legacyV1PayloadWithInlineBlock() map[string]interface{} {
	payload := legacyV1Payload()
	payload["sources"] = append(payload["sources"].([]interface{}), map[string]interface{}{
		"kind": "inline_block", "path": "AGENTS.md", "content": "rules: []\n",
		"block_id": "block-1", "line_start": json.Number("7"),
	})
	payload["source_digest"] = legacyV1SourceDigest(payload)
	return payload
}

func legacyV1SourcePrecedence() []interface{} {
	return []interface{}{
		"global", "claude_md", "agents_md", "start_md", "inline_block",
		"compiler_config", "preset", "policy_file",
	}
}

func legacyV1SourceDigest(payload map[string]interface{}) string {
	canonical, err := json.Marshal(map[string]interface{}{
		"source_precedence": payload["source_precedence"],
		"sources":           payload["sources"],
	})
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

func mutateLegacyV1Source(field string, value interface{}) func(map[string]interface{}) {
	return func(payload map[string]interface{}) {
		payload["sources"].([]interface{})[1].(map[string]interface{})[field] = value
	}
}

func TestMigrateLockfileV2RemovesSourceBodiesAndPreservesFreshnessDigest(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	content := "rules: []\n"
	legacyPath := strings.Join([]string{"", "Users", "example", ".reconc", "global-policy.yml"}, "/")
	payload := map[string]interface{}{
		"$schema":        schema.LegacyPolicyLockV2URLUnpinned,
		"format_version": "2",
		"repo_root":      PortableRepoRoot,
		"discovery": map[string]interface{}{
			"repo_root":  PortableRepoRoot,
			"start_path": PortableRepoRoot,
			"discovered": true,
		},
		"sources": []interface{}{
			map[string]interface{}{
				"kind":    string(policy.SourceGlobal),
				"path":    legacyPath,
				"content": content,
			},
		},
	}
	legacyDigest, err := ComputeLockDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["lock_digest"] = legacyDigest

	out, applied, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatalf("MigrateLockfile: %v", err)
	}
	if len(applied) != 4 || applied[0].FromVersion != "2" || applied[len(applied)-1].ToVersion != LockfileFormatVersion {
		t.Fatalf("unexpected migration chain: %+v", applied)
	}
	source := out["sources"].([]interface{})[0].(map[string]interface{})
	if _, leaked := source["content"]; leaked {
		t.Fatalf("migrated source leaked content: %#v", source)
	}
	if source["path"] != ingest.GlobalPolicySourcePath || len(source["content_sha256"].(string)) != 64 {
		t.Fatalf("migrated source provenance = %#v", source)
	}
}

func TestMigrateLockfileV2RejectsTamperingBeforeMigration(t *testing.T) {
	payload := map[string]interface{}{
		"$schema":        schema.LegacyPolicyLockV2URLUnpinned,
		"format_version": "2",
		"lock_digest":    strings.Repeat("a", 64),
		"sources":        []interface{}{},
	}
	if _, _, err := MigrateLockfile(payload); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("expected tampered v2 rejection, got %v", err)
	}
}

func TestMigrateLockfileV1RejectsUnknownSchema(t *testing.T) {
	payload := map[string]interface{}{
		"$schema":        "https://attacker.invalid/policy-lock/v1",
		"format_version": "1",
		"repo_root":      "/tmp/original-checkout",
		"discovery": map[string]interface{}{
			"repo_root":  "/tmp/original-checkout",
			"start_path": "/tmp/original-checkout",
		},
	}
	_, _, err := MigrateLockfile(payload)
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("expected legacy schema rejection, got %v", err)
	}
}

func TestMigrateLockfileV1RejectsMalformedPhysicalIdentity(t *testing.T) {
	payload := map[string]interface{}{
		"$schema":        LegacyLockfileSchemaV1,
		"format_version": "1",
		"repo_root":      "/tmp/original-checkout",
		"discovery": map[string]interface{}{
			"repo_root":  "/tmp/original-checkout",
			"start_path": "",
		},
	}
	_, _, err := MigrateLockfile(payload)
	if err == nil || !strings.Contains(err.Error(), "discovery.start_path") {
		t.Fatalf("expected malformed legacy identity rejection, got %v", err)
	}
}

func TestMigrateLockfileMissingVersionErrors(t *testing.T) {
	payload := map[string]interface{}{"foo": "bar"}
	_, _, err := MigrateLockfile(payload)
	if err == nil {
		t.Fatal("expected error for missing format_version")
	}
	if !strings.Contains(err.Error(), "format_version") {
		t.Errorf("error should mention format_version; got %v", err)
	}
}

func TestMigrateLockfileUnknownVersionErrors(t *testing.T) {
	payload := map[string]interface{}{"format_version": "99"}
	_, _, err := MigrateLockfile(payload)
	if err == nil {
		t.Fatal("expected error for unknown version")
	}
	if !strings.Contains(err.Error(), "no migration registered") {
		t.Errorf("error should mention missing migration; got %v", err)
	}
}

func TestMigrateLockfileAppliesChain(t *testing.T) {
	// Inject a synthetic chain 0 -> 0.5 -> current. We reset via
	// defer to keep the registry clean for other tests.
	orig := Migrations
	defer func() { Migrations = orig }()
	Migrations = []Migration{
		{FromVersion: "0", ToVersion: "0.5", Apply: func(p map[string]interface{}) (map[string]interface{}, error) {
			p["step1"] = true
			return p, nil
		}},
		{FromVersion: "0.5", ToVersion: LockfileFormatVersion, Apply: func(p map[string]interface{}) (map[string]interface{}, error) {
			p["step2"] = true
			return p, nil
		}},
	}

	payload := map[string]interface{}{"format_version": "0"}
	out, applied, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatalf("MigrateLockfile: %v", err)
	}
	if len(applied) != 2 {
		t.Errorf("expected 2 migrations applied, got %d", len(applied))
	}
	if out["step1"] != true || out["step2"] != true {
		t.Errorf("both migrations should have run; got %v", out)
	}
	if out["format_version"] != LockfileFormatVersion {
		t.Errorf("final version should be current; got %v", out["format_version"])
	}
}

func TestMigrateLockfilePropagatesApplyError(t *testing.T) {
	orig := Migrations
	defer func() { Migrations = orig }()
	Migrations = []Migration{
		{FromVersion: "0", ToVersion: LockfileFormatVersion, Apply: func(p map[string]interface{}) (map[string]interface{}, error) {
			return nil, &testErr{msg: "boom"}
		}},
	}
	payload := map[string]interface{}{"format_version": "0"}
	_, _, err := MigrateLockfile(payload)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected apply error to propagate; got %v", err)
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

// TestMigrateLockfileV3ToCurrentStampsTheCurrentContract proves a format-3 lock
// reaches the current format with a consistent self-digest. The rule payload is
// carried verbatim: a legacy lock simply declares no script cache inputs.
func TestMigrateLockfileV3ToCurrentStampsTheCurrentContract(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	payload := map[string]interface{}{
		"$schema":        schema.LegacyPolicyLockV3URL,
		"format_version": "3",
		"repo_root":      PortableRepoRoot,
		"discovery": map[string]interface{}{
			"repo_root":  PortableRepoRoot,
			"start_path": PortableRepoRoot,
			"discovered": true,
		},
		"sources": []interface{}{
			map[string]interface{}{
				"kind":           string(policy.SourcePolicyFile),
				"path":           "policies/rules.yml",
				"content_sha256": strings.Repeat("a", 64),
			},
		},
		"rules": []interface{}{
			map[string]interface{}{
				"id": "gate", "kind": "require_script", "message": "gate",
				"script": "scripts/check.sh",
			},
		},
	}
	digest, err := ComputeLockDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["lock_digest"] = digest

	out, applied, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatalf("MigrateLockfile: %v", err)
	}
	if len(applied) != 3 || applied[0].FromVersion != "3" || applied[len(applied)-1].ToVersion != LockfileFormatVersion {
		t.Fatalf("unexpected migration chain: %+v", applied)
	}
	if out["format_version"] != LockfileFormatVersion {
		t.Fatalf("format_version = %v, want %s", out["format_version"], LockfileFormatVersion)
	}
	if out["$schema"] != DefaultLockfileSchema {
		t.Fatalf("schema = %v, want %q", out["$schema"], DefaultLockfileSchema)
	}
	migratedDigest, err := ComputeLockDigest(out)
	if err != nil {
		t.Fatal(err)
	}
	if out["lock_digest"] != migratedDigest {
		t.Fatal("migrated lockfile digest does not describe its own contents")
	}
	rule := out["rules"].([]interface{})[0].(map[string]interface{})
	if _, declared := rule["cache_inputs"]; declared {
		t.Fatalf("migration invented a declaration: %#v", rule)
	}
}

// TestMigrateLockfileV3RejectsForeignSchema keeps the step from accepting a
// payload that only claims to be format 3.
func TestMigrateLockfileV3RejectsForeignSchema(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	_, _, err := MigrateLockfile(map[string]interface{}{
		"$schema":        "https://example.test/other.schema.json",
		"format_version": "3",
	})
	if err == nil || !strings.Contains(err.Error(), "is not recognized") {
		t.Fatalf("error = %v, want an unrecognized-schema refusal", err)
	}
}

func TestMigrateLockfileV4LowersLegacyMCPIntoOneStableActionPlan(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	payload := map[string]interface{}{
		"$schema":        schema.LegacyPolicyLockV4URL,
		"format_version": "4",
		"mcp": map[string]interface{}{
			"unclassified": "deny",
			"tools": []interface{}{
				map[string]interface{}{
					"platform": "cursor", "tool": "write", "effect": "repository_write",
					"path_fields": []interface{}{`/path`}, "source_path": ".reconc.yml",
				},
			},
		},
	}
	digest, err := ComputeLockDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["lock_digest"] = digest

	first, applied, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatalf("migrate v4: %v", err)
	}
	second, _, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatalf("repeat migrate v4: %v", err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("repeated v4 migrations are not byte-identical")
	}
	if len(applied) != 2 || applied[0].FromVersion != "4" || applied[len(applied)-1].ToVersion != LockfileFormatVersion {
		t.Fatalf("migration chain = %+v", applied)
	}
	if _, present := first["mcp"]; present {
		t.Fatal("migrated lock retained a parallel MCP plan")
	}
	actions := first["actions"].(map[string]interface{})
	defaults := actions["defaults"].(map[string]interface{})
	tools := actions["tools"].([]interface{})
	tool := tools[0].(map[string]interface{})
	if defaults["host_unmatched"] != "block" || defaults["gateway_unmatched"] != "block" ||
		tool["origin"] != "legacy_mcp" || tool["transport"] != "host_mcp" ||
		!strings.HasPrefix(tool["id"].(string), legacyMCPIDPrefix) {
		t.Fatalf("migrated action contract = %#v", actions)
	}
	ledger := actions["ledger"].(map[string]interface{})
	if ledger["mode"] != "required" || ledger["tool_identity"] != "declaration_id" ||
		len(ledger["selected_fields"].([]interface{})) != 0 {
		t.Fatalf("migrated ledger contract = %#v", ledger)
	}
}

func TestMigrateLockfileV5AddsLedgerWithoutMutatingInput(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	actions := map[string]interface{}{
		"format_version": "1", "tools": []interface{}{}, "rules": []interface{}{},
		"budgets": []interface{}{}, "approvals": []interface{}{}, "detectors": []interface{}{},
		"defaults": map[string]interface{}{},
	}
	payload := map[string]interface{}{
		"$schema": schema.LegacyPolicyLockV5URL, "format_version": "5", "actions": actions,
	}
	digest, err := ComputeLockDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["lock_digest"] = digest
	out, applied, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0].FromVersion != "5" || applied[0].ToVersion != "6" {
		t.Fatalf("migration chain = %+v", applied)
	}
	if _, mutated := actions["ledger"]; mutated {
		t.Fatal("v5 migration mutated the caller's nested action plan")
	}
	ledger := out["actions"].(map[string]interface{})["ledger"].(map[string]interface{})
	if ledger["mode"] != "required" || ledger["tool_identity"] != "declaration_id" {
		t.Fatalf("migrated ledger = %#v", ledger)
	}
}

func TestMigrateLockfileV4RejectsTamperingBeforeLowering(t *testing.T) {
	payload := map[string]interface{}{
		"$schema":        schema.LegacyPolicyLockV4URL,
		"format_version": "4",
		"lock_digest":    strings.Repeat("a", 64),
	}
	if _, _, err := MigrateLockfile(payload); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered v4 lock accepted: %v", err)
	}
}
