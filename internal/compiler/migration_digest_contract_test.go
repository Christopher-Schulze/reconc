package compiler

import (
	"fmt"
	"testing"

	"reconc.dev/reconc/internal/schema"
)

func TestMigrationStepsReturnUnstampedPayload(t *testing.T) {
	out, err := migrateLockfileV3ToV4(map[string]interface{}{
		"$schema":     schema.LegacyPolicyLockV3URL,
		"lock_digest": "legacy",
	})
	if err != nil {
		t.Fatalf("migrate v3 to v4: %v", err)
	}
	if _, present := out["lock_digest"]; present {
		t.Fatalf("migration step stamped lock_digest: %v", out["lock_digest"])
	}
}

func TestMigrateLockfileDriverOwnsIntermediateAndFinalDigests(t *testing.T) {
	original := Migrations
	defer func() { Migrations = original }()

	Migrations = []Migration{
		{
			FromVersion: "probe-1",
			ToVersion:   "probe-2",
			Apply: func(payload map[string]interface{}) (map[string]interface{}, error) {
				out := cloneLockfileMap(payload)
				out["first"] = true
				delete(out, "lock_digest")
				return out, nil
			},
		},
		{
			FromVersion: "probe-2",
			ToVersion:   LockfileFormatVersion,
			Apply: func(payload map[string]interface{}) (map[string]interface{}, error) {
				stored, _ := payload["lock_digest"].(string)
				computed, err := ComputeLockDigest(payload)
				if err != nil || stored == "" || stored != computed {
					return nil, fmt.Errorf("intermediate digest was not stamped by driver")
				}
				out := cloneLockfileMap(payload)
				out["second"] = true
				delete(out, "lock_digest")
				return out, nil
			},
		},
	}

	out, applied, err := MigrateLockfile(map[string]interface{}{"format_version": "probe-1"})
	if err != nil {
		t.Fatalf("migrate synthetic chain: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("applied migrations = %d, want 2", len(applied))
	}
	stored, _ := out["lock_digest"].(string)
	computed, err := ComputeLockDigest(out)
	if err != nil {
		t.Fatalf("compute final digest: %v", err)
	}
	if stored == "" || stored != computed {
		t.Fatalf("final digest = %q, computed %q", stored, computed)
	}
}
