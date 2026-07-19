package retention

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	MaxSessionIDBytes  = 512
	maxSessionFileStem = 120
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
