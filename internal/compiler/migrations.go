package compiler

import (
	"fmt"
	"os"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
)

// Migration is one step in the lockfile format evolution. Each entry
// knows which `format_version` it reads, which it writes, and the
// pure transformation between them. Migrations compose: the reader
// walks the chain until it reaches the current format.
//
// Keeping migrations as pure functions (map -> map, no IO) makes
// them trivially testable and lets us dry-run them in `reconc doctor`.
type Migration struct {
	FromVersion string
	ToVersion   string
	Apply       func(payload map[string]interface{}) (map[string]interface{}, error)
}

// Migrations registers the full chain of lockfile migrations, ordered
// from oldest FromVersion to newest.
//
// When you bump LockfileFormatVersion, append a Migration here that
// transforms the previous layout to the new one. MigrateLockfile then
// picks up the new step automatically.
//
// Never edit or reorder existing entries -- migrations are load-bearing
// for every deployed artefact out there. Only append.
var Migrations = []Migration{
	{
		FromVersion: "1",
		ToVersion:   "2",
		Apply:       migrateLockfileV1ToV2,
	},
}

func migrateLockfileV1ToV2(payload map[string]interface{}) (map[string]interface{}, error) {
	schemaURL, _ := payload["$schema"].(string)
	if schemaURL != LegacyLockfileSchemaV1 && schemaURL != legacyLockfileSchemaForEnterprise() {
		return nil, fmt.Errorf("legacy lockfile schema %q is not recognized", schemaURL)
	}
	if root, ok := payload["repo_root"].(string); !ok || strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("legacy lockfile repo_root must be a non-empty string")
	}
	discovery, ok := payload["discovery"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("legacy lockfile discovery must contain an object")
	}
	for _, field := range []string{"repo_root", "start_path"} {
		if value, ok := discovery[field].(string); !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("legacy lockfile discovery.%s must be a non-empty string", field)
		}
	}

	out := cloneLockfileMap(payload)
	portableDiscovery := cloneLockfileMap(discovery)
	out["$schema"] = LockfileSchema()
	out["repo_root"] = PortableRepoRoot
	portableDiscovery["repo_root"] = PortableRepoRoot
	portableDiscovery["start_path"] = PortableRepoRoot
	out["discovery"] = portableDiscovery
	return out, nil
}

func legacyLockfileSchemaForEnterprise() string {
	base := strings.TrimRight(os.Getenv("RECONC_SCHEMA_BASE_URL"), "/")
	if base == "" {
		return LegacyLockfileSchemaV1
	}
	return base + "/schemas/policy-lock/v1"
}

func cloneLockfileMap(input map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

// MigrateLockfile walks the Migrations chain from the payload's
// `format_version` to LockfileFormatVersion. Returns the migrated
// payload and the slice of applied migrations (empty for a fresh
// lockfile that needs no migration).
//
// Errors when:
//   - payload has no `format_version` field
//   - format_version is newer than this binary knows about
//   - no migration path exists from the payload's version to current
func MigrateLockfile(payload map[string]interface{}) (map[string]interface{}, []Migration, error) {
	rawVer, ok := payload["format_version"]
	if !ok {
		return nil, nil, &rerrors.LockfileError{
			Message: "lockfile missing format_version; unable to migrate",
		}
	}
	got, ok := rawVer.(string)
	if !ok {
		return nil, nil, &rerrors.LockfileError{
			Message: fmt.Sprintf("lockfile format_version must be string, got %T", rawVer),
		}
	}
	if got == LockfileFormatVersion {
		return payload, nil, nil
	}

	// Walk the chain. Each step must bring us closer to the current
	// version; cycles are impossible because each Migration increases
	// the from->to version monotonically per the append-only invariant.
	current := payload
	applied := []Migration{}
	guard := 0
	for got != LockfileFormatVersion {
		guard++
		if guard > 100 {
			return nil, nil, &rerrors.LockfileError{
				Message: "migration chain looped (>100 steps); check Migrations table for a cycle",
			}
		}
		found := false
		for _, m := range Migrations {
			if m.FromVersion == got {
				next, err := m.Apply(current)
				if err != nil {
					return nil, applied, &rerrors.LockfileError{
						Message: fmt.Sprintf("migration %s->%s failed: %s", m.FromVersion, m.ToVersion, err.Error()),
						Cause:   err,
					}
				}
				next["format_version"] = m.ToVersion
				current = next
				got = m.ToVersion
				applied = append(applied, m)
				found = true
				break
			}
		}
		if !found {
			return nil, applied, &rerrors.LockfileError{
				Message: fmt.Sprintf("no migration registered from format_version %s to %s", got, LockfileFormatVersion),
			}
		}
	}
	return current, applied, nil
}
