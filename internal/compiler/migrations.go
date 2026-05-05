package compiler

import (
	"fmt"

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
// from oldest FromVersion to newest. Empty until the format version
// bumps past "1".
//
// When you bump LockfileFormatVersion, append a Migration here that
// transforms the previous layout to the new one. MigrateLockfile then
// picks up the new step automatically.
//
// Never edit or reorder existing entries -- migrations are load-bearing
// for every deployed artefact out there. Only append.
var Migrations = []Migration{}

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
				Message: fmt.Sprintf("no migration registered from format_version %s to %s; re-run `reconc compile`", got, LockfileFormatVersion),
			}
		}
	}
	return current, applied, nil
}
