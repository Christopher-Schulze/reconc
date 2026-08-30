// Package jsonl provides cross-process-safe, bounded JSONL publication.
package jsonl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
)

const (
	appendJournalVersion  = 2
	legacyJournalVersion  = 1
	appendStatePreparing  = "preparing"
	appendStatePrepared   = "prepared"
	appendStatePublished  = "published"
	appendStateCommitting = "committing"
	appendStateResolved   = "resolved"
	maxAppendJournalBytes = 64 * 1024
	// MaxArchiveFiles is the largest archive ring accepted by the JSONL
	// writer. Callers that inspect a ring use this bound for historical files
	// even when their current policy retains fewer archives.
	MaxArchiveFiles = 32
	// DefaultLockTimeout bounds legacy entry points that do not accept a
	// caller context. Context-aware entry points apply both this layout budget
	// and the caller lifecycle.
	DefaultLockTimeout = filelock.DefaultTimeout
)

// ErrTransactionCommitRequired means recovery reached a transaction whose
// caller-owned commit may have started. Only the owner can safely complete it
// by supplying the same idempotent commit callback used for the append.
var ErrTransactionCommitRequired = errors.New("JSONL transaction commit may have started; owner callback is required for recovery")

// ErrLayoutMismatch means an append journal belongs to a different JSONL
// layout than the one supplied for recovery.
var ErrLayoutMismatch = errors.New("JSONL append journal belongs to a different layout")

// Policy bounds one live JSONL file plus a fixed archive ring.
type Policy struct {
	MaxBytes    int64
	MaxArchives int
}

// LayoutSecurity binds a custom JSONL layout to a caller-owned filesystem
// security contract. Existing objects are validated without repair; only
// newly created or atomically replaced files are secured.
type LayoutSecurity interface {
	JSONLSecurityIdentity() string
	ValidateJSONLDirectory(path string) error
	SecureJSONLFile(path string) error
	ValidateJSONLFile(path string, maximum int64) error
	ValidateOpenedJSONLFile(file *os.File, info os.FileInfo, maximum int64) error
}

// Layout binds the lock, transaction journal, temporary backup prefix, and
// filesystem modes used by one JSONL publication. All paths must be distinct,
// clean siblings of the live file. Callers that need stable public names or
// private modes must pass the same layout to append and recovery.
type Layout struct {
	LockPath      string
	JournalPath   string
	BackupPrefix  string
	DirectoryMode os.FileMode
	FileMode      os.FileMode
	JournalMode   os.FileMode
	LockTimeout   time.Duration
	Security      LayoutSecurity
	lockLease     *layoutLockLease
}

func defaultLayout(path string) Layout {
	return Layout{
		LockPath: path + ".lock", JournalPath: path + ".append-transaction.json",
		BackupPrefix: path + ".append-backup", DirectoryMode: 0o755, FileMode: 0o644,
		JournalMode: 0o600, LockTimeout: DefaultLockTimeout,
	}
}

func layoutIsDefault(path string, layout Layout) bool {
	want := defaultLayout(path)
	return layout.Security == nil && layout.LockPath == want.LockPath &&
		layout.JournalPath == want.JournalPath && layout.BackupPrefix == want.BackupPrefix &&
		layout.DirectoryMode == want.DirectoryMode && layout.FileMode == want.FileMode &&
		layout.JournalMode == want.JournalMode && layout.LockTimeout == want.LockTimeout
}

func legacyUnboundedDefaultLayoutIdentity(path string) string {
	legacy := defaultLayout(path)
	legacy.LockTimeout = 0
	return layoutIdentity(path, legacy)
}

func validateLayout(path string, layout Layout) error {
	base := filepath.Base(path)
	if path == "" || filepath.Clean(path) != path || filepath.Dir(path) == path ||
		base == "." || base == ".." || base == string(filepath.Separator) {
		return errors.New("jsonl live path must be non-empty and clean")
	}
	directory := filepath.Dir(path)
	paths := []string{layout.LockPath, layout.JournalPath, layout.BackupPrefix}
	seen := map[string]bool{path: true}
	for _, candidate := range paths {
		if candidate == "" || filepath.Clean(candidate) != candidate || filepath.Dir(candidate) != directory || seen[candidate] {
			return errors.New("jsonl layout paths must be distinct clean siblings of the live file")
		}
		seen[candidate] = true
	}
	if layout.DirectoryMode.Perm() == 0 || layout.DirectoryMode.Perm() != layout.DirectoryMode ||
		layout.FileMode.Perm() == 0 || layout.FileMode.Perm() != layout.FileMode ||
		layout.JournalMode.Perm() == 0 || layout.JournalMode.Perm() != layout.JournalMode {
		return errors.New("jsonl layout modes must contain only non-zero permission bits")
	}
	if layout.LockTimeout < 0 || layout.LockTimeout > 2*time.Minute {
		return errors.New("jsonl layout lock timeout must be between zero and two minutes")
	}
	if layout.Security != nil {
		identity := layout.Security.JSONLSecurityIdentity()
		if identity == "" || len(identity) > 128 || strings.TrimSpace(identity) != identity ||
			strings.ContainsAny(identity, "\x00\r\n") {
			return errors.New("jsonl layout security identity is invalid")
		}
	}
	return nil
}

func validateLayoutSecurityFile(layout Layout, path string, maximum int64) error {
	if layout.Security == nil {
		return nil
	}
	return boundedio.WithRegularFileSnapshot(path, maximum, func(file *os.File, info os.FileInfo) error {
		return validateOpenedLayoutSecurityFile(layout, file, info, maximum)
	})
}

func validateOpenedLayoutSecurityFile(layout Layout, file *os.File, info os.FileInfo, maximum int64) error {
	if layout.Security == nil {
		return nil
	}
	if file == nil || info == nil {
		return errors.New("JSONL file security requires an opened file")
	}
	if err := layout.Security.ValidateOpenedJSONLFile(file, info, maximum); err != nil {
		return fmt.Errorf("validate JSONL file security: %w", err)
	}
	return nil
}

func withValidatedLayoutSecurityFile(
	layout Layout,
	path string,
	maximum int64,
	use func(*os.File, os.FileInfo) error,
) error {
	return withValidatedLayoutSecurityFileLimits(layout, path, maximum, maximum, use)
}

func withValidatedLayoutSecurityFileLimits(
	layout Layout,
	path string,
	snapshotMaximum int64,
	securityMaximum int64,
	use func(*os.File, os.FileInfo) error,
) error {
	if use == nil {
		return errors.New("validated JSONL file requires a callback")
	}
	return boundedio.WithRegularFileSnapshot(path, snapshotMaximum, func(file *os.File, info os.FileInfo) error {
		if err := validateOpenedLayoutSecurityFile(layout, file, info, securityMaximum); err != nil {
			return err
		}
		return use(file, info)
	})
}

func secureLayoutSecurityFile(layout Layout, path string, maximum int64) error {
	if layout.Security == nil {
		return nil
	}
	if err := layout.validateLockLease(); err != nil {
		return err
	}
	if err := layout.Security.SecureJSONLFile(path); err != nil {
		return fmt.Errorf("secure JSONL file: %w", err)
	}
	return validateLayoutSecurityFile(layout, path, maximum)
}

// EnforceResult reports bytes removed from existing live/archive files.
type EnforceResult struct {
	BytesFreed   int64
	FilesRemoved int
}

type appendJournal struct {
	FormatVersion  int                   `json:"format_version"`
	LayoutIdentity string                `json:"layout_identity,omitempty"`
	State          string                `json:"state"`
	Transactional  bool                  `json:"transactional"`
	Rotated        bool                  `json:"rotated"`
	MaxBytes       int64                 `json:"max_bytes"`
	MaxArchives    int                   `json:"max_archives"`
	LiveExisted    bool                  `json:"live_existed"`
	LiveSize       int64                 `json:"live_size"`
	Backups        []appendJournalBackup `json:"backups,omitempty"`
}

func layoutIdentity(path string, layout Layout) string {
	return layoutIdentityWithSecurity(path, layout, true)
}

func layoutIdentityWithSecurity(path string, layout Layout, includeSecurity bool) string {
	hash := sha256.New()
	values := []string{
		filepath.Clean(path), layout.LockPath, layout.JournalPath, layout.BackupPrefix,
		fmt.Sprintf("%04o", layout.DirectoryMode.Perm()), fmt.Sprintf("%04o", layout.FileMode.Perm()),
		fmt.Sprintf("%04o", layout.JournalMode.Perm()), layout.LockTimeout.String(),
	}
	if includeSecurity && layout.Security != nil {
		values = append(values, layout.Security.JSONLSecurityIdentity())
	}
	for _, value := range values {
		_, _ = io.WriteString(hash, value)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type appendJournalBackup struct {
	Index   int    `json:"index"`
	Existed bool   `json:"existed"`
	Mode    uint32 `json:"mode,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// Inspect reports the cleanup Enforce would perform against the current
// snapshot without creating locks, temp files, or other filesystem state.
func Inspect(path string, policy Policy) (EnforceResult, error) {
	return InspectContext(context.Background(), path, policy)
}

// InspectContext reports the cleanup Enforce would perform under the caller
// lifecycle without creating locks or mutating filesystem state.
func InspectContext(ctx context.Context, path string, policy Policy) (EnforceResult, error) {
	if ctx == nil {
		return EnforceResult{}, errors.New("jsonl inspection context is required")
	}
	if err := ctx.Err(); err != nil {
		return EnforceResult{}, err
	}
	if err := validatePolicy(policy); err != nil {
		return EnforceResult{}, err
	}
	result := EnforceResult{}
	candidates, err := archiveCandidatesContext(ctx, path)
	if err != nil {
		return result, err
	}
	for _, candidate := range candidates {
		if candidate.index <= policy.MaxArchives {
			continue
		}
		info, err := os.Lstat(candidate.path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return result, fmt.Errorf("JSONL archive must be a non-symlink regular file: %s", candidate.path)
			}
			result.BytesFreed += info.Size()
			result.FilesRemoved++
		}
	}
	for index := policy.MaxArchives; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		original, kept, _, _, err := tailData(candidate, policy.MaxBytes)
		if err != nil {
			return result, err
		}
		result.BytesFreed += original - kept
	}
	return result, nil
}

// Append writes exactly one newline-terminated record. Rotation happens
// before the append, so every live/archive file remains within MaxBytes.
