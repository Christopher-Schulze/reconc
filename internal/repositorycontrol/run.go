package repositorycontrol

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/jsonl"
	"reconc.dev/reconc/internal/privatefs"
)

// RunDecisionLockTimeout is the shared finite writer and retention budget for
// the repository-run JSONL ring.
const RunDecisionLockTimeout = 30 * time.Second

// EnsureRunDirectory establishes the current-user-only repository runtime
// boundary below the shareable .reconc root. Existing insecure directories
// fail closed and are never repaired implicitly.
func EnsureRunDirectory(repoRoot string) error {
	if err := EnsureRoot(repoRoot); err != nil {
		return err
	}
	path := filepath.Join(repoRoot, RootName, RunName)
	if err := privatefs.EnsureDirectory(path); err != nil {
		return fmt.Errorf("secure repository run directory: %w", err)
	}
	return nil
}

// ValidateRunDirectory validates the current-user-only runtime boundary
// without creating or repairing it.
func ValidateRunDirectory(path string) error {
	return privatefs.ValidateDirectory(path)
}

// RunDecisionLayout defines the private JSONL access contract shared by the
// repository-run writer and retention owner.
func RunDecisionLayout(path string) jsonl.Layout {
	lockPath := path + ".lock"
	return jsonl.Layout{
		LockPath: lockPath, JournalPath: path + ".append-transaction.json",
		BackupPrefix: path + ".append-backup", DirectoryMode: privatefs.PrivateDirectoryMode,
		FileMode: privatefs.PrivateFileMode, JournalMode: privatefs.PrivateFileMode,
		LockTimeout: RunDecisionLockTimeout, Security: privateJSONLSecurity{lockPath: lockPath},
	}
}

type privateJSONLSecurity struct {
	lockPath string
}

func (privateJSONLSecurity) JSONLSecurityIdentity() string {
	return "reconc-repository-run-private-jsonl-v1"
}

func (privateJSONLSecurity) ValidateJSONLDirectory(path string) error {
	return privatefs.ValidateDirectory(path)
}

func (security privateJSONLSecurity) SecureJSONLFile(path string) error {
	var file *os.File
	var err error
	if path == security.lockPath {
		file, err = privatefs.OpenExistingLock(path)
	} else {
		file, err = privatefs.OpenExistingPrivateFile(path)
	}
	if err != nil {
		return err
	}
	return file.Close()
}

func (security privateJSONLSecurity) ValidateJSONLFile(path string, maximum int64) error {
	return boundedio.WithRegularFileSnapshot(path, maximum, func(file *os.File, info os.FileInfo) error {
		return security.ValidateOpenedJSONLFile(file, info, maximum)
	})
}

func (security privateJSONLSecurity) ValidateOpenedJSONLFile(file *os.File, info os.FileInfo, _ int64) error {
	if file == nil || info == nil {
		return errors.New("repository run JSONL file handle is unavailable")
	}
	if file.Name() == security.lockPath {
		return privatefs.ValidateFile(file, info)
	}
	return privatefs.ValidateFileAllowLinks(file, info)
}
