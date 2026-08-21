package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestMigratePolicyLockBytesRejectsTamperedV1Sources(t *testing.T) {
	payload := map[string]interface{}{
		"$schema": compiler.LegacyLockfileSchemaV1, "format_version": "1",
		"repo_root": "/tmp/reconc-legacy",
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
	}
	setLegacyV1SourceDigest(t, payload)
	payload["sources"].([]interface{})[0].(map[string]interface{})["kind"] = "claude_md"
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := migratePolicyLockBytes(body); err == nil || !strings.Contains(err.Error(), "source_digest") {
		t.Fatalf("repository sync accepted tampered format-1 sources: %v", err)
	}
}
