package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/schema"
)

const (
	repositorySyncTransactionRelativePath = ".reconc/repository-sync-transaction.json"
	repositorySyncTransactionFormat       = "reconc.repository-sync-transaction/v1"
	maxRepositorySyncTransactionBytes     = 96 << 20
)

var errRepositorySyncInterrupted = errors.New("injected repository sync interruption")

type syncMutation struct {
	Path    string
	Mode    uint32
	After   []byte
	Created bool
}

type repositorySyncTransaction struct {
	FormatVersion      string                      `json:"format_version"`
	RepoRoot           string                      `json:"repo_root"`
	ProductVersion     string                      `json:"product_version"`
	VerifyRunningBuild bool                        `json:"verify_running_build"`
	PlanDigest         string                      `json:"plan_digest"`
	Files              []repositorySyncJournalFile `json:"files"`
	CreatedDirectories []string                    `json:"created_directories"`
	JournalDigest      string                      `json:"journal_digest"`
}

type repositorySyncJournalFile struct {
	Path         string `json:"path"`
	Before       []byte `json:"before,omitempty"`
	BeforeMode   uint32 `json:"before_mode"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterMode    uint32 `json:"after_mode"`
	AfterSHA256  string `json:"after_sha256"`
	Created      bool   `json:"created"`
}

type syncTransactionState int

const (
	syncTransactionBefore syncTransactionState = iota
	syncTransactionAfter
	syncTransactionMixed
)

func RecoverRepositorySync(repoRoot string) (*SyncRecovery, error) {
	root, err := canonicalRepoRoot(repoRoot)
	report := &SyncRecovery{
		Schema: schema.Resolve(schema.RepositorySyncReport), FormatVersion: SyncRecoveryFormatVersion,
		Status: SyncRecoveryClean, Restored: []string{}, Verification: []Check{},
	}
	if err != nil {
		report.Status = SyncRecoveryRefused
		report.NextAction = err.Error()
		return report, err
	}
	report.RepoRoot = root
	err = withRepositoryTransactionLock(root, func() error {
		path, pathErr := repositorySyncTransactionPath(root)
		if pathErr != nil {
			return pathErr
		}
		if _, lstatErr := os.Lstat(path); os.IsNotExist(lstatErr) {
			report.NextAction = "No repository sync recovery is required."
			return nil
		} else if lstatErr != nil {
			return fmt.Errorf("inspect repository sync transaction journal: %w", lstatErr)
		}
		transaction, loadErr := loadRepositorySyncTransaction(root)
		if loadErr != nil {
			return loadErr
		}
		report.PlanDigest = transaction.PlanDigest
		state, stateErr := inspectRepositorySyncTransactionState(root, transaction)
		if stateErr != nil {
			return stateErr
		}
		if state == syncTransactionAfter {
			expectedProductVersion := ""
			if transaction.VerifyRunningBuild {
				expectedProductVersion = transaction.ProductVersion
			}
			verification, verifyErr := verifyRepository(root, expectedProductVersion, true)
			if verifyErr != nil {
				return verifyErr
			}
			report.Verification = append(report.Verification, verification.Checks...)
			if verification.Valid {
				if removeErr := removeRepositorySyncTransaction(root); removeErr != nil {
					return removeErr
				}
				report.Status = SyncRecoveryFinalized
				if transaction.VerifyRunningBuild {
					report.NextAction = "reconc check " + quoteBootstrapArgument(root)
				} else {
					report.NextAction = "reconc repo sync plan " + quoteBootstrapArgument(root)
				}
				return nil
			}
		}
		restored, rollbackErr := rollbackRepositorySyncTransaction(root, transaction)
		report.Restored = append(report.Restored, restored...)
		if rollbackErr != nil {
			return rollbackErr
		}
		report.Status = SyncRecoveryRolledBack
		report.NextAction = "reconc repo sync plan " + quoteBootstrapArgument(root)
		return nil
	})
	if err != nil {
		report.Status = SyncRecoveryRefused
		report.NextAction = err.Error()
	}
	return report, err
}

func ensureNoPendingRepositorySync(root string) error {
	path, err := repositorySyncTransactionPath(root)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect repository sync transaction journal: %w", err)
	}
	return fmt.Errorf(
		"pending repository sync transaction exists; run `reconc repo sync recover %s`",
		quoteBootstrapArgument(root),
	)
}

func buildRepositorySyncTransaction(
	root string,
	productVersion string,
	planDigest string,
	mutations []syncMutation,
	verifyRunningBuild bool,
) (*repositorySyncTransaction, error) {
	if strings.TrimSpace(productVersion) == "" || !validSHA256(planDigest) {
		return nil, fmt.Errorf("repository sync transaction identity is invalid")
	}
	transaction := &repositorySyncTransaction{
		FormatVersion: repositorySyncTransactionFormat,
		RepoRoot:      root, ProductVersion: productVersion, VerifyRunningBuild: verifyRunningBuild, PlanDigest: planDigest,
		Files: []repositorySyncJournalFile{}, CreatedDirectories: []string{},
	}
	totalBeforeBytes := 0
	seen := map[string]bool{}
	for _, mutation := range mutations {
		if seen[mutation.Path] {
			return nil, fmt.Errorf("repository sync transaction repeats path %s", mutation.Path)
		}
		seen[mutation.Path] = true
		if !validRepositoryRelativePath(mutation.Path) ||
			(mutation.Mode != 0o644 && mutation.Mode != 0o755) ||
			len(mutation.After) == 0 {
			return nil, fmt.Errorf("repository sync mutation is invalid: %s", mutation.Path)
		}
		target, err := safeRepositorySyncPath(root, mutation.Path)
		if err != nil {
			return nil, err
		}
		file := repositorySyncJournalFile{
			Path: mutation.Path, BeforeMode: mutation.Mode,
			BeforeSHA256: bytesSHA256(nil), AfterMode: mutation.Mode,
			AfterSHA256: bytesSHA256(mutation.After), Created: mutation.Created,
		}
		info, lstatErr := os.Lstat(target)
		if mutation.Created {
			if lstatErr == nil {
				return nil, fmt.Errorf("repository sync create target appeared before journaling: %s", mutation.Path)
			}
			if !os.IsNotExist(lstatErr) {
				return nil, fmt.Errorf("inspect repository sync create target %s: %w", mutation.Path, lstatErr)
			}
		} else {
			if lstatErr != nil {
				return nil, fmt.Errorf("inspect repository sync source %s: %w", mutation.Path, lstatErr)
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("repository sync source is not a real regular file: %s", mutation.Path)
			}
			before, err := boundedio.ReadRegularFile(target, maxSyncRollbackBytes)
			if err != nil {
				return nil, fmt.Errorf("read repository sync source %s: %w", mutation.Path, err)
			}
			totalBeforeBytes += len(before)
			if totalBeforeBytes > maxSyncRollbackBytes {
				return nil, fmt.Errorf("repository sync rollback set exceeds %d bytes", maxSyncRollbackBytes)
			}
			file.Before = before
			file.BeforeMode = uint32(info.Mode().Perm())
			file.BeforeSHA256 = bytesSHA256(before)
		}
		transaction.Files = append(transaction.Files, file)
		directories, err := missingRepositorySyncDirectories(root, filepath.Dir(target))
		if err != nil {
			return nil, err
		}
		transaction.CreatedDirectories = append(transaction.CreatedDirectories, directories...)
	}
	transaction.CreatedDirectories = uniqueSortedRepositoryPaths(transaction.CreatedDirectories)
	journalDigest, err := computeRepositorySyncTransactionDigest(transaction)
	if err != nil {
		return nil, err
	}
	transaction.JournalDigest = journalDigest
	if err := validateRepositorySyncTransaction(transaction); err != nil {
		return nil, err
	}
	return transaction, nil
}

func publishRepositorySyncTransaction(
	root string,
	transaction *repositorySyncTransaction,
	mutations []syncMutation,
	failAfter int,
	interruptAfter int,
) error {
	if err := validateRepositorySyncTransaction(transaction); err != nil {
		return err
	}
	if len(mutations) != len(transaction.Files) {
		return fmt.Errorf("repository sync mutation count changed after journaling")
	}
	if err := writeRepositorySyncTransaction(root, transaction); err != nil {
		return err
	}
	for index, mutation := range mutations {
		journalFile := transaction.Files[index]
		if journalFile.Path != mutation.Path ||
			journalFile.AfterSHA256 != bytesSHA256(mutation.After) ||
			journalFile.AfterMode != mutation.Mode ||
			journalFile.Created != mutation.Created {
			return fmt.Errorf("repository sync mutation drifted after journaling: %s", mutation.Path)
		}
		if err := publishRepositorySyncMutation(root, transaction.PlanDigest, mutation, journalFile); err != nil {
			return err
		}
		published := index + 1
		if interruptAfter > 0 && published >= interruptAfter {
			return fmt.Errorf("%w after %d artifacts", errRepositorySyncInterrupted, published)
		}
		if failAfter > 0 && published >= failAfter {
			return fmt.Errorf("injected repository sync failure after %d artifacts", published)
		}
	}
	return nil
}

func publishRepositorySyncMutation(
	root string,
	planDigest string,
	mutation syncMutation,
	journalFile repositorySyncJournalFile,
) error {
	target, err := safeRepositorySyncPath(root, mutation.Path)
	if err != nil {
		return err
	}
	if mutation.Created {
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			if err == nil {
				err = fmt.Errorf("target already exists")
			}
			return fmt.Errorf("repository sync create precondition failed for %s: %w", mutation.Path, err)
		}
		artifact := desiredArtifact{
			component: "repository-sync", path: mutation.Path,
			mode: mutation.Mode, content: mutation.After,
		}
		record, directories, err := publishArtifact(
			root, artifact, mutation.Path, journalFile.AfterSHA256, planDigest,
		)
		closeCreatedDirectoryIdentities(directories)
		if err != nil {
			_ = record.close()
			return err
		}
		verifyErr := verifyRepositorySyncMutation(target, journalFile.AfterSHA256, journalFile.AfterMode)
		return errors.Join(verifyErr, record.close())
	}
	info, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("inspect repository sync precondition %s: %w", mutation.Path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("repository sync precondition is not a real regular file: %s", mutation.Path)
	}
	current, err := boundedio.ReadRegularFile(target, maxSyncRollbackBytes)
	if err != nil {
		return fmt.Errorf("read repository sync precondition %s: %w", mutation.Path, err)
	}
	if bytesSHA256(current) != journalFile.BeforeSHA256 ||
		uint32(info.Mode().Perm()) != journalFile.BeforeMode {
		return fmt.Errorf("repository sync precondition changed after journaling: %s", mutation.Path)
	}
	if _, err := atomicfile.WriteIfChanged(target, mutation.After, os.FileMode(mutation.Mode)); err != nil {
		return fmt.Errorf("publish repository sync artifact %s: %w", mutation.Path, err)
	}
	return verifyRepositorySyncMutation(target, journalFile.AfterSHA256, journalFile.AfterMode)
}

func verifyRepositorySyncMutation(target, expectedSHA string, expectedMode uint32) error {
	info, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("inspect published repository sync artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		!modeSatisfies(info.Mode(), expectedMode) {
		return fmt.Errorf("published repository sync artifact has an invalid type or mode: %s", target)
	}
	body, err := boundedio.ReadRegularFile(target, maxBinaryBytes)
	if err != nil {
		return fmt.Errorf("read published repository sync artifact: %w", err)
	}
	if bytesSHA256(body) != expectedSHA {
		return fmt.Errorf("published repository sync artifact checksum mismatch: %s", target)
	}
	return nil
}

func writeRepositorySyncTransaction(root string, transaction *repositorySyncTransaction) error {
	if err := ensureNoPendingRepositorySync(root); err != nil {
		return err
	}
	body, err := encodeRepositorySyncTransaction(transaction)
	if err != nil {
		return err
	}
	path, err := repositorySyncTransactionPath(root)
	if err != nil {
		return err
	}
	if _, err := atomicfile.WritePrivateIfChanged(path, body, 0o600); err != nil {
		return fmt.Errorf("publish repository sync transaction journal: %w", err)
	}
	return nil
}

func loadRepositorySyncTransaction(root string) (*repositorySyncTransaction, error) {
	path, err := repositorySyncTransactionPath(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect repository sync transaction journal: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxRepositorySyncTransactionBytes {
		return nil, fmt.Errorf("repository sync transaction journal must be a bounded real regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open repository sync transaction journal: %w", err)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxRepositorySyncTransactionBytes+1))
	decoder.DisallowUnknownFields()
	var transaction repositorySyncTransaction
	decodeErr := decoder.Decode(&transaction)
	var extra interface{}
	extraErr := decoder.Decode(&extra)
	closeErr := file.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("decode repository sync transaction journal: %w", decodeErr)
	}
	if extraErr != io.EOF {
		return nil, fmt.Errorf("repository sync transaction journal must contain exactly one JSON document")
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close repository sync transaction journal: %w", closeErr)
	}
	if err := validateRepositorySyncTransaction(&transaction); err != nil {
		return nil, err
	}
	if transaction.RepoRoot != root {
		return nil, fmt.Errorf("repository sync transaction journal belongs to a different repository")
	}
	return &transaction, nil
}

func encodeRepositorySyncTransaction(transaction *repositorySyncTransaction) ([]byte, error) {
	if err := validateRepositorySyncTransaction(transaction); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(transaction, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode repository sync transaction journal: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxRepositorySyncTransactionBytes {
		return nil, fmt.Errorf("repository sync transaction journal exceeds %d bytes", maxRepositorySyncTransactionBytes)
	}
	return body, nil
}

func validateRepositorySyncTransaction(transaction *repositorySyncTransaction) error {
	if transaction == nil || transaction.FormatVersion != repositorySyncTransactionFormat ||
		strings.TrimSpace(transaction.RepoRoot) == "" ||
		strings.TrimSpace(transaction.ProductVersion) == "" ||
		!validSHA256(transaction.PlanDigest) || !validSHA256(transaction.JournalDigest) ||
		len(transaction.Files) == 0 {
		return fmt.Errorf("repository sync transaction journal identity is invalid")
	}
	seen := map[string]bool{}
	for _, file := range transaction.Files {
		if seen[file.Path] || !validRepositoryRelativePath(file.Path) ||
			file.BeforeMode == 0 || file.BeforeMode > 0o777 ||
			(file.AfterMode != 0o644 && file.AfterMode != 0o755) ||
			!validSHA256(file.BeforeSHA256) || !validSHA256(file.AfterSHA256) {
			return fmt.Errorf("repository sync transaction journal file is invalid: %s", file.Path)
		}
		seen[file.Path] = true
		if bytesSHA256(file.Before) != file.BeforeSHA256 {
			return fmt.Errorf("repository sync transaction before image checksum mismatch: %s", file.Path)
		}
		if file.Created && len(file.Before) != 0 {
			return fmt.Errorf("repository sync created file has a before image: %s", file.Path)
		}
	}
	if err := validateSortedPaths(transaction.CreatedDirectories, "repository sync created directory"); err != nil {
		return err
	}
	digest, err := computeRepositorySyncTransactionDigest(transaction)
	if err != nil {
		return err
	}
	if digest != transaction.JournalDigest {
		return fmt.Errorf("repository sync transaction journal digest mismatch")
	}
	return nil
}

func computeRepositorySyncTransactionDigest(transaction *repositorySyncTransaction) (string, error) {
	if transaction == nil {
		return "", fmt.Errorf("repository sync transaction journal is nil")
	}
	copy := *transaction
	copy.JournalDigest = ""
	body, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("encode repository sync transaction digest: %w", err)
	}
	return bytesSHA256(body), nil
}

func inspectRepositorySyncTransactionState(
	root string,
	transaction *repositorySyncTransaction,
) (syncTransactionState, error) {
	beforeCount := 0
	afterCount := 0
	for _, file := range transaction.Files {
		target, err := safeRepositorySyncPath(root, file.Path)
		if err != nil {
			return syncTransactionMixed, err
		}
		info, lstatErr := os.Lstat(target)
		if file.Created && os.IsNotExist(lstatErr) {
			beforeCount++
			continue
		}
		if lstatErr != nil {
			return syncTransactionMixed, fmt.Errorf("recovery conflict: inspect %s: %w", file.Path, lstatErr)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return syncTransactionMixed, fmt.Errorf("recovery conflict: %s is not a real regular file", file.Path)
		}
		body, err := boundedio.ReadRegularFile(target, maxBinaryBytes)
		if err != nil {
			return syncTransactionMixed, fmt.Errorf("recovery conflict: read %s: %w", file.Path, err)
		}
		digest := bytesSHA256(body)
		mode := uint32(info.Mode().Perm())
		switch {
		case !file.Created && digest == file.BeforeSHA256 && mode == file.BeforeMode:
			beforeCount++
		case digest == file.AfterSHA256 && modeSatisfies(info.Mode(), file.AfterMode):
			afterCount++
		default:
			return syncTransactionMixed, fmt.Errorf(
				"recovery conflict: %s is neither the recorded before nor after image; refusing to overwrite it",
				file.Path,
			)
		}
	}
	switch {
	case beforeCount == len(transaction.Files):
		return syncTransactionBefore, nil
	case afterCount == len(transaction.Files):
		return syncTransactionAfter, nil
	default:
		return syncTransactionMixed, nil
	}
}

func rollbackRepositorySyncTransaction(
	root string,
	transaction *repositorySyncTransaction,
) ([]string, error) {
	if _, err := inspectRepositorySyncTransactionState(root, transaction); err != nil {
		return nil, err
	}
	restored := []string{}
	for index := len(transaction.Files) - 1; index >= 0; index-- {
		file := transaction.Files[index]
		target, err := safeRepositorySyncPath(root, file.Path)
		if err != nil {
			return restored, err
		}
		if file.Created {
			info, lstatErr := os.Lstat(target)
			if os.IsNotExist(lstatErr) {
				continue
			}
			if lstatErr != nil {
				return restored, fmt.Errorf("inspect interrupted repository sync artifact %s: %w", file.Path, lstatErr)
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return restored, fmt.Errorf("refuse rollback after concurrent change: %s is not a real regular file", file.Path)
			}
			body, readErr := boundedio.ReadRegularFile(target, maxBinaryBytes)
			if readErr != nil {
				return restored, fmt.Errorf("read interrupted repository sync artifact %s: %w", file.Path, readErr)
			}
			if bytesSHA256(body) != file.AfterSHA256 || !modeSatisfies(info.Mode(), file.AfterMode) {
				return restored, fmt.Errorf("refuse rollback after concurrent change: %s", file.Path)
			}
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return restored, fmt.Errorf("remove interrupted repository sync artifact %s: %w", file.Path, err)
			}
			restored = append(restored, file.Path)
			continue
		}
		info, lstatErr := os.Lstat(target)
		if lstatErr != nil {
			return restored, fmt.Errorf("inspect interrupted repository sync artifact %s: %w", file.Path, lstatErr)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return restored, fmt.Errorf("refuse rollback after concurrent change: %s is not a real regular file", file.Path)
		}
		body, err := boundedio.ReadRegularFile(target, maxBinaryBytes)
		if err != nil {
			return restored, fmt.Errorf("read interrupted repository sync artifact %s: %w", file.Path, err)
		}
		if bytesSHA256(body) == file.BeforeSHA256 && uint32(info.Mode().Perm()) == file.BeforeMode {
			continue
		}
		if bytesSHA256(body) != file.AfterSHA256 || !modeSatisfies(info.Mode(), file.AfterMode) {
			return restored, fmt.Errorf("refuse rollback after concurrent change: %s", file.Path)
		}
		if _, err := atomicfile.WriteIfChanged(target, file.Before, os.FileMode(file.BeforeMode)); err != nil {
			return restored, fmt.Errorf("restore interrupted repository sync artifact %s: %w", file.Path, err)
		}
		restored = append(restored, file.Path)
	}
	// Directory identity cannot be proven across a process crash. Preserve
	// journal-created empty directories rather than risk deleting a same-path
	// directory that another process created after the interruption.
	state, err := inspectRepositorySyncTransactionState(root, transaction)
	if err != nil {
		return restored, err
	}
	if state != syncTransactionBefore {
		return restored, fmt.Errorf(
			"repository sync state changed during rollback; recovery journal was preserved",
		)
	}
	sort.Strings(restored)
	if err := removeRepositorySyncTransaction(root); err != nil {
		return restored, err
	}
	return restored, nil
}

func removeRepositorySyncTransaction(root string) error {
	path, err := repositorySyncTransactionPath(root)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove repository sync transaction journal: %w", err)
	}
	return nil
}

func repositorySyncTransactionPath(root string) (string, error) {
	return safeRepositorySyncPath(root, repositorySyncTransactionRelativePath)
}

func safeRepositorySyncPath(root, relative string) (string, error) {
	target, err := safeBootstrapTarget(root, relative)
	if err != nil {
		return "", err
	}
	relativeParent, err := filepath.Rel(root, filepath.Dir(target))
	if err != nil {
		return "", fmt.Errorf("resolve repository sync parent: %w", err)
	}
	current := root
	if relativeParent != "." {
		for _, part := range strings.Split(relativeParent, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, lstatErr := os.Lstat(current)
			if os.IsNotExist(lstatErr) {
				break
			}
			if lstatErr != nil {
				return "", fmt.Errorf("inspect repository sync path component %s: %w", current, lstatErr)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("repository sync path component is not a real directory: %s", current)
			}
		}
	}
	return target, nil
}

func missingRepositorySyncDirectories(root, parent string) ([]string, error) {
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("repository sync parent escapes repository: %s", parent)
	}
	if relative == "." {
		return []string{}, nil
	}
	missing := []string{}
	current := root
	absent := false
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if absent {
			relativePath, _ := filepath.Rel(root, current)
			missing = append(missing, filepath.ToSlash(relativePath))
			continue
		}
		info, lstatErr := os.Lstat(current)
		if os.IsNotExist(lstatErr) {
			absent = true
			relativePath, _ := filepath.Rel(root, current)
			missing = append(missing, filepath.ToSlash(relativePath))
			continue
		}
		if lstatErr != nil {
			return nil, fmt.Errorf("inspect repository sync parent %s: %w", current, lstatErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("repository sync parent is not a real directory: %s", current)
		}
	}
	return missing, nil
}

func uniqueSortedRepositoryPaths(values []string) []string {
	sort.Strings(values)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
