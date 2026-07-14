// Package retention owns bounded cleanup for every Reconc runtime storage
// class. It never scans or deletes files outside explicitly owned paths.
package retention

import "time"

const (
	StateRootEnv  = "RECONC_CLAUDE_STATE_DIR"
	ReconcHomeEnv = "RECONC_HOME"
)

// ClassPolicy bounds one set of regular files.
type ClassPolicy struct {
	MaxFiles int
	MaxBytes int64
	MaxAge   time.Duration
}

// Policy is the complete product retention contract.
type Policy struct {
	Sessions             ClassPolicy
	Reports              ClassPolicy
	Locks                ClassPolicy
	GeneratedBinaries    ClassPolicy
	StateTotalBytes      int64
	RepoRuntimeBytes     int64
	AuditFileBytes       int64
	AuditArchives        int
	RunDecisionFileBytes int64
	RunDecisionArchives  int
	AbandonedTempAge     time.Duration
	OwnedTempTotalBytes  int64
	Interval             time.Duration
}

// DefaultPolicy keeps useful recent evidence while placing deterministic
// ceilings on write amplification and persistent disk use.
func DefaultPolicy() Policy {
	return Policy{
		Sessions:             ClassPolicy{MaxFiles: 32, MaxBytes: 8 * 1024 * 1024, MaxAge: 14 * 24 * time.Hour},
		Reports:              ClassPolicy{MaxFiles: 32, MaxBytes: 8 * 1024 * 1024, MaxAge: 14 * 24 * time.Hour},
		Locks:                ClassPolicy{MaxFiles: 128, MaxBytes: 1024 * 1024, MaxAge: 24 * time.Hour},
		GeneratedBinaries:    ClassPolicy{MaxFiles: 8, MaxBytes: 32 * 1024 * 1024, MaxAge: 14 * 24 * time.Hour},
		StateTotalBytes:      16 * 1024 * 1024,
		RepoRuntimeBytes:     48 * 1024 * 1024,
		AuditFileBytes:       2 * 1024 * 1024,
		AuditArchives:        2,
		RunDecisionFileBytes: 2 * 1024 * 1024,
		RunDecisionArchives:  2,
		AbandonedTempAge:     2 * time.Hour,
		OwnedTempTotalBytes:  512 * 1024 * 1024,
		Interval:             6 * time.Hour,
	}
}

// ClassReport is the deterministic cleanup result for one storage class.
type ClassReport struct {
	Name         string `json:"name"`
	FilesKept    int    `json:"files_kept"`
	FilesDeleted int    `json:"files_deleted"`
	BytesBefore  int64  `json:"bytes_before"`
	BytesAfter   int64  `json:"bytes_after"`
	BytesFreed   int64  `json:"bytes_freed"`
}

// Report describes one complete retention pass.
type Report struct {
	Ran             bool          `json:"ran"`
	DryRun          bool          `json:"dry_run"`
	Classes         []ClassReport `json:"classes"`
	StateBytesAfter int64         `json:"state_bytes_after"`
	StateByteBudget int64         `json:"state_byte_budget"`
	RepoBytesAfter  int64         `json:"repo_runtime_bytes_after"`
	RepoByteBudget  int64         `json:"repo_runtime_byte_budget"`
	OwnedTempBytes  int64         `json:"owned_temp_bytes_after"`
	OwnedTempBudget int64         `json:"owned_temp_byte_budget"`
	Errors          []string      `json:"errors,omitempty"`
}

// Options configures one pass. Now and TempRoot exist for deterministic tests;
// zero values use the live clock and OS temp directory.
type Options struct {
	RepoRoot      string
	StateRoot     string
	ActiveSession string
	Policy        Policy
	Now           time.Time
	TempRoot      string
	DryRun        bool
}
