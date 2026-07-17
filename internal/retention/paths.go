package retention

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
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
func ProjectDir(stateRoot, repoRoot string) string {
	sum := sha256.Sum256([]byte(repoRoot))
	key := hex.EncodeToString(sum[:])[:16]
	return filepath.Join(stateRoot, "projects", key)
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

func sessionFileID(id string) string {
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
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}
