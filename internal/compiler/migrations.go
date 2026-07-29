package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/schema"
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
	{
		FromVersion: "2",
		ToVersion:   "3",
		Apply:       migrateLockfileV2ToV3,
	},
}

func migrateLockfileV1ToV2(payload map[string]interface{}) (map[string]interface{}, error) {
	schemaURL, _ := payload["$schema"].(string)
	if schemaURL != LegacyLockfileSchemaV1 &&
		schemaURL != schema.LegacyPolicyLockURLUnpinned &&
		schemaURL != legacyLockfileSchemaForEnterprise("v1") {
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
	out["$schema"] = legacyLockfileSchemaForEnterprise("v2")
	out["format_version"] = "2"
	out["repo_root"] = PortableRepoRoot
	portableDiscovery["repo_root"] = PortableRepoRoot
	portableDiscovery["start_path"] = PortableRepoRoot
	out["discovery"] = portableDiscovery
	digest, err := ComputeLockDigest(out)
	if err != nil {
		return nil, fmt.Errorf("compute portable v2 lockfile digest: %w", err)
	}
	out["lock_digest"] = digest
	return out, nil
}

func migrateLockfileV2ToV3(payload map[string]interface{}) (map[string]interface{}, error) {
	schemaURL, _ := payload["$schema"].(string)
	if schemaURL != schema.LegacyPolicyLockV2URL &&
		schemaURL != schema.LegacyPolicyLockV2URLUnpinned &&
		schemaURL != legacyLockfileSchemaForEnterprise("v2") {
		return nil, fmt.Errorf("legacy lockfile schema %q is not recognized", schemaURL)
	}
	storedDigest, _ := payload["lock_digest"].(string)
	computedDigest, err := ComputeLockDigest(payload)
	if err != nil {
		return nil, fmt.Errorf("compute legacy lockfile digest: %w", err)
	}
	if storedDigest == "" || storedDigest != computedDigest {
		return nil, fmt.Errorf("legacy lockfile payload digest does not match its contents")
	}
	rawSources, ok := payload["sources"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("legacy lockfile sources must contain a list")
	}
	sources := make([]interface{}, 0, len(rawSources))
	for index, item := range rawSources {
		source, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("legacy lockfile sources[%d] must contain an object", index)
		}
		content, ok := source["content"].(string)
		if !ok {
			return nil, fmt.Errorf("legacy lockfile sources[%d].content must contain a string", index)
		}
		kind, _ := source["kind"].(string)
		sourcePath, _ := source["path"].(string)
		if kind == string(policy.SourceGlobal) {
			sourcePath = ingest.GlobalPolicySourcePath
		}
		if !portableSourcePath(sourcePath) {
			return nil, fmt.Errorf("legacy lockfile sources[%d].path is not portable", index)
		}
		digest := sha256.Sum256([]byte(content))
		next := map[string]interface{}{
			"kind":           kind,
			"path":           sourcePath,
			"content_sha256": hex.EncodeToString(digest[:]),
		}
		for _, field := range []string{"block_id", "line_start"} {
			if value, present := source[field]; present {
				next[field] = value
			}
		}
		sources = append(sources, next)
	}
	sourceDigest, err := computeSerializedSourceDigest(sources)
	if err != nil {
		return nil, fmt.Errorf("compute migrated source digest: %w", err)
	}
	out := cloneLockfileMap(payload)
	out["$schema"] = LockfileSchema()
	out["sources"] = sources
	out["source_digest"] = sourceDigest
	return out, nil
}

func legacyLockfileSchemaForEnterprise(version string) string {
	base := strings.TrimRight(os.Getenv("RECONC_SCHEMA_BASE_URL"), "/")
	if base == "" {
		if version == "v1" {
			return LegacyLockfileSchemaV1
		}
		return schema.LegacyPolicyLockV2URL
	}
	return base + "/schemas/policy-lock/" + version
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
	digest, err := ComputeLockDigest(current)
	if err != nil {
		return nil, applied, &rerrors.LockfileError{Message: "compute migrated lockfile digest", Cause: err}
	}
	current["lock_digest"] = digest
	return current, applied, nil
}
