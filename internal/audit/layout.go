package audit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/jsonl"
	"reconc.dev/reconc/internal/privatefs"
)

const auditDirectoryMode os.FileMode = 0o700

type auditLayoutSecurity struct {
	lockPath string
}

func (auditLayoutSecurity) JSONLSecurityIdentity() string { return "reconc-audit-private-jsonl-v1" }

func (security auditLayoutSecurity) ValidateJSONLDirectory(path string) error {
	return privatefs.ValidateDirectory(path)
}

func (security auditLayoutSecurity) SecureJSONLFile(path string) error {
	if path == security.lockPath {
		file, err := privatefs.OpenExistingLock(path)
		if err != nil {
			return err
		}
		return file.Close()
	}
	file, err := privatefs.OpenExistingPrivateFile(path)
	if err != nil {
		return err
	}
	return file.Close()
}

func (security auditLayoutSecurity) ValidateJSONLFile(path string, maximum int64) error {
	return boundedio.WithRegularFileSnapshot(path, maximum, func(file *os.File, info os.FileInfo) error {
		if info.Size() > maximum {
			return fmt.Errorf("audit JSONL file exceeds %d bytes", maximum)
		}
		if path == security.lockPath {
			return privatefs.ValidateFile(file, info)
		}
		return privatefs.ValidateFileAllowLinks(file, info)
	})
}

func auditLayout(path string) jsonl.Layout {
	lockPath := path + ".lock"
	return jsonl.Layout{
		LockPath: lockPath, JournalPath: path + ".append-transaction.json",
		BackupPrefix: path + ".append-backup", DirectoryMode: auditDirectoryMode,
		FileMode: 0o600, JournalMode: 0o600, LockTimeout: filelockTimeout,
		Security: auditLayoutSecurity{lockPath: lockPath},
	}
}

const filelockTimeout = 2 * time.Minute

var preparedAuditDirectories sync.Map
var prepareAuditDirectoryMu sync.Mutex

func prepareAuditLayout(repoRoot string) (jsonl.Layout, error) {
	path := filepath.Join(repoRoot, AuditFileRelative)
	directory := filepath.Dir(path)
	layout := auditLayout(path)
	if _, ok := preparedAuditDirectories.Load(directory); ok {
		ready, err := refreshPreparedAuditLayout(path, layout)
		if err != nil {
			return jsonl.Layout{}, err
		}
		if ready {
			return layout, nil
		}
		preparedAuditDirectories.Delete(directory)
	}
	prepareAuditDirectoryMu.Lock()
	defer prepareAuditDirectoryMu.Unlock()
	if _, ok := preparedAuditDirectories.Load(directory); ok {
		ready, err := refreshPreparedAuditLayout(path, layout)
		if err != nil {
			return jsonl.Layout{}, err
		}
		if ready {
			return layout, nil
		}
		preparedAuditDirectories.Delete(directory)
	}
	if info, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		if err := privatefs.RepairDirectory(directory); err != nil {
			return jsonl.Layout{}, fmt.Errorf("secure audit directory: %w", err)
		}
	} else if err != nil {
		return jsonl.Layout{}, fmt.Errorf("inspect audit directory: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return jsonl.Layout{}, fmt.Errorf("audit directory must be a non-symlink directory")
	} else if info.Mode().Perm() == auditDirectoryMode.Perm() {
		if err := privatefs.ValidateDirectory(directory); err != nil {
			return jsonl.Layout{}, fmt.Errorf("validate audit directory: %w", err)
		}
	} else if err := privatefs.RepairDirectory(directory); err != nil {
		return jsonl.Layout{}, fmt.Errorf("migrate audit directory: %w", err)
	}
	for _, candidate := range auditLayoutFiles(path, layout) {
		if err := migrateAuditFile(candidate, candidate == layout.LockPath); err != nil {
			return jsonl.Layout{}, err
		}
	}
	preparedAuditDirectories.Store(directory, struct{}{})
	return layout, nil
}

func refreshPreparedAuditLayout(path string, layout jsonl.Layout) (bool, error) {
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect audit directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("audit directory must be a non-symlink directory")
	}
	if info.Mode().Perm() != auditDirectoryMode.Perm() {
		if err := privatefs.RepairDirectory(directory); err != nil {
			return false, fmt.Errorf("migrate audit directory: %w", err)
		}
	}
	for _, candidate := range auditLayoutFiles(path, layout) {
		if err := migrateAuditFile(candidate, candidate == layout.LockPath); err != nil {
			return false, err
		}
	}
	return true, nil
}

func auditLayoutFiles(path string, layout jsonl.Layout) []string {
	files := []string{path, layout.LockPath, layout.JournalPath, filepath.Join(filepath.Dir(path), "audit.head.json")}
	for index := 1; index <= MaxArchiveFiles; index++ {
		files = append(files, fmt.Sprintf("%s.%d", path, index))
	}
	for index := 0; index <= MaxArchiveFiles; index++ {
		files = append(files, fmt.Sprintf("%s.%d", layout.BackupPrefix, index))
	}
	return files
}

func migrateAuditFile(path string, lock bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect audit layout file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("audit layout file must be a non-symlink regular file: %s", path)
	}
	if info.Mode().Perm() == 0o600 {
		return nil
	}
	var file *os.File
	if lock {
		file, err = privatefs.OpenExistingLock(path)
	} else {
		file, err = privatefs.OpenExistingPrivateFile(path)
	}
	if err != nil {
		return fmt.Errorf("migrate audit layout file %s: %w", path, err)
	}
	return file.Close()
}

func validateAuditHead(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("audit detached head must be a non-symlink regular file")
	}
	file, err := privatefs.OpenExistingPrivateFile(path)
	if err != nil {
		return err
	}
	return file.Close()
}

func validateAuditContentFiles(path string, layout jsonl.Layout) error {
	sources, err := jsonl.PathsOldestFirst(path, MaxArchiveFiles)
	if err != nil {
		return err
	}
	security, ok := layout.Security.(auditLayoutSecurity)
	if !ok {
		return errors.New("audit layout security contract is unavailable")
	}
	for _, source := range sources {
		if err := security.ValidateJSONLFile(source, DefaultMaxSizeBytes); err != nil {
			return fmt.Errorf("validate audit evidence %s: %w", source, err)
		}
	}
	return validateAuditHead(filepath.Join(filepath.Dir(path), "audit.head.json"))
}

func auditLayoutForRead(repoRoot string) (jsonl.Layout, error) {
	return prepareAuditLayout(repoRoot)
}
