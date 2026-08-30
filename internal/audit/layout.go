package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/jsonl"
	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/repositorycontrol"
)

const auditDirectoryMode os.FileMode = repositorycontrol.PublicDirectoryMode

type auditLayoutSecurity struct {
	lockPath string
}

func (auditLayoutSecurity) JSONLSecurityIdentity() string { return "reconc-audit-private-jsonl-v1" }

func (security auditLayoutSecurity) ValidateJSONLDirectory(path string) error {
	return repositorycontrol.ValidateRoot(path)
}

func (security auditLayoutSecurity) SecureJSONLFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	secure := privatefs.SecureFileAllowLinks
	if path == security.lockPath {
		secure = privatefs.SecureFile
	}
	return errors.Join(secure(file), file.Close())
}

func (security auditLayoutSecurity) ValidateJSONLFile(path string, maximum int64) error {
	return boundedio.WithRegularFileSnapshot(path, maximum, func(file *os.File, info os.FileInfo) error {
		return security.ValidateOpenedJSONLFile(file, info, maximum)
	})
}

func (security auditLayoutSecurity) ValidateOpenedJSONLFile(file *os.File, info os.FileInfo, maximum int64) error {
	if file == nil || info == nil {
		return errors.New("audit JSONL file handle is unavailable")
	}
	if info.Size() > maximum {
		return fmt.Errorf("audit JSONL file exceeds %d bytes", maximum)
	}
	if file.Name() == security.lockPath {
		return privatefs.ValidateFile(file, info)
	}
	return privatefs.ValidateFileAllowLinks(file, info)
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

const (
	filelockTimeout        = 2 * time.Minute
	auditAppendGateTimeout = 5 * time.Minute
)

var preparedAuditDirectories sync.Map
var prepareAuditDirectoryMu sync.Mutex
var errAuditAppendGateTimeout = errors.New("audit append serialization timed out")

type auditAppendGate struct {
	token chan struct{}
	refs  int
}

var auditAppendGates = struct {
	sync.Mutex
	values map[string]*auditAppendGate
}{values: make(map[string]*auditAppendGate)}

// acquireAuditAppendGate serializes append transactions from this process
// before they contend on the authoritative cross-process file lock. References
// include holders and waiters, so an idle per-directory gate is removed as soon
// as its last transaction releases or abandons it.
func acquireAuditAppendGate(ctx context.Context, repoRoot string, timeout time.Duration) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		return nil, errors.New("audit append serialization timeout must be positive")
	}
	directory := filepath.Clean(filepath.Dir(filepath.Join(repoRoot, AuditFileRelative)))
	auditAppendGates.Lock()
	gate := auditAppendGates.values[directory]
	if gate == nil {
		gate = &auditAppendGate{token: make(chan struct{}, 1)}
		auditAppendGates.values[directory] = gate
	}
	gate.refs++
	auditAppendGates.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case gate.token <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-gate.token
				releaseAuditAppendGate(directory, gate)
			})
		}, nil
	case <-ctx.Done():
		releaseAuditAppendGate(directory, gate)
		return nil, ctx.Err()
	case <-timer.C:
		releaseAuditAppendGate(directory, gate)
		return nil, fmt.Errorf("%w after %s", errAuditAppendGateTimeout, timeout)
	}
}

func releaseAuditAppendGate(directory string, gate *auditAppendGate) {
	auditAppendGates.Lock()
	defer auditAppendGates.Unlock()
	gate.refs--
	if gate.refs == 0 && auditAppendGates.values[directory] == gate {
		delete(auditAppendGates.values, directory)
	}
}

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
	if err := repositorycontrol.EnsureRoot(repoRoot); err != nil {
		return jsonl.Layout{}, fmt.Errorf("prepare audit control root: %w", err)
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
	if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err := repositorycontrol.ValidateRoot(directory); err != nil {
		return false, fmt.Errorf("validate audit control root: %w", err)
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
	if auditFileAccessReady(info) {
		return nil
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("migrate audit layout file %s: %w", path, err)
	}
	opened, statErr := file.Stat()
	if statErr != nil {
		return errors.Join(statErr, file.Close())
	}
	validate := privatefs.ValidateFileAllowLinks
	secure := privatefs.SecureFileAllowLinks
	if lock {
		validate = privatefs.ValidateFile
		secure = privatefs.SecureFile
	}
	if err := validate(file, opened); err == nil {
		return file.Close()
	}
	return errors.Join(secure(file), file.Close())
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
	return boundedio.WithRegularFileSnapshot(path, auditHeadMaxBytes, func(file *os.File, info os.FileInfo) error {
		return privatefs.ValidateFileAllowLinks(file, info)
	})
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
		if err := boundedio.WithRegularFileSnapshot(source, DefaultMaxSizeBytes, func(file *os.File, info os.FileInfo) error {
			return security.ValidateOpenedJSONLFile(file, info, DefaultMaxSizeBytes)
		}); err != nil {
			return fmt.Errorf("validate audit evidence %s: %w", source, err)
		}
	}
	return validateAuditHead(filepath.Join(filepath.Dir(path), "audit.head.json"))
}

func auditLayoutForRead(repoRoot string) (jsonl.Layout, error) {
	return prepareAuditLayout(repoRoot)
}
