package tasklifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/filelock"
)

const (
	transactionRel      = ".reconc/task-transaction.json"
	taskLockRel         = ".reconc/locks/task-lifecycle.lock"
	transactionVersion  = 1
	maxTransactionBytes = 4 << 20
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
	FormatVersion int               `json:"format_version"`
	Action        string            `json:"action"`
	Files         []transactionFile `json:"files"`
	Moves         []transactionMove `json:"moves,omitempty"`
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

func transactionExists(repoRoot string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(transactionRel)))
	return err == nil
}

func withMutationLock(repoRoot string, fn func() error) error {
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
	defer file.Close()
	unlock, err := filelock.Lock(file)
	if err != nil {
		return fmt.Errorf("lock TASK lifecycle: %w", err)
	}
	defer unlock()
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
	body, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal TASK transaction: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxTransactionBytes {
		return fmt.Errorf("TASK transaction journal is %d bytes; maximum is %d", len(body), maxTransactionBytes)
	}
	_, journalPath, err := safeTransactionPath(repoRoot, transactionRel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(journalPath); err == nil {
		return fmt.Errorf("pending %s exists; run `reconc task recover %s`", transactionRel, repoRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect TASK transaction: %w", err)
	}
	if _, err := atomicfile.WriteIfChanged(journalPath, body, 0o600); err != nil {
		return fmt.Errorf("publish TASK transaction: %w", err)
	}
	if err := publishTransaction(repoRoot, journal, files, moves); err != nil {
		rollbackErr := rollbackTransaction(repoRoot, journal)
		if rollbackErr != nil {
			return fmt.Errorf("publish TASK transaction: %w; automatic rollback failed: %v; run `reconc task recover %s`", err, rollbackErr, repoRoot)
		}
		_ = os.Remove(journalPath)
		return fmt.Errorf("publish TASK transaction: %w (rolled back)", err)
	}
	if err := os.Remove(journalPath); err != nil {
		return fmt.Errorf("TASK transaction committed but journal cleanup failed: %w; run `reconc task recover %s`", err, repoRoot)
	}
	return nil
}

func buildTransaction(repoRoot, action string, files []fileMutation, moves []moveMutation) (transaction, error) {
	journal := transaction{FormatVersion: transactionVersion, Action: action}
	transactionFiles, afterByPath, err := buildTransactionFiles(repoRoot, files)
	if err != nil {
		return transaction{}, err
	}
	journal.Files = transactionFiles
	for _, move := range moves {
		transactionMove, err := buildTransactionMove(repoRoot, move, afterByPath)
		if err != nil {
			return transaction{}, err
		}
		journal.Moves = append(journal.Moves, transactionMove)
	}
	return journal, nil
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
			before, err := os.ReadFile(abs)
			if err != nil {
				return nil, nil, fmt.Errorf("read transaction source %s: %w", rel, err)
			}
			info, err := os.Stat(abs)
			if err != nil {
				return nil, nil, fmt.Errorf("stat transaction source %s: %w", rel, err)
			}
			out = append(out, transactionFile{Path: rel, Before: before, BeforeMode: uint32(info.Mode().Perm()), BeforeHash: hashContent(before), AfterHash: hashContent(change.After)})
		}
		afterByPath[rel] = hashContent(change.After)
	}
	return out, afterByPath, nil
}

func buildTransactionMove(repoRoot string, move moveMutation, afterByPath map[string]string) (transactionMove, error) {
	sourceRel, sourceAbs, err := safeTransactionPath(repoRoot, move.Source)
	if err != nil {
		return transactionMove{}, err
	}
	destinationRel, destinationAbs, err := safeTransactionPath(repoRoot, move.Destination)
	if err != nil {
		return transactionMove{}, err
	}
	body, err := os.ReadFile(sourceAbs)
	if err != nil {
		return transactionMove{}, fmt.Errorf("read move source %s: %w", sourceRel, err)
	}
	if _, err := os.Stat(destinationAbs); err == nil {
		return transactionMove{}, fmt.Errorf("move destination already exists: %s", destinationRel)
	} else if !errors.Is(err, os.ErrNotExist) {
		return transactionMove{}, fmt.Errorf("inspect move destination %s: %w", destinationRel, err)
	}
	beforeHash := hashContent(body)
	afterHash := beforeHash
	if mutatedHash, ok := afterByPath[sourceRel]; ok {
		afterHash = mutatedHash
	}
	return transactionMove{Source: sourceRel, Destination: destinationRel, BeforeHash: beforeHash, AfterHash: afterHash}, nil
}

func publishTransaction(repoRoot string, journal transaction, files []fileMutation, moves []moveMutation) error {
	modes := make(map[string]os.FileMode, len(journal.Files))
	for _, file := range journal.Files {
		modes[file.Path] = os.FileMode(file.BeforeMode)
	}
	for _, change := range files {
		rel, abs, err := safeTransactionPath(repoRoot, change.Path)
		if err != nil {
			return err
		}
		mode, ok := modes[rel]
		if !ok || mode == 0 {
			return fmt.Errorf("transaction has no valid before mode for %s", rel)
		}
		if journalFileCreated(journal.Files, rel) {
			if err := writeNewTransactionFile(abs, change.After, mode); err != nil {
				return err
			}
		} else if _, err := atomicfile.WriteIfChanged(abs, change.After, mode); err != nil {
			return err
		}
	}
	for _, move := range moves {
		_, source, err := safeTransactionPath(repoRoot, move.Source)
		if err != nil {
			return err
		}
		_, destination, err := safeTransactionPath(repoRoot, move.Destination)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		if err := os.Rename(source, destination); err != nil {
			return err
		}
	}
	return nil
}

func journalFileCreated(files []transactionFile, path string) bool {
	for _, file := range files {
		if file.Path == path {
			return file.Created
		}
	}
	return false
}

func writeNewTransactionFile(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create transaction parent: %w", err)
	}
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

// Recover rolls an interrupted transaction back only when every touched file
// still equals the recorded before or after image. Any external edit causes a
// fail-closed conflict instead of being overwritten.
func Recover(repoRoot string) error {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return withMutationLock(root, func() error {
		journal, err := readTransaction(root)
		if err != nil {
			return err
		}
		if err := rollbackTransaction(root, journal); err != nil {
			return err
		}
		_, journalPath, pathErr := safeTransactionPath(root, transactionRel)
		if pathErr != nil {
			return pathErr
		}
		if err := os.Remove(journalPath); err != nil {
			return fmt.Errorf("remove recovered TASK journal: %w", err)
		}
		return nil
	})
}

func readTransaction(repoRoot string) (transaction, error) {
	_, path, err := safeTransactionPath(repoRoot, transactionRel)
	if err != nil {
		return transaction{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return transaction{}, fmt.Errorf("read %s: %w", transactionRel, err)
	}
	if len(body) > maxTransactionBytes {
		return transaction{}, fmt.Errorf("%s exceeds %d bytes", transactionRel, maxTransactionBytes)
	}
	var journal transaction
	if err := json.Unmarshal(body, &journal); err != nil {
		return transaction{}, fmt.Errorf("parse %s: %w", transactionRel, err)
	}
	if journal.FormatVersion != transactionVersion {
		return transaction{}, fmt.Errorf("unsupported TASK transaction format_version %d", journal.FormatVersion)
	}
	return journal, nil
}

func rollbackTransaction(repoRoot string, journal transaction) error {
	if err := validateRollbackState(repoRoot, journal); err != nil {
		return err
	}
	for index := len(journal.Moves) - 1; index >= 0; index-- {
		move := journal.Moves[index]
		_, source, _ := safeTransactionPath(repoRoot, move.Source)
		_, destination, _ := safeTransactionPath(repoRoot, move.Destination)
		if _, err := os.Stat(destination); err == nil {
			if err := os.Rename(destination, source); err != nil {
				return fmt.Errorf("rollback move %s: %w", move.Source, err)
			}
		}
	}
	for _, file := range journal.Files {
		_, abs, _ := safeTransactionPath(repoRoot, file.Path)
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
	return nil
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
	return validateRollbackMoves(repoRoot, journal.Moves)
}

func validateRollbackFiles(repoRoot string, files []transactionFile, movesBySource map[string]transactionMove) error {
	for _, file := range files {
		_, abs, err := safeTransactionPath(repoRoot, file.Path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(abs)
		if file.Created {
			validCreatedState := errors.Is(err, os.ErrNotExist) || (err == nil && hashContent(body) == file.AfterHash)
			if !validCreatedState {
				if err != nil {
					return fmt.Errorf("recovery conflict: read %s: %w", file.Path, err)
				}
				return fmt.Errorf("recovery conflict: created %s changed outside the recorded transaction; refusing to remove it", file.Path)
			}
			if len(file.Before) != 0 || file.BeforeHash != hashContent(nil) {
				return fmt.Errorf("recovery journal corruption: created file %s has a before image", file.Path)
			}
			continue
		}
		validFileState := err == nil && (hashContent(body) == file.BeforeHash || hashContent(body) == file.AfterHash)
		if move, moved := movesBySource[file.Path]; moved && errors.Is(err, os.ErrNotExist) {
			_, destination, pathErr := safeTransactionPath(repoRoot, move.Destination)
			if pathErr != nil {
				return pathErr
			}
			destinationBody, destinationErr := os.ReadFile(destination)
			validFileState = destinationErr == nil && hashContent(destinationBody) == file.AfterHash
		}
		if !validFileState {
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("recovery conflict: read %s: %w", file.Path, err)
			}
			return fmt.Errorf("recovery conflict: %s changed outside the recorded transaction; refusing to overwrite it", file.Path)
		}
		if hashContent(file.Before) != file.BeforeHash {
			return fmt.Errorf("recovery journal corruption: before image for %s has the wrong hash", file.Path)
		}
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
		sourceBody, sourceErr := os.ReadFile(source)
		destinationBody, destinationErr := os.ReadFile(destination)
		sourceHash := hashContent(sourceBody)
		before := sourceErr == nil && errors.Is(destinationErr, os.ErrNotExist) && (sourceHash == move.BeforeHash || sourceHash == move.AfterHash)
		after := errors.Is(sourceErr, os.ErrNotExist) && destinationErr == nil && hashContent(destinationBody) == move.AfterHash
		if !before && !after {
			return fmt.Errorf("recovery conflict: move %s -> %s is neither in its recorded before nor after state", move.Source, move.Destination)
		}
	}
	return nil
}

func validateTransactionShape(journal transaction) error {
	if strings.TrimSpace(journal.Action) == "" {
		return fmt.Errorf("recovery journal has no action")
	}
	paths := make(map[string]bool, len(journal.Files)+len(journal.Moves)*2)
	for _, file := range journal.Files {
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
	}
	movePaths := map[string]bool{}
	for _, move := range journal.Moves {
		if move.Source == move.Destination || movePaths[move.Source] || movePaths[move.Destination] || paths[move.Destination] {
			return fmt.Errorf("recovery journal has conflicting move %s -> %s", move.Source, move.Destination)
		}
		movePaths[move.Source] = true
		movePaths[move.Destination] = true
		if !validContentHash(move.BeforeHash) || !validContentHash(move.AfterHash) {
			return fmt.Errorf("recovery journal has invalid move hash for %s", move.Source)
		}
	}
	return nil
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
