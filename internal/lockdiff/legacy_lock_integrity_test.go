package lockdiff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestDiffRejectsTamperedV1Sources(t *testing.T) {
	dir := t.TempDir()
	payload := diffLegacyV1Payload(t)
	payload["source_precedence"].([]interface{})[0] = "agents_md"
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(dir, "left.json")
	right := filepath.Join(dir, "right.json")
	if err := os.WriteFile(left, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Diff(left, right); err == nil || !strings.Contains(err.Error(), "source_digest") {
		t.Fatalf("lock diff accepted tampered format-1 sources: %v", err)
	}
}

func diffLegacyV1Payload(t *testing.T) map[string]interface{} {
	t.Helper()
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
		},
		"sources": []interface{}{map[string]interface{}{
			"kind": "agents_md", "path": "AGENTS.md", "content": "# project\n",
		}},
		"rules": []interface{}{},
	}
	canonical, err := json.Marshal(map[string]interface{}{
		"source_precedence": payload["source_precedence"], "sources": payload["sources"],
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	payload["source_digest"] = hex.EncodeToString(digest[:])
	return payload
}
