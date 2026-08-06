package retention

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"reconc.dev/reconc/internal/boundedio"
)

// canonicalCaseCache memoizes CanonicalizePathCase per process: hook
// invocations resolve the same repo root many times (state, report,
// active-session, lock paths) and the directory walk would otherwise repeat.
var canonicalCaseCache sync.Map

const (
	MaxSessionIDBytes  = 512
	maxSessionFileStem = 120
	maxCaseDirEntries  = 65_536
)

// ResolveStateRoot returns the product-wide session state root.
func ResolveStateRoot() string {
	if override := strings.TrimSpace(os.Getenv(StateRootEnv)); override != "" {
		return override
	}
	if home := strings.TrimSpace(os.Getenv(ReconcHomeEnv)); home != "" {
		return filepath.Join(home, "sessions", "claude")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".reconc", "sessions", "claude")
	}
	return filepath.Join(os.TempDir(), "reconc-claude-sessions")
}

// ProjectDir is the exact hash-keyed directory used by agent session state.
// The repo root is case-canonicalized before hashing so spelling variants of
// the same checkout on case-insensitive filesystems (`/workspace/repo` vs
// `/workspace/REPO`) share one project bucket instead of silently splitting
// sessions, claims, and command proofs across two.
func ProjectDir(stateRoot, repoRoot string) string {
	sum := sha256.Sum256([]byte(CanonicalizePathCase(repoRoot)))
	key := hex.EncodeToString(sum[:])[:16]
	return filepath.Join(stateRoot, "projects", key)
}

// CanonicalizePathCase returns the on-disk spelling of an existing absolute
// path by walking its components and preferring the directory entry the
// filesystem actually stores. It rewrites only when the rewritten path is
// verifiably the SAME file as the input (os.SameFile), so on case-sensitive
// filesystems - where a case variant is a different file - the input is
// always returned unchanged. Missing paths are returned unchanged.
func CanonicalizePathCase(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	if cached, ok := canonicalCaseCache.Load(path); ok {
		return cached.(string)
	}
	canonical := canonicalizePathCaseUncached(path)
	canonicalCaseCache.Store(path, canonical)
	return canonical
}

func canonicalizePathCaseUncached(path string) string {
	originalInfo, err := os.Stat(path)
	if err != nil {
		return path
	}
	volume := filepath.VolumeName(path)
	rest := strings.Trim(strings.TrimPrefix(filepath.Clean(path), volume), string(filepath.Separator))
	canonical := volume + string(filepath.Separator)
	if rest != "" {
		for _, component := range strings.Split(rest, string(filepath.Separator)) {
			next := filepath.Join(canonical, component)
			if _, err := os.Lstat(next); err != nil {
				return path
			}
			entries, err := boundedio.ReadDir(canonical, maxCaseDirEntries)
			if err != nil {
				canonical = next
				continue
			}
			resolved := component
			for _, entry := range entries {
				if entry.Name() == component {
					resolved = component
					break
				}
				if strings.EqualFold(entry.Name(), component) {
					resolved = entry.Name()
				}
			}
			canonical = filepath.Join(canonical, resolved)
		}
	}
	canonicalInfo, err := os.Stat(canonical)
	if err != nil || !os.SameFile(originalInfo, canonicalInfo) {
		return path
	}
	return canonical
}

func isProjectKey(name string) bool {
	if len(name) != 16 {
		return false
	}
	for _, char := range name {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}

// ValidateSessionID rejects ambiguous or resource-exhausting runtime session
// identifiers before they are used in state paths or active-session pointers.
func ValidateSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("session_id is empty")
	}
	if strings.TrimSpace(id) != id {
		return fmt.Errorf("session_id has leading or trailing whitespace")
	}
	if len(id) > MaxSessionIDBytes {
		return fmt.Errorf("session_id exceeds %d bytes", MaxSessionIDBytes)
	}
	for _, char := range id {
		if unicode.IsControl(char) {
			return fmt.Errorf("session_id contains a control character")
		}
	}
	return nil
}

// SessionFileID returns the collision-resistant filename stem shared by the
// agent-session runtime and retention. Canonical UUID-like IDs remain verbatim;
// transformed or truncated IDs receive a hash of the original source bytes.
func SessionFileID(id string) string {
	id = strings.TrimSpace(id)
	var builder strings.Builder
	for _, char := range id {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_':
			builder.WriteRune(char)
		default:
			builder.WriteRune('_')
		}
	}
	safe := builder.String()
	if safe == id && len(safe) <= maxSessionFileStem {
		return safe
	}
	sum := sha256.Sum256([]byte(id))
	suffix := hex.EncodeToString(sum[:])[:16]
	if len(safe) > maxSessionFileStem-1-len(suffix) {
		safe = safe[:maxSessionFileStem-1-len(suffix)]
	}
	safe = strings.Trim(safe, "_")
	if safe == "" {
		safe = "session"
	}
	return safe + "-" + suffix
}
