package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func writeCLITestLock(t *testing.T, path, body string) {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode lock fixture: %v", err)
	}
	payload["$schema"] = compiler.LockfileSchema()
	payload["format_version"] = compiler.LockfileFormatVersion
	payload["repo_root"] = compiler.PortableRepoRoot
	payload["discovery"] = map[string]interface{}{
		"repo_root":  compiler.PortableRepoRoot,
		"start_path": compiler.PortableRepoRoot,
	}
	sources, ok := payload["sources"].([]interface{})
	if !ok {
		sources = []interface{}{}
		payload["sources"] = sources
	}
	rules, ok := payload["rules"].([]interface{})
	if !ok {
		rules = []interface{}{}
		payload["rules"] = rules
	}
	payload["source_count"] = len(sources)
	payload["rule_count"] = len(rules)
	if _, ok := payload["default_mode"]; !ok {
		payload["default_mode"] = "warn"
	}
	if _, ok := payload["source_digest"]; !ok {
		payload["source_digest"] = strings.Repeat("0", 64)
	}
	delete(payload, "lock_digest")
	digest, err := compiler.ComputeLockDigest(payload)
	if err != nil {
		t.Fatalf("compute lock fixture digest: %v", err)
	}
	payload["lock_digest"] = digest
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode lock fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}
