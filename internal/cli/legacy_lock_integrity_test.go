package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
)

func TestReadLockfileSummaryRejectsTamperedV1Sources(t *testing.T) {
	repo := t.TempDir()
	payload := cliLegacyV1Payload(t)
	payload["sources"].([]interface{})[0].(map[string]interface{})["path"] = "CLAUDE.md"
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ingest.LockfilePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readLockfileSummary(repo); err == nil || !strings.Contains(err.Error(), "source_digest") {
		t.Fatalf("CLI summary accepted tampered format-1 sources: %v", err)
	}
}

func cliLegacyV1Payload(t *testing.T) map[string]interface{} {
	t.Helper()
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
