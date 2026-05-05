package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

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
	if !strings.Contains(err.Error(), "no migration registered from format_version 0 to 1") {
		t.Fatalf("expected missing-migration error, got: %v", err)
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
