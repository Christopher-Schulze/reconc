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
	path := filepath.Join(repoRoot, filepath.FromSlash(taskLockRel))
	if err := rejectSymlinkComponents(repoRoot, path); err != nil {
		return fmt.Errorf("unsafe TASK lock path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create TASK lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open TASK lock: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, closeMutationLock(file)) }()
	unlock, err := acquireMutationLock(context.Background(), file, filelock.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("lock TASK lifecycle: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, unlock()) }()
	return fn()
}

func applyTransaction(repoRoot, action string, files []fileMutation, moves []moveMutation) error {
	journal, err := buildTransaction(repoRoot, action, files, moves)
	if err != nil {
		return err
	}
	if err := validateTransactionShape(journal); err != nil {
		return err
	}
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
	if _, err := atomicfile.WritePrivateIfChanged(journalPath, body, 0o600); err != nil {
		return fmt.Errorf("publish TASK transaction: %w", err)
	}
	if err := publishTransaction(repoRoot, journal, files, moves); err != nil {
		rollbackErr := rollbackTransaction(repoRoot, journal)
		if rollbackErr != nil {
			return fmt.Errorf("publish TASK transaction: %w; automatic rollback failed: %v; run `reconc task recover %s`", err, rollbackErr, repoRoot)
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
		rollbackErr := rollbackTransaction(repoRoot, journal)
		return errors.Join(err, rollbackErr)
	}
	if _, err := atomicfile.WritePrivateIfChanged(journalPath, committedBody, 0o600); err != nil {
		observed, readErr := readTransaction(repoRoot)
		if readErr != nil || observed.Phase == transactionPhaseCommitted {
			return errors.Join(
				fmt.Errorf("TASK transaction publication completed but commit-state durability is uncertain: %w; run `reconc task recover %s`", err, repoRoot),
				readErr,
			)
		}
		rollbackErr := rollbackTransaction(repoRoot, journal)
		if rollbackErr != nil {
			return fmt.Errorf("mark TASK transaction committed: %w; automatic rollback failed: %v; run `reconc task recover %s`", err, rollbackErr, repoRoot)
		}
		return fmt.Errorf("mark TASK transaction committed: %w (rolled back)", err)
	}
	if err := cleanupCommittedDirectoryMarkers(repoRoot, committed.CreatedDirectories); err != nil {
		return fmt.Errorf("TASK transaction committed but directory-marker cleanup failed: %w; run `reconc task recover %s`", err, repoRoot)
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
	if err := validateTransactionShape(journal); err != nil {
		return err
	}
	if err := validatePublishInputs(repoRoot, journal, files, moves); err != nil {
		return err
	}
	if err := validatePublishState(repoRoot, journal); err != nil {
		return err
	}
	if err := prepareTransactionDirectories(repoRoot, journal.CreatedDirectories); err != nil {
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
		mode := os.FileMode(recorded.BeforeMode)
		if recorded.Created {
			if err := writeNewTransactionFile(abs, change.After, mode); err != nil {
				return err
			}
		} else if _, err := atomicfile.WriteIfChanged(abs, change.After, mode); err != nil {
			return err
		}
	}
	for index, move := range moves {
		if err := publishTransactionMove(repoRoot, journal.Moves[index], filesByPath, move); err != nil {
			return err
		}
	}
	return nil
}

func prepareTransactionDirectories(repoRoot string, directories []transactionDirectory) error {
	for _, directory := range directories {
		_, absolute, err := safeTransactionPath(repoRoot, directory.Path)
		if err != nil {
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
		if err := atomicfile.WriteNew(marker, transactionDirectoryMarkerBody(directory), 0o600); err != nil {
			return errors.Join(
				fmt.Errorf("publish transaction directory marker for %s: %w", directory.Path, err),
				os.Remove(absolute),
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

func publishTransactionMove(
	repoRoot string,
	recorded transactionMove,
	filesByPath map[string]transactionFile,
	requested moveMutation,
) error {
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
	if err := validateMovePublishPrecondition(repoRoot, recorded, sourceFile); err != nil {
		return err
	}
	_, source, err := safeTransactionPath(repoRoot, requested.Source)
	if err != nil {
		return err
	}
	if err := os.Link(source, destination); err != nil {
		return fmt.Errorf("transaction move destination precondition failed for %s: %w", recorded.Destination, err)
	}
	if err := validateLinkedMove(source, destination, recorded, sourceFile); err != nil {
		removeErr := os.Remove(destination)
		return errors.Join(err, removeErr)
	}
	if err := os.Remove(source); err != nil {
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

func writeNewTransactionFile(path string, body []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create transaction file %s: %w", path, err)
	}
	written, writeErr := file.Write(body)
	if writeErr == nil && written != len(body) {
		writeErr = io.ErrShortWrite
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		removeErr := os.Remove(path)
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
	err = withMutationLock(root, func() error {
		journal, err := readTransaction(root)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if journal.Phase == transactionPhaseCommitted {
			if err := cleanupCommittedDirectoryMarkers(root, journal.CreatedDirectories); err != nil {
				return err
			}
		} else {
			if err := rollbackTransaction(root, journal); err != nil {
				return err
			}
		}
		_, journalPath, pathErr := safeTransactionPath(root, transactionRel)
		if pathErr != nil {
			return pathErr
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
	if journal.Phase == transactionPhaseCommitted {
		return errors.New("committed TASK transaction cannot be rolled back; finalize recovery instead")
	}
	if err := validateRollbackState(repoRoot, journal); err != nil {
		return err
	}
	filesByPath := transactionFilesByPath(journal.Files)
	for index := len(journal.Moves) - 1; index >= 0; index-- {
		move := journal.Moves[index]
		if err := rollbackTransactionMove(repoRoot, move, filesByPath[move.Source]); err != nil {
			return err
		}
	}
	for _, file := range journal.Files {
		_, abs, err := safeTransactionPath(repoRoot, file.Path)
		if err != nil {
			return err
		}
		if file.Created {
			if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("rollback created %s: %w", file.Path, err)
			}
			continue
		}
		mode := os.FileMode(file.BeforeMode)
		if mode == 0 {
			return fmt.Errorf("recovery journal has no valid before mode for %s", file.Path)
		}
		if _, err := atomicfile.WriteIfChanged(abs, file.Before, mode); err != nil {
			return fmt.Errorf("rollback %s: %w", file.Path, err)
		}
	}
	return rollbackCreatedDirectories(repoRoot, journal.CreatedDirectories)
}

func cleanupCommittedDirectoryMarkers(repoRoot string, directories []transactionDirectory) error {
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
		if err := os.Remove(marker); errors.Is(err, os.ErrNotExist) {
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

func rollbackCreatedDirectories(repoRoot string, directories []transactionDirectory) error {
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
		if err := os.Remove(marker); err != nil {
			return fmt.Errorf("remove rollback transaction directory marker for %s: %w", directory.Path, err)
		}
		after, err := os.Lstat(absolute)
		if err != nil || !os.SameFile(before, after) || !after.IsDir() {
			return errors.Join(fmt.Errorf("rollback transaction directory changed after marker removal: %s", directory.Path), err)
		}
		if err := os.Remove(absolute); err != nil {
			restoreErr := atomicfile.WriteNew(marker, transactionDirectoryMarkerBody(directory), 0o600)
			return errors.Join(fmt.Errorf("remove rollback transaction directory %s: %w", directory.Path, err), restoreErr)
		}
	}
	return nil
}

func rollbackTransactionMove(repoRoot string, move transactionMove, sourceFile transactionFile) error {
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
		if err := os.Remove(destination); err != nil {
			return fmt.Errorf("rollback linked move %s: %w", move.Destination, err)
		}
		return nil
	case errors.Is(sourceErr, os.ErrNotExist):
		if err := os.Link(destination, source); err != nil {
			return fmt.Errorf("rollback move source precondition failed for %s: %w", move.Source, err)
		}
		if err := validateLinkedMove(source, destination, move, sourceFile); err != nil {
			removeErr := os.Remove(source)
			return errors.Join(err, removeErr)
		}
		if err := os.Remove(destination); err != nil {
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
