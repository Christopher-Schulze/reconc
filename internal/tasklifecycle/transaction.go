package tasklifecycle

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
)

const (
	transactionRel                 = ".reconc/task-transaction.json"
	taskLockRel                    = ".reconc/locks/task-lifecycle.lock"
	transactionVersion             = 2
	legacyTransactionVersion       = 1
	maxTransactionBytes            = 4 << 20
	maxTransactionDirectoryEntries = 1024
	transactionPhasePrepared       = "prepared"
	transactionPhaseCommitted      = "committed"
	transactionDirectoryMarker     = ".reconc-task-transaction-owner"
)

type fileMutation struct {
	Path   string
	After  []byte
	Create bool
}

type moveMutation struct {
	Source      string
	Destination string
}

type transaction struct {
	FormatVersion      int                    `json:"format_version"`
	Action             string                 `json:"action"`
	Phase              string                 `json:"phase,omitempty"`
	Files              []transactionFile      `json:"files"`
	Moves              []transactionMove      `json:"moves,omitempty"`
	CreatedDirectories []transactionDirectory `json:"created_directories,omitempty"`
	lockLease          *taskMutationLockLease
}

func (journal transaction) validateLockLease() error {
	if journal.lockLease == nil {
		return nil
	}
	return journal.lockLease.validate()
}

type transactionFile struct {
	Path       string `json:"path"`
	Before     []byte `json:"before"`
	BeforeMode uint32 `json:"before_mode"`
	BeforeHash string `json:"before_hash"`
	AfterHash  string `json:"after_hash"`
	Created    bool   `json:"created,omitempty"`
}

type transactionMove struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	BeforeHash  string `json:"before_hash"`
	AfterHash   string `json:"after_hash"`
}

type transactionDirectory struct {
	Path        string `json:"path"`
	MarkerToken string `json:"marker_token"`
}

var (
	acquireMutationLock = filelock.LockContext
	closeMutationLock   = func(file *os.File) error { return file.Close() }
)

type taskMutationLockLease struct {
	repoRoot            string
	reconcPath          string
	lockDirectoryPath   string
	lockPath            string
	lockName            string
	repository          *os.Root
	reconcDirectory     *os.Root
	lockDirectory       *os.Root
	repositoryInfo      os.FileInfo
	reconcDirectoryInfo os.FileInfo
	lockDirectoryInfo   os.FileInfo
	lockInfo            os.FileInfo
	file                *os.File
}

func transactionExists(repoRoot string) (bool, error) {
	_, path, err := safeTransactionPath(repoRoot, transactionRel)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", transactionRel, err)
	}
	err = boundedio.WithRegularFileSnapshot(path, maxTransactionBytes, func(*os.File, os.FileInfo) error {
		return nil
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect %s: %w", transactionRel, err)
}

func withMutationLock(repoRoot string, fn func() error) (resultErr error) {
	if fn == nil {
		return errors.New("TASK mutation lock callback is required")
	}
	return withMutationLockLease(repoRoot, func(*taskMutationLockLease) error {
		return fn()
	})
}

func withMutationLockLease(repoRoot string, fn func(*taskMutationLockLease) error) (resultErr error) {
	if fn == nil {
		return errors.New("TASK mutation lock callback is required")
	}
	lease, err := openTaskMutationLock(repoRoot)
	if err != nil {
		return err
	}
	closeRoots := func(cause error) error {
		return errors.Join(cause, lease.closeRoots())
	}
	unlock, err := acquireMutationLock(context.Background(), lease.file, filelock.DefaultTimeout)
	if err != nil {
		return closeRoots(errors.Join(fmt.Errorf("lock TASK lifecycle: %w", err), closeMutationLock(lease.file)))
	}
	if err := lease.validate(); err != nil {
		return closeRoots(errors.Join(err, unlock(), closeMutationLock(lease.file)))
	}
	operationErr := fn(lease)
	leaseErr := lease.validate()
	unlockErr := unlock()
	closeErr := closeMutationLock(lease.file)
	if leaseErr != nil {
		operationErr = errors.Join(operationErr, fmt.Errorf("TASK lock lease changed: %w", leaseErr))
	}
	return errors.Join(operationErr, unlockErr, closeErr, lease.closeRoots())
}

func openTaskMutationLock(repoRoot string) (*taskMutationLockLease, error) {
	repository, err := os.OpenRoot(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("open TASK repository root: %w", err)
	}
	lease := &taskMutationLockLease{
		repoRoot:          repoRoot,
		reconcPath:        filepath.Join(repoRoot, ".reconc"),
		lockDirectoryPath: filepath.Join(repoRoot, filepath.FromSlash(filepath.Dir(taskLockRel))),
		lockPath:          filepath.Join(repoRoot, filepath.FromSlash(taskLockRel)),
		lockName:          filepath.Base(taskLockRel),
		repository:        repository,
	}
	closeOnError := func(cause error) (*taskMutationLockLease, error) {
		return nil, errors.Join(cause, lease.closeRoots())
	}
	if err := captureTaskMutationRootIdentity(lease); err != nil {
		return closeOnError(err)
	}
	if err := repository.Mkdir(".reconc", 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return closeOnError(fmt.Errorf("create TASK lock root directory: %w", err))
	}
	reconcInfo, err := repository.Lstat(".reconc")
	if err != nil || reconcInfo.Mode()&os.ModeSymlink != 0 || !reconcInfo.IsDir() {
		return closeOnError(errors.Join(fmt.Errorf("TASK lock parent must be a non-symlink directory"), err))
	}
	reconcDirectory, err := repository.OpenRoot(".reconc")
	if err != nil {
		return closeOnError(fmt.Errorf("open TASK lock root directory: %w", err))
	}
	lease.reconcDirectory = reconcDirectory
	lease.reconcDirectoryInfo = reconcInfo
	if err := validateTaskMutationDirectory(lease.reconcPath, reconcDirectory, reconcInfo); err != nil {
		return closeOnError(err)
	}
	if err := reconcDirectory.Mkdir("locks", 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return closeOnError(fmt.Errorf("create TASK lock directory: %w", err))
	}
	lockDirectoryInfo, err := reconcDirectory.Lstat("locks")
	if err != nil || lockDirectoryInfo.Mode()&os.ModeSymlink != 0 || !lockDirectoryInfo.IsDir() {
		return closeOnError(errors.Join(fmt.Errorf("TASK lock directory must be a non-symlink directory"), err))
	}
	lockDirectory, err := reconcDirectory.OpenRoot("locks")
	if err != nil {
		return closeOnError(fmt.Errorf("open TASK lock directory: %w", err))
	}
	lease.lockDirectory = lockDirectory
	lease.lockDirectoryInfo = lockDirectoryInfo
	if err := validateTaskMutationDirectory(lease.lockDirectoryPath, lockDirectory, lockDirectoryInfo); err != nil {
		return closeOnError(err)
	}
	before, err := lockDirectory.Lstat(lease.lockName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return closeOnError(fmt.Errorf("inspect TASK lock: %w", err))
	}
	if err == nil && (before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular()) {
		return closeOnError(fmt.Errorf("TASK lock must be a non-symlink regular file"))
	}
	if errors.Is(err, os.ErrNotExist) {
		before = nil
	}
	file, err := openTaskMutationLockFile(lockDirectory, lease.lockName, before)
	if err != nil {
		return closeOnError(err)
	}
	lease.file = file
	lockInfo, err := file.Stat()
	if err != nil {
		return closeOnError(errors.Join(fmt.Errorf("inspect opened TASK lock: %w", err), closeMutationLock(file)))
	}
	lease.lockInfo = lockInfo
	if err := lease.validate(); err != nil {
		return closeOnError(errors.Join(err, closeMutationLock(file)))
	}
	return lease, nil
}

func (lease *taskMutationLockLease) closeRoots() error {
	if lease == nil {
		return nil
	}
	return errors.Join(closeTaskRoot(lease.lockDirectory), closeTaskRoot(lease.reconcDirectory), closeTaskRoot(lease.repository))
}

func closeTaskRoot(root *os.Root) error {
	if root == nil {
		return nil
	}
	return root.Close()
}

func captureTaskMutationRootIdentity(lease *taskMutationLockLease) error {
	opened, statErr := lease.repository.Stat(".")
	current, lstatErr := os.Lstat(lease.repoRoot)
	if statErr != nil || lstatErr != nil || !opened.IsDir() || current.Mode()&os.ModeSymlink != 0 ||
		!current.IsDir() || !os.SameFile(opened, current) {
		return errors.Join(fmt.Errorf("TASK repository root changed identity while opening"), statErr, lstatErr)
	}
	lease.repositoryInfo = opened
	return nil
}

func validateTaskMutationDirectory(path string, root *os.Root, expected os.FileInfo) error {
	opened, statErr := root.Stat(".")
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || expected == nil || !opened.IsDir() || current.Mode()&os.ModeSymlink != 0 ||
		!current.IsDir() || !os.SameFile(expected, opened) || !os.SameFile(opened, current) {
		return errors.Join(fmt.Errorf("TASK lock directory changed identity: %s", path), statErr, lstatErr)
	}
	return nil
}

func (lease *taskMutationLockLease) validate() error {
	if lease == nil || lease.repository == nil || lease.reconcDirectory == nil || lease.lockDirectory == nil ||
		lease.file == nil || lease.lockInfo == nil {
		return errors.New("TASK lock lease is unavailable")
	}
	if err := validateTaskMutationDirectory(lease.repoRoot, lease.repository, lease.repositoryInfo); err != nil {
		return err
	}
	if err := validateTaskMutationDirectory(lease.reconcPath, lease.reconcDirectory, lease.reconcDirectoryInfo); err != nil {
		return err
	}
	if err := validateTaskMutationDirectory(lease.lockDirectoryPath, lease.lockDirectory, lease.lockDirectoryInfo); err != nil {
		return err
	}
	opened, statErr := lease.file.Stat()
	current, lstatErr := lease.lockDirectory.Lstat(lease.lockName)
	pathCurrent, pathErr := os.Lstat(lease.lockPath)
	if statErr != nil || lstatErr != nil || pathErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || pathCurrent.Mode()&os.ModeSymlink != 0 ||
		!pathCurrent.Mode().IsRegular() || !os.SameFile(lease.lockInfo, opened) || !os.SameFile(opened, current) ||
		!os.SameFile(opened, pathCurrent) {
		return errors.Join(fmt.Errorf("TASK lock path changed identity: %s", lease.lockPath), statErr, lstatErr, pathErr)
	}
	return nil
}

func validateTaskMutationLease(lease *taskMutationLockLease) error {
	if lease == nil {
		return nil
	}
	return lease.validate()
}

func removeTransactionPathWithLease(path string, lease *taskMutationLockLease) error {
	if err := validateTaskMutationLease(lease); err != nil {
		return err
	}
	return os.Remove(path)
}

func openTaskMutationLockFile(directory *os.Root, name string, before os.FileInfo) (*os.File, error) {
	flags := os.O_RDWR
	if before == nil {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := directory.OpenFile(name, flags, 0o644)
	if before == nil && errors.Is(err, os.ErrExist) {
		before, err = directory.Lstat(name)
		if err == nil && (before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular()) {
			return nil, fmt.Errorf("TASK lock must be a non-symlink regular file")
		}
		if err == nil {
			file, err = directory.OpenFile(name, os.O_RDWR, 0)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("open TASK lock: %w", err)
	}
	opened, statErr := file.Stat()
	current, lstatErr := directory.Lstat(name)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(opened, current) || before != nil && !os.SameFile(before, opened) {
		return nil, errors.Join(fmt.Errorf("TASK lock changed identity while opening"), statErr, lstatErr, file.Close())
	}
	return file, nil
}

func applyTransaction(repoRoot, action string, files []fileMutation, moves []moveMutation) error {
	return applyTransactionWithLease(repoRoot, action, files, moves, nil)
}

func applyTransactionWithLease(repoRoot, action string, files []fileMutation, moves []moveMutation, lease *taskMutationLockLease) error {
	journal, err := buildTransaction(repoRoot, action, files, moves)
	if err != nil {
		return err
	}
	if err := validateTransactionShape(journal); err != nil {
		return err
	}
	journal.lockLease = lease
	body, err := encodeTransaction(journal)
	if err != nil {
		return err
	}
	_, journalPath, err := safeTransactionPath(repoRoot, transactionRel)
	if err != nil {
		return err
	}
	if _, _, err := boundedio.ReadRegularFileSnapshot(journalPath, maxTransactionBytes); err == nil {
		return fmt.Errorf("pending %s exists; run `reconc task recover %s`", transactionRel, repoRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect TASK transaction: %w", err)
	}
	if err := journal.validateLockLease(); err != nil {
		return err
	}
	if _, err := atomicfile.WritePrivateIfChanged(journalPath, body, 0o600); err != nil {
		return fmt.Errorf("publish TASK transaction: %w", err)
	}
	if err := publishTransactionWithLease(repoRoot, journal, files, moves, lease); err != nil {
		rollbackErr := rollbackTransactionWithLease(repoRoot, journal, lease)
		if rollbackErr != nil {
			return fmt.Errorf("publish TASK transaction: %w; automatic rollback failed: %v; run `reconc task recover %s`", err, rollbackErr, repoRoot)
		}
		if guardErr := journal.validateLockLease(); guardErr != nil {
			return fmt.Errorf("publish TASK transaction: %w (rolled back); lock lease changed: %v; run `reconc task recover %s`", err, guardErr, repoRoot)
		}
		if removeErr := os.Remove(journalPath); removeErr != nil {
			return fmt.Errorf("publish TASK transaction: %w (rolled back); journal cleanup failed: %v; run `reconc task recover %s`", err, removeErr, repoRoot)
		}
		return fmt.Errorf("publish TASK transaction: %w (rolled back)", err)
	}
	committed := journal
	committed.Phase = transactionPhaseCommitted
	committedBody, err := encodeTransaction(committed)
	if err != nil {
		rollbackErr := rollbackTransactionWithLease(repoRoot, journal, lease)
		return errors.Join(err, rollbackErr)
	}
	if err := committed.validateLockLease(); err != nil {
		return err
	}
	if _, err := atomicfile.WritePrivateIfChanged(journalPath, committedBody, 0o600); err != nil {
		observed, readErr := readTransaction(repoRoot)
		if readErr != nil || observed.Phase == transactionPhaseCommitted {
			return errors.Join(
				fmt.Errorf("TASK transaction publication completed but commit-state durability is uncertain: %w; run `reconc task recover %s`", err, repoRoot),
				readErr,
			)
		}
		rollbackErr := rollbackTransactionWithLease(repoRoot, journal, lease)
		if rollbackErr != nil {
			return fmt.Errorf("mark TASK transaction committed: %w; automatic rollback failed: %v; run `reconc task recover %s`", err, rollbackErr, repoRoot)
		}
		return fmt.Errorf("mark TASK transaction committed: %w (rolled back)", err)
	}
	if err := cleanupCommittedDirectoryMarkersWithLease(repoRoot, committed.CreatedDirectories, lease); err != nil {
		return fmt.Errorf("TASK transaction committed but directory-marker cleanup failed: %w; run `reconc task recover %s`", err, repoRoot)
	}
	if err := committed.validateLockLease(); err != nil {
		return err
	}
	if err := os.Remove(journalPath); err != nil {
		return fmt.Errorf("TASK transaction committed but journal cleanup failed: %w; run `reconc task recover %s`", err, repoRoot)
	}
	return nil
}

func encodeTransaction(journal transaction) ([]byte, error) {
	body, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal TASK transaction: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxTransactionBytes {
		return nil, fmt.Errorf("TASK transaction journal is %d bytes; maximum is %d", len(body), maxTransactionBytes)
	}
	return body, nil
}

func buildTransaction(repoRoot, action string, files []fileMutation, moves []moveMutation) (transaction, error) {
	journal := transaction{FormatVersion: transactionVersion, Action: action, Phase: transactionPhasePrepared}
	transactionFiles, afterByPath, err := buildTransactionFiles(repoRoot, files)
	if err != nil {
		return transaction{}, err
	}
	journal.Files = transactionFiles
	filesByPath := make(map[string]transactionFile, len(transactionFiles))
	for _, file := range transactionFiles {
		filesByPath[file.Path] = file
	}
	for _, move := range moves {
		transactionMove, sourceFile, err := buildTransactionMove(repoRoot, move, afterByPath)
		if err != nil {
			return transaction{}, err
		}
		if recorded, ok := filesByPath[sourceFile.Path]; ok {
			if recorded.BeforeHash != sourceFile.BeforeHash || recorded.BeforeMode != sourceFile.BeforeMode {
				return transaction{}, fmt.Errorf("transaction source %s changed while preparing the journal", sourceFile.Path)
			}
		} else {
			journal.Files = append(journal.Files, sourceFile)
			filesByPath[sourceFile.Path] = sourceFile
		}
		journal.Moves = append(journal.Moves, transactionMove)
	}
	targets := make([]string, 0, len(files)+len(moves))
	for _, change := range files {
		if change.Create {
			targets = append(targets, change.Path)
		}
	}
	for _, move := range moves {
		targets = append(targets, move.Destination)
	}
	journal.CreatedDirectories, err = planTransactionDirectories(repoRoot, targets)
	if err != nil {
		return transaction{}, err
	}
	return journal, nil
}

func planTransactionDirectories(repoRoot string, targets []string) ([]transactionDirectory, error) {
	missing := map[string]bool{}
	for _, target := range targets {
		_, absolute, err := safeTransactionPath(repoRoot, target)
		if err != nil {
			return nil, err
		}
		for directory := filepath.Dir(absolute); directory != repoRoot; directory = filepath.Dir(directory) {
			info, statErr := os.Lstat(directory)
			switch {
			case statErr == nil:
				if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
					return nil, fmt.Errorf("transaction parent is not a real directory: %s", directory)
				}
			case errors.Is(statErr, os.ErrNotExist):
				relative, relErr := filepath.Rel(repoRoot, directory)
				if relErr != nil {
					return nil, fmt.Errorf("resolve transaction parent %s: %w", directory, relErr)
				}
				missing[filepath.ToSlash(relative)] = true
			default:
				return nil, fmt.Errorf("inspect transaction parent %s: %w", directory, statErr)
			}
		}
	}
	paths := make([]string, 0, len(missing))
	for path := range missing {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(left, right int) bool {
		leftDepth := transactionPathDepth(paths[left])
		rightDepth := transactionPathDepth(paths[right])
		return leftDepth < rightDepth || leftDepth == rightDepth && paths[left] < paths[right]
	})
	directories := make([]transactionDirectory, 0, len(paths))
	for _, path := range paths {
		token, err := newTransactionDirectoryToken()
		if err != nil {
			return nil, err
		}
		directories = append(directories, transactionDirectory{Path: path, MarkerToken: token})
	}
	return directories, nil
}

func newTransactionDirectoryToken() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("create transaction directory ownership token: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
}

func transactionPathDepth(path string) int {
	return strings.Count(filepath.ToSlash(path), "/")
}

func buildTransactionFiles(repoRoot string, files []fileMutation) ([]transactionFile, map[string]string, error) {
	out := make([]transactionFile, 0, len(files))
	afterByPath := make(map[string]string, len(files))
	for _, change := range files {
		rel, abs, err := safeTransactionPath(repoRoot, change.Path)
		if err != nil {
			return nil, nil, err
		}
		if change.Create {
			if _, err := os.Lstat(abs); err == nil {
				return nil, nil, fmt.Errorf("transaction create target already exists: %s", rel)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, nil, fmt.Errorf("inspect transaction create target %s: %w", rel, err)
			}
			out = append(out, transactionFile{Path: rel, BeforeMode: 0o644, BeforeHash: hashContent(nil), AfterHash: hashContent(change.After), Created: true})
		} else {
			before, info, err := readRegularTransactionFile(abs)
			if err != nil {
				return nil, nil, fmt.Errorf("read transaction source %s: %w", rel, err)
			}
			out = append(out, transactionFile{Path: rel, Before: before, BeforeMode: uint32(info.Mode().Perm()), BeforeHash: hashContent(before), AfterHash: hashContent(change.After)})
		}
		afterByPath[rel] = hashContent(change.After)
	}
	return out, afterByPath, nil
}

func buildTransactionMove(repoRoot string, move moveMutation, afterByPath map[string]string) (transactionMove, transactionFile, error) {
	sourceRel, sourceAbs, err := safeTransactionPath(repoRoot, move.Source)
	if err != nil {
		return transactionMove{}, transactionFile{}, err
	}
	destinationRel, destinationAbs, err := safeTransactionPath(repoRoot, move.Destination)
	if err != nil {
		return transactionMove{}, transactionFile{}, err
	}
	body, info, err := readRegularTransactionFile(sourceAbs)
	if err != nil {
		return transactionMove{}, transactionFile{}, fmt.Errorf("read move source %s: %w", sourceRel, err)
	}
	if _, err := os.Lstat(destinationAbs); err == nil {
		return transactionMove{}, transactionFile{}, fmt.Errorf("move destination already exists: %s", destinationRel)
	} else if !errors.Is(err, os.ErrNotExist) {
		return transactionMove{}, transactionFile{}, fmt.Errorf("inspect move destination %s: %w", destinationRel, err)
	}
	beforeHash := hashContent(body)
	afterHash := beforeHash
	if mutatedHash, ok := afterByPath[sourceRel]; ok {
		afterHash = mutatedHash
	}
	sourceFile := transactionFile{
		Path:       sourceRel,
		Before:     body,
		BeforeMode: uint32(info.Mode().Perm()),
		BeforeHash: beforeHash,
		AfterHash:  afterHash,
	}
	return transactionMove{
		Source: sourceRel, Destination: destinationRel,
		BeforeHash: beforeHash, AfterHash: afterHash,
	}, sourceFile, nil
}

func publishTransaction(repoRoot string, journal transaction, files []fileMutation, moves []moveMutation) error {
	return publishTransactionWithLease(repoRoot, journal, files, moves, journal.lockLease)
}

func publishTransactionWithLease(repoRoot string, journal transaction, files []fileMutation, moves []moveMutation, lease *taskMutationLockLease) error {
	journal.lockLease = lease
	if err := validateTransactionShape(journal); err != nil {
		return err
	}
	if err := validatePublishInputs(repoRoot, journal, files, moves); err != nil {
		return err
	}
	if err := validatePublishState(repoRoot, journal); err != nil {
		return err
	}
	if err := prepareTransactionDirectoriesWithLease(repoRoot, journal.CreatedDirectories, lease); err != nil {
		return err
	}
	if err := journal.validateLockLease(); err != nil {
		return err
	}
	filesByPath := transactionFilesByPath(journal.Files)
	for _, change := range files {
		rel, abs, err := safeTransactionPath(repoRoot, change.Path)
		if err != nil {
			return err
		}
		recorded := filesByPath[rel]
		if err := validateFilePublishPrecondition(repoRoot, recorded); err != nil {
			return err
		}
		if err := journal.validateLockLease(); err != nil {
			return err
		}
		mode := os.FileMode(recorded.BeforeMode)
		if recorded.Created {
			if err := writeNewTransactionFileWithLease(abs, change.After, mode, lease); err != nil {
				return err
			}
		} else {
			if err := validateTaskMutationLease(lease); err != nil {
				return err
			}
			if _, err := atomicfile.WriteIfChanged(abs, change.After, mode); err != nil {
				return err
			}
		}
	}
	for index, move := range moves {
		if err := publishTransactionMoveWithLease(repoRoot, journal.Moves[index], filesByPath, move, lease); err != nil {
			return err
		}
	}
	return nil
}

func prepareTransactionDirectories(repoRoot string, directories []transactionDirectory) error {
	return prepareTransactionDirectoriesWithLease(repoRoot, directories, nil)
}

func prepareTransactionDirectoriesWithLease(repoRoot string, directories []transactionDirectory, lease *taskMutationLockLease) error {
	for _, directory := range directories {
		_, absolute, err := safeTransactionPath(repoRoot, directory.Path)
		if err != nil {
			return err
		}
		if err := validateTaskMutationLease(lease); err != nil {
			return err
		}
		if err := os.Mkdir(absolute, 0o755); err != nil {
			return fmt.Errorf("create transaction directory %s: %w", directory.Path, err)
		}
		info, err := os.Lstat(absolute)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.Join(fmt.Errorf("validate created transaction directory %s", directory.Path), err)
		}
		marker, err := transactionDirectoryMarkerPath(repoRoot, directory)
		if err != nil {
			return err
		}
		if err := validateTaskMutationLease(lease); err != nil {
			return err
		}
		if _, err := atomicfile.WriteNew(marker, transactionDirectoryMarkerBody(directory), 0o600); err != nil {
			return errors.Join(
				fmt.Errorf("publish transaction directory marker for %s: %w", directory.Path, err),
				removeTransactionPathWithLease(absolute, lease),
			)
		}
		if err := validateTransactionDirectoryMarker(repoRoot, directory, false); err != nil {
			return err
		}
	}
	return nil
}

func transactionDirectoryMarkerPath(repoRoot string, directory transactionDirectory) (string, error) {
	marker := filepath.ToSlash(filepath.Join(filepath.FromSlash(directory.Path), transactionDirectoryMarker))
	_, absolute, err := safeTransactionPath(repoRoot, marker)
	return absolute, err
}

func transactionDirectoryMarkerBody(directory transactionDirectory) []byte {
	return []byte(directory.MarkerToken + "\n")
}

func validateTransactionDirectoryMarker(repoRoot string, directory transactionDirectory, allowMissing bool) error {
	marker, err := transactionDirectoryMarkerPath(repoRoot, directory)
	if err != nil {
		return err
	}
	body, err := boundedio.ReadRegularFile(marker, sha256.Size*2+1)
	if allowMissing && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("validate transaction directory marker for %s: %w", directory.Path, err)
	}
	if !bytes.Equal(body, transactionDirectoryMarkerBody(directory)) {
		return fmt.Errorf("transaction directory ownership marker changed for %s", directory.Path)
	}
	return nil
}

func validatePublishInputs(repoRoot string, journal transaction, files []fileMutation, moves []moveMutation) error {
	if len(moves) != len(journal.Moves) {
		return fmt.Errorf("transaction input has %d moves; journal records %d", len(moves), len(journal.Moves))
	}
	recordedFiles := transactionFilesByPath(journal.Files)
	changedPaths, err := validatePublishFileInputs(repoRoot, recordedFiles, files)
	if err != nil {
		return err
	}
	moveSources, err := validatePublishMoveInputs(repoRoot, journal.Moves, moves)
	if err != nil {
		return err
	}
	for _, recorded := range journal.Files {
		if !changedPaths[recorded.Path] && !moveSources[recorded.Path] {
			return fmt.Errorf("transaction journal records unrequested file %s", recorded.Path)
		}
	}
	targets := make([]string, 0, len(files)+len(moves))
	for _, change := range files {
		if change.Create {
			targets = append(targets, change.Path)
		}
	}
	for _, move := range moves {
		targets = append(targets, move.Destination)
	}
	expectedDirectories, err := planTransactionDirectories(repoRoot, targets)
	if err != nil {
		return err
	}
	if len(expectedDirectories) != len(journal.CreatedDirectories) {
		return fmt.Errorf("transaction directory precondition has %d missing entries; journal records %d", len(expectedDirectories), len(journal.CreatedDirectories))
	}
	for index := range expectedDirectories {
		if expectedDirectories[index].Path != journal.CreatedDirectories[index].Path {
			return fmt.Errorf("transaction directory precondition does not match journal at %s", journal.CreatedDirectories[index].Path)
		}
	}
	return nil
}

func validatePublishFileInputs(
	repoRoot string,
	recordedFiles map[string]transactionFile,
	files []fileMutation,
) (map[string]bool, error) {
	changedPaths := make(map[string]bool, len(files))
	for _, change := range files {
		rel, _, err := safeTransactionPath(repoRoot, change.Path)
		if err != nil {
			return nil, err
		}
		recorded, ok := recordedFiles[rel]
		if !ok || recorded.Created != change.Create || recorded.AfterHash != hashContent(change.After) {
			return nil, fmt.Errorf("transaction input does not match journaled file %s", rel)
		}
		if changedPaths[rel] {
			return nil, fmt.Errorf("transaction input repeats file %s", rel)
		}
		changedPaths[rel] = true
	}
	return changedPaths, nil
}

func validatePublishMoveInputs(
	repoRoot string,
	recordedMoves []transactionMove,
	moves []moveMutation,
) (map[string]bool, error) {
	moveSources := make(map[string]bool, len(moves))
	for index, move := range moves {
		source, _, err := safeTransactionPath(repoRoot, move.Source)
		if err != nil {
			return nil, err
		}
		destination, _, err := safeTransactionPath(repoRoot, move.Destination)
		if err != nil {
			return nil, err
		}
		recorded := recordedMoves[index]
		if recorded.Source != source || recorded.Destination != destination {
			return nil, fmt.Errorf("transaction input does not match journaled move %s -> %s", source, destination)
		}
		moveSources[source] = true
	}
	return moveSources, nil
}

func validatePublishState(repoRoot string, journal transaction) error {
	for _, file := range journal.Files {
		if err := validateFilePublishPrecondition(repoRoot, file); err != nil {
			return err
		}
	}
	for _, move := range journal.Moves {
		if err := validateMoveDestinationAbsent(repoRoot, move); err != nil {
			return err
		}
	}
	return nil
}

func validateFilePublishPrecondition(repoRoot string, file transactionFile) error {
	_, abs, err := safeTransactionPath(repoRoot, file.Path)
	if err != nil {
		return err
	}
	body, info, err := readRegularTransactionFile(abs)
	if file.Created {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("transaction create precondition for %s: %w", file.Path, err)
		}
		return fmt.Errorf("transaction create precondition failed: %s already exists", file.Path)
	}
	if err != nil {
		return fmt.Errorf("transaction file precondition for %s: %w", file.Path, err)
	}
	if hashContent(body) != file.BeforeHash {
		return fmt.Errorf("transaction file precondition failed: %s content changed", file.Path)
	}
	if !sameTransactionMode(info.Mode().Perm(), file.BeforeMode) {
		return fmt.Errorf(
			"transaction file mode precondition failed: %s is %04o, expected %04o",
			file.Path, info.Mode().Perm(), file.BeforeMode,
		)
	}
	return nil
}

func validateMoveDestinationAbsent(repoRoot string, move transactionMove) error {
	_, destination, err := safeTransactionPath(repoRoot, move.Destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("transaction move destination precondition failed: %s already exists", move.Destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("transaction move destination precondition for %s: %w", move.Destination, err)
	}
	return nil
}

func validateMovePublishPrecondition(repoRoot string, move transactionMove, sourceFile transactionFile) error {
	_, source, err := safeTransactionPath(repoRoot, move.Source)
	if err != nil {
		return err
	}
	body, info, err := readRegularTransactionFile(source)
	if err != nil {
		return fmt.Errorf("transaction move source precondition for %s: %w", move.Source, err)
	}
	if hashContent(body) != move.AfterHash {
		return fmt.Errorf("transaction move source precondition failed: %s content changed", move.Source)
	}
	if !sameTransactionMode(info.Mode().Perm(), sourceFile.BeforeMode) {
		return fmt.Errorf(
			"transaction move source mode precondition failed: %s is %04o, expected %04o",
			move.Source, info.Mode().Perm(), sourceFile.BeforeMode,
		)
	}
	return validateMoveDestinationAbsent(repoRoot, move)
}

func publishTransactionMoveWithLease(
	repoRoot string,
	recorded transactionMove,
	filesByPath map[string]transactionFile,
	requested moveMutation,
	lease *taskMutationLockLease,
) error {
	if err := validateTaskMutationLease(lease); err != nil {
		return err
	}
	sourceFile, ok := filesByPath[recorded.Source]
	if !ok {
		return fmt.Errorf("transaction move source %s has no before-image", recorded.Source)
	}
	if err := validateMovePublishPrecondition(repoRoot, recorded, sourceFile); err != nil {
		return err
	}
	_, destination, err := safeTransactionPath(repoRoot, requested.Destination)
	if err != nil {
		return err
	}
	_, source, err := safeTransactionPath(repoRoot, requested.Source)
	if err != nil {
		return err
	}
	if err := validateTaskMutationLease(lease); err != nil {
		return err
	}
	if err := os.Link(source, destination); err != nil {
		return fmt.Errorf("transaction move destination precondition failed for %s: %w", recorded.Destination, err)
	}
	if err := validateLinkedMove(source, destination, recorded, sourceFile); err != nil {
		removeErr := removeTransactionPathWithLease(destination, lease)
		return errors.Join(err, removeErr)
	}
	if err := removeTransactionPathWithLease(source, lease); err != nil {
		return fmt.Errorf("remove transaction move source %s: %w", recorded.Source, err)
	}
	return nil
}

func validateLinkedMove(source, destination string, move transactionMove, sourceFile transactionFile) error {
	sourceBody, sourceInfo, sourceErr := readRegularTransactionFile(source)
	destinationBody, destinationInfo, destinationErr := readRegularTransactionFile(destination)
	if sourceErr != nil || destinationErr != nil {
		return fmt.Errorf(
			"verify linked transaction move %s -> %s: source=%v destination=%v",
			move.Source, move.Destination, sourceErr, destinationErr,
		)
	}
	if !os.SameFile(sourceInfo, destinationInfo) {
		return fmt.Errorf("verify linked transaction move %s -> %s: paths do not identify the same file", move.Source, move.Destination)
	}
	if hashContent(sourceBody) != move.AfterHash || hashContent(destinationBody) != move.AfterHash {
		return fmt.Errorf("verify linked transaction move %s -> %s: content changed", move.Source, move.Destination)
	}
	if !sameTransactionMode(sourceInfo.Mode().Perm(), sourceFile.BeforeMode) ||
		!sameTransactionMode(destinationInfo.Mode().Perm(), sourceFile.BeforeMode) {
		return fmt.Errorf("verify linked transaction move %s -> %s: mode changed", move.Source, move.Destination)
	}
	return nil
}

func transactionFilesByPath(files []transactionFile) map[string]transactionFile {
	out := make(map[string]transactionFile, len(files))
	for _, file := range files {
		out[file.Path] = file
	}
	return out
}

func transactionFileByPath(files []transactionFile, path string) (transactionFile, bool) {
	for _, file := range files {
		if file.Path == path {
			return file, true
		}
	}
	return transactionFile{}, false
}

func writeNewTransactionFileWithLease(path string, body []byte, mode os.FileMode, lease *taskMutationLockLease) error {
	if err := validateTaskMutationLease(lease); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create transaction file %s: %w", path, err)
	}
	if err := validateTaskMutationLease(lease); err != nil {
		return errors.Join(err, file.Close())
	}
	written, writeErr := file.Write(body)
	if writeErr == nil && written != len(body) {
		writeErr = io.ErrShortWrite
	}
	if err := validateTaskMutationLease(lease); err != nil {
		writeErr = errors.Join(writeErr, err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		removeErr := removeTransactionPathWithLease(path, lease)
		return errors.Join(fmt.Errorf("publish transaction file %s: %w", path, err), removeErr)
	}
	return nil
}

func readRegularTransactionFile(path string) ([]byte, os.FileInfo, error) {
	initial, err := lstatRegularTransactionFile(path)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, initial, err
	}
	opened, statErr := file.Stat()
	if statErr != nil {
		return nil, initial, errors.Join(statErr, file.Close())
	}
	if !opened.Mode().IsRegular() || !os.SameFile(initial, opened) {
		return nil, opened, errors.Join(fmt.Errorf("%s changed identity before it was opened", path), file.Close())
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxTaskControlBytes+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, closeErr); err != nil {
		return nil, opened, err
	}
	if len(body) > maxTaskControlBytes {
		return nil, after, fmt.Errorf("%s exceeds %d bytes", path, maxTaskControlBytes)
	}
	pathAfter, err := lstatRegularTransactionFile(path)
	if err != nil {
		return nil, after, err
	}
	if !os.SameFile(after, pathAfter) ||
		opened.Size() != after.Size() ||
		!opened.ModTime().Equal(after.ModTime()) ||
		opened.Mode() != after.Mode() {
		return nil, after, fmt.Errorf("%s changed while it was read", path)
	}
	return body, after, nil
}

func lstatRegularTransactionFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return info, fmt.Errorf("%s is not a regular file", path)
	}
	return info, nil
}

func sameTransactionMode(current os.FileMode, expected uint32) bool {
	if runtime.GOOS == "windows" {
		return current&0o200 == os.FileMode(expected)&0o200
	}
	return uint32(current.Perm()) == expected
}

// Recover rolls an interrupted transaction back only when every touched file
// still equals the recorded before or after image. Any external edit causes a
// fail-closed conflict instead of being overwritten.
func Recover(repoRoot string) error {
	_, err := RecoverIfNeeded(repoRoot)
	return err
}

// RecoverIfNeeded is the idempotent recovery contract used by the CLI. It
// reports false when no journal exists and performs no mutation in that case.
func RecoverIfNeeded(repoRoot string) (bool, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return false, err
	}
	recovered := false
	err = withMutationLockLease(root, func(lease *taskMutationLockLease) error {
		journal, err := readTransaction(root)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		journal.lockLease = lease
		if journal.Phase == transactionPhaseCommitted {
			if err := cleanupCommittedDirectoryMarkersWithLease(root, journal.CreatedDirectories, lease); err != nil {
				return err
			}
		} else {
			if err := rollbackTransactionWithLease(root, journal, lease); err != nil {
				return err
			}
		}
		_, journalPath, pathErr := safeTransactionPath(root, transactionRel)
		if pathErr != nil {
			return pathErr
		}
		if err := journal.validateLockLease(); err != nil {
			return err
		}
		if err := os.Remove(journalPath); err != nil {
			return fmt.Errorf("remove recovered TASK journal: %w", err)
		}
		recovered = true
		return nil
	})
	return recovered, err
}

func readTransaction(repoRoot string) (transaction, error) {
	_, path, err := safeTransactionPath(repoRoot, transactionRel)
	if err != nil {
		return transaction{}, err
	}
	body, _, err := boundedio.ReadRegularFileSnapshot(path, maxTransactionBytes)
	if err != nil {
		return transaction{}, fmt.Errorf("read %s: %w", transactionRel, err)
	}
	if len(body) > maxTransactionBytes {
		return transaction{}, fmt.Errorf("%s exceeds %d bytes", transactionRel, maxTransactionBytes)
	}
	var journal transaction
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return transaction{}, fmt.Errorf("parse %s: %w", transactionRel, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return transaction{}, fmt.Errorf("parse %s: unexpected trailing JSON value", transactionRel)
		}
		return transaction{}, fmt.Errorf("parse %s: unexpected trailing data: %w", transactionRel, err)
	}
	if journal.FormatVersion != legacyTransactionVersion && journal.FormatVersion != transactionVersion {
		return transaction{}, fmt.Errorf("unsupported TASK transaction format_version %d", journal.FormatVersion)
	}
	if err := validateTransactionShape(journal); err != nil {
		return transaction{}, err
	}
	return journal, nil
}

func rollbackTransaction(repoRoot string, journal transaction) error {
	return rollbackTransactionWithLease(repoRoot, journal, journal.lockLease)
}

func rollbackTransactionWithLease(repoRoot string, journal transaction, lease *taskMutationLockLease) error {
	journal.lockLease = lease
	if err := journal.validateLockLease(); err != nil {
		return err
	}
	if journal.Phase == transactionPhaseCommitted {
		return errors.New("committed TASK transaction cannot be rolled back; finalize recovery instead")
	}
	if err := validateRollbackState(repoRoot, journal); err != nil {
		return err
	}
	filesByPath := transactionFilesByPath(journal.Files)
	for index := len(journal.Moves) - 1; index >= 0; index-- {
		move := journal.Moves[index]
		if err := rollbackTransactionMoveWithLease(repoRoot, move, filesByPath[move.Source], lease); err != nil {
			return err
		}
	}
	for _, file := range journal.Files {
		_, abs, err := safeTransactionPath(repoRoot, file.Path)
		if err != nil {
			return err
		}
		if file.Created {
			if err := removeTransactionPathWithLease(abs, lease); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("rollback created %s: %w", file.Path, err)
			}
			continue
		}
		mode := os.FileMode(file.BeforeMode)
		if mode == 0 {
			return fmt.Errorf("recovery journal has no valid before mode for %s", file.Path)
		}
		if err := validateTaskMutationLease(lease); err != nil {
			return err
		}
		if _, err := atomicfile.WriteIfChanged(abs, file.Before, mode); err != nil {
			return fmt.Errorf("rollback %s: %w", file.Path, err)
		}
	}
	return rollbackCreatedDirectoriesWithLease(repoRoot, journal.CreatedDirectories, lease)
}

func cleanupCommittedDirectoryMarkersWithLease(repoRoot string, directories []transactionDirectory, lease *taskMutationLockLease) error {
	if err := validateTaskMutationLease(lease); err != nil {
		return err
	}
	for _, directory := range directories {
		if err := validateTransactionDirectoryMarker(repoRoot, directory, true); err != nil {
			return err
		}
	}
	for _, directory := range directories {
		_, absolute, err := safeTransactionPath(repoRoot, directory.Path)
		if err != nil {
			return err
		}
		before, err := os.Lstat(absolute)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			return errors.Join(fmt.Errorf("validate committed transaction directory %s", directory.Path), err)
		}
		marker, err := transactionDirectoryMarkerPath(repoRoot, directory)
		if err != nil {
			return err
		}
		if err := validateTaskMutationLease(lease); err != nil {
			return err
		}
		if err := removeTransactionPathWithLease(marker, lease); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("remove committed transaction directory marker for %s: %w", directory.Path, err)
		}
		after, err := os.Lstat(absolute)
		if err != nil || !os.SameFile(before, after) || !after.IsDir() {
			return errors.Join(fmt.Errorf("committed transaction directory changed while removing marker: %s", directory.Path), err)
		}
	}
	return nil
}

func rollbackCreatedDirectoriesWithLease(repoRoot string, directories []transactionDirectory, lease *taskMutationLockLease) error {
	if err := validateTaskMutationLease(lease); err != nil {
		return err
	}
	ordered := append([]transactionDirectory(nil), directories...)
	sort.Slice(ordered, func(left, right int) bool {
		leftDepth := transactionPathDepth(ordered[left].Path)
		rightDepth := transactionPathDepth(ordered[right].Path)
		return leftDepth > rightDepth || leftDepth == rightDepth && ordered[left].Path > ordered[right].Path
	})
	for _, directory := range ordered {
		_, absolute, err := safeTransactionPath(repoRoot, directory.Path)
		if err != nil {
			return err
		}
		before, err := os.Lstat(absolute)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			return errors.Join(fmt.Errorf("validate rollback transaction directory %s", directory.Path), err)
		}
		if err := validateTransactionDirectoryMarker(repoRoot, directory, false); err != nil {
			return err
		}
		entries, err := boundedio.ReadDirNoSymlink(absolute, maxTransactionDirectoryEntries)
		if err != nil {
			return fmt.Errorf("inspect rollback transaction directory %s: %w", directory.Path, err)
		}
		if len(entries) != 1 || entries[0].Name() != transactionDirectoryMarker {
			return fmt.Errorf("rollback preserved non-empty transaction directory %s", directory.Path)
		}
		marker, err := transactionDirectoryMarkerPath(repoRoot, directory)
		if err != nil {
			return err
		}
		if err := removeTransactionPathWithLease(marker, lease); err != nil {
			return fmt.Errorf("remove rollback transaction directory marker for %s: %w", directory.Path, err)
		}
		after, err := os.Lstat(absolute)
		if err != nil || !os.SameFile(before, after) || !after.IsDir() {
			return errors.Join(fmt.Errorf("rollback transaction directory changed after marker removal: %s", directory.Path), err)
		}
		if err := removeTransactionPathWithLease(absolute, lease); err != nil {
			var restoreErr error
			if guardErr := validateTaskMutationLease(lease); guardErr == nil {
				_, restoreErr = atomicfile.WriteNew(marker, transactionDirectoryMarkerBody(directory), 0o600)
			}
			return errors.Join(fmt.Errorf("remove rollback transaction directory %s: %w", directory.Path, err), restoreErr)
		}
	}
	return nil
}

func rollbackTransactionMove(repoRoot string, move transactionMove, sourceFile transactionFile) error {
	return rollbackTransactionMoveWithLease(repoRoot, move, sourceFile, nil)
}

func rollbackTransactionMoveWithLease(repoRoot string, move transactionMove, sourceFile transactionFile, lease *taskMutationLockLease) error {
	if err := validateTaskMutationLease(lease); err != nil {
		return err
	}
	_, source, err := safeTransactionPath(repoRoot, move.Source)
	if err != nil {
		return err
	}
	_, destination, err := safeTransactionPath(repoRoot, move.Destination)
	if err != nil {
		return err
	}
	sourceInfo, sourceErr := os.Lstat(source)
	destinationInfo, destinationErr := os.Lstat(destination)
	switch {
	case errors.Is(destinationErr, os.ErrNotExist):
		return nil
	case destinationErr != nil:
		return fmt.Errorf("rollback move destination %s: %w", move.Destination, destinationErr)
	case sourceErr == nil && os.SameFile(sourceInfo, destinationInfo):
		if err := removeTransactionPathWithLease(destination, lease); err != nil {
			return fmt.Errorf("rollback linked move %s: %w", move.Destination, err)
		}
		return nil
	case errors.Is(sourceErr, os.ErrNotExist):
		if err := validateTaskMutationLease(lease); err != nil {
			return err
		}
		if err := os.Link(destination, source); err != nil {
			return fmt.Errorf("rollback move source precondition failed for %s: %w", move.Source, err)
		}
		if err := validateLinkedMove(source, destination, move, sourceFile); err != nil {
			removeErr := removeTransactionPathWithLease(source, lease)
			return errors.Join(err, removeErr)
		}
		if err := removeTransactionPathWithLease(destination, lease); err != nil {
			return fmt.Errorf("remove rollback move destination %s: %w", move.Destination, err)
		}
		return nil
	default:
		return fmt.Errorf("rollback move %s: source state changed after validation", move.Source)
	}
}

func validateRollbackState(repoRoot string, journal transaction) error {
	if err := validateTransactionShape(journal); err != nil {
		return err
	}
	movesBySource := make(map[string]transactionMove, len(journal.Moves))
	for _, move := range journal.Moves {
		movesBySource[move.Source] = move
	}
	if err := validateRollbackFiles(repoRoot, journal.Files, movesBySource); err != nil {
		return err
	}
	if err := validateRollbackMoves(repoRoot, journal.Moves); err != nil {
		return err
	}
	for _, directory := range journal.CreatedDirectories {
		_, absolute, err := safeTransactionPath(repoRoot, directory.Path)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(absolute); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect rollback transaction directory %s: %w", directory.Path, err)
		}
		if err := validateTransactionDirectoryMarker(repoRoot, directory, false); err != nil {
			return err
		}
	}
	return nil
}

func validateRollbackFiles(repoRoot string, files []transactionFile, movesBySource map[string]transactionMove) error {
	for _, file := range files {
		if err := validateRollbackFile(repoRoot, file, movesBySource); err != nil {
			return err
		}
	}
	return nil
}

func validateRollbackFile(repoRoot string, file transactionFile, movesBySource map[string]transactionMove) error {
	_, abs, err := safeTransactionPath(repoRoot, file.Path)
	if err != nil {
		return err
	}
	body, info, err := readRegularTransactionFile(abs)
	if file.Created {
		return validateCreatedRollbackFile(file, body, info, err)
	}
	observedPath := file.Path
	if move, moved := movesBySource[file.Path]; moved && errors.Is(err, os.ErrNotExist) {
		observedPath = move.Destination
		_, abs, err = safeTransactionPath(repoRoot, move.Destination)
		if err != nil {
			return err
		}
		body, info, err = readRegularTransactionFile(abs)
	}
	if err != nil {
		return fmt.Errorf("recovery conflict: read %s: %w", observedPath, err)
	}
	if !sameTransactionMode(info.Mode().Perm(), file.BeforeMode) {
		return fmt.Errorf(
			"recovery conflict: %s mode is %04o, expected %04o; refusing to overwrite it",
			observedPath, info.Mode().Perm(), file.BeforeMode,
		)
	}
	if digest := hashContent(body); digest != file.BeforeHash && digest != file.AfterHash {
		return fmt.Errorf("recovery conflict: %s changed outside the recorded transaction; refusing to overwrite it", observedPath)
	}
	return nil
}

func validateCreatedRollbackFile(file transactionFile, body []byte, info os.FileInfo, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("recovery conflict: read %s: %w", file.Path, err)
	}
	if hashContent(body) != file.AfterHash {
		return fmt.Errorf("recovery conflict: created %s changed outside the recorded transaction; refusing to remove it", file.Path)
	}
	if !sameTransactionMode(info.Mode().Perm(), file.BeforeMode) {
		return fmt.Errorf(
			"recovery conflict: created %s mode is %04o, expected %04o; refusing to remove it",
			file.Path, info.Mode().Perm(), file.BeforeMode,
		)
	}
	return nil
}

func validateRollbackMoves(repoRoot string, moves []transactionMove) error {
	for _, move := range moves {
		_, source, err := safeTransactionPath(repoRoot, move.Source)
		if err != nil {
			return err
		}
		_, destination, err := safeTransactionPath(repoRoot, move.Destination)
		if err != nil {
			return err
		}
		sourceBody, sourceInfo, sourceErr := readRegularTransactionFile(source)
		destinationBody, destinationInfo, destinationErr := readRegularTransactionFile(destination)
		sourceHash := hashContent(sourceBody)
		before := sourceErr == nil && errors.Is(destinationErr, os.ErrNotExist) && (sourceHash == move.BeforeHash || sourceHash == move.AfterHash)
		after := errors.Is(sourceErr, os.ErrNotExist) && destinationErr == nil && hashContent(destinationBody) == move.AfterHash
		linked := sourceErr == nil && destinationErr == nil &&
			sourceHash == move.AfterHash && hashContent(destinationBody) == move.AfterHash &&
			os.SameFile(sourceInfo, destinationInfo)
		if !before && !after && !linked {
			return fmt.Errorf("recovery conflict: move %s -> %s is neither in its recorded before nor after state", move.Source, move.Destination)
		}
	}
	return nil
}

func validateTransactionShape(journal transaction) error {
	switch journal.FormatVersion {
	case legacyTransactionVersion:
		if journal.Phase != "" || len(journal.CreatedDirectories) != 0 {
			return fmt.Errorf("legacy TASK transaction contains v2 lifecycle fields")
		}
	case transactionVersion:
		if journal.Phase != transactionPhasePrepared && journal.Phase != transactionPhaseCommitted {
			return fmt.Errorf("recovery journal has invalid phase %q", journal.Phase)
		}
	default:
		return fmt.Errorf("unsupported TASK transaction format_version %d", journal.FormatVersion)
	}
	if strings.TrimSpace(journal.Action) == "" {
		return fmt.Errorf("recovery journal has no action")
	}
	paths := make(map[string]bool, len(journal.Files)+len(journal.Moves)*2)
	for _, file := range journal.Files {
		if err := validateTransactionFileShape(file, paths); err != nil {
			return err
		}
	}
	movePaths := map[string]bool{}
	for _, move := range journal.Moves {
		if err := validateTransactionMoveShape(move, journal.Files, paths, movePaths); err != nil {
			return err
		}
	}
	for _, directory := range journal.CreatedDirectories {
		if !canonicalTransactionPath(directory.Path) || paths[directory.Path] {
			return fmt.Errorf("recovery journal has invalid or conflicting created directory %q", directory.Path)
		}
		if !validContentHash(directory.MarkerToken) {
			return fmt.Errorf("recovery journal has invalid directory ownership token for %s", directory.Path)
		}
		paths[directory.Path] = true
	}
	return nil
}

func validateTransactionFileShape(file transactionFile, paths map[string]bool) error {
	if !canonicalTransactionPath(file.Path) {
		return fmt.Errorf("recovery journal has invalid path %q", file.Path)
	}
	if paths[file.Path] {
		return fmt.Errorf("recovery journal repeats path %s", file.Path)
	}
	paths[file.Path] = true
	if file.BeforeMode == 0 || file.BeforeMode&^uint32(0o777) != 0 {
		return fmt.Errorf("recovery journal has invalid before mode for %s", file.Path)
	}
	if !validContentHash(file.BeforeHash) || !validContentHash(file.AfterHash) {
		return fmt.Errorf("recovery journal has invalid content hash for %s", file.Path)
	}
	if file.Created {
		if len(file.Before) != 0 || file.BeforeHash != hashContent(nil) {
			return fmt.Errorf("recovery journal corruption: created file %s has a before image", file.Path)
		}
	} else if hashContent(file.Before) != file.BeforeHash {
		return fmt.Errorf("recovery journal corruption: before image for %s has the wrong hash", file.Path)
	}
	return nil
}

func validateTransactionMoveShape(
	move transactionMove,
	files []transactionFile,
	paths map[string]bool,
	movePaths map[string]bool,
) error {
	if !canonicalTransactionPath(move.Source) || !canonicalTransactionPath(move.Destination) {
		return fmt.Errorf("recovery journal has invalid move path %q -> %q", move.Source, move.Destination)
	}
	if move.Source == move.Destination || movePaths[move.Source] || movePaths[move.Destination] || paths[move.Destination] {
		return fmt.Errorf("recovery journal has conflicting move %s -> %s", move.Source, move.Destination)
	}
	movePaths[move.Source] = true
	movePaths[move.Destination] = true
	if !validContentHash(move.BeforeHash) || !validContentHash(move.AfterHash) {
		return fmt.Errorf("recovery journal has invalid move hash for %s", move.Source)
	}
	sourceFile, ok := transactionFileByPath(files, move.Source)
	if !ok || sourceFile.Created {
		return fmt.Errorf("recovery journal move source %s has no before-image", move.Source)
	}
	if sourceFile.BeforeHash != move.BeforeHash || sourceFile.AfterHash != move.AfterHash {
		return fmt.Errorf("recovery journal move source %s disagrees with its file image", move.Source)
	}
	return nil
}

func canonicalTransactionPath(path string) bool {
	if validateRepoRelativePath(path) != nil {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == path
}

func validContentHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func safeTransactionPath(repoRoot, raw string) (string, string, error) {
	if err := validateRepoRelativePath(raw); err != nil {
		return "", "", fmt.Errorf("unsafe transaction path %q: %w", raw, err)
	}
	rel := filepath.Clean(filepath.FromSlash(raw))
	abs := filepath.Join(repoRoot, rel)
	if err := rejectSymlinkComponents(repoRoot, abs); err != nil {
		return "", "", fmt.Errorf("unsafe transaction path %q: %w", raw, err)
	}
	return filepath.ToSlash(rel), abs, nil
}

func hashContent(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
