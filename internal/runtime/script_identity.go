package runtime

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/execfile"
	"reconc.dev/reconc/internal/privatefs"
)

type scriptExecutionStage string

const (
	scriptStagePathValidated   scriptExecutionStage = "path_validated"
	scriptStageSourceOpened    scriptExecutionStage = "source_opened"
	scriptStageSnapshotCreated scriptExecutionStage = "snapshot_created"
	scriptStageCommandPrepared scriptExecutionStage = "command_prepared"
)

type scriptExecutionHook func(scriptExecutionStage, string) error

// boundScript owns the immutable private executable snapshot and the source
// identity/digest that must still match immediately before process creation.
type boundScript struct {
	repoRoot      string
	scriptPath    string
	sourcePath    string
	sourceInfo    os.FileInfo
	sourceDigest  [sha256.Size]byte
	executionPath string
	executionInfo os.FileInfo
	tempDir       string
}

func prepareBoundScript(repoRoot, scriptPath, sourcePath string, hook scriptExecutionHook) (*boundScript, error) {
	sourceInfo, digest, err := validateScriptSource(scriptPath, sourcePath)
	if err != nil {
		return nil, err
	}
	if err := runScriptExecutionHook(hook, scriptStagePathValidated, sourcePath); err != nil {
		return nil, err
	}
	source, err := openValidatedScript(scriptPath, sourcePath, sourceInfo)
	if err != nil {
		return nil, err
	}
	if err := runScriptExecutionHook(hook, scriptStageSourceOpened, sourcePath); err != nil {
		return nil, errors.Join(err, source.Close())
	}
	executionPath, executionInfo, tempDir, err := createPrivateScriptSnapshot(source, sourceInfo, digest, scriptPath)
	if err != nil {
		return nil, err
	}
	bound := &boundScript{
		repoRoot: repoRoot, scriptPath: scriptPath, sourcePath: sourcePath,
		sourceInfo: sourceInfo, sourceDigest: digest,
		executionPath: executionPath, executionInfo: executionInfo, tempDir: tempDir,
	}
	if err := runScriptExecutionHook(hook, scriptStageSnapshotCreated, sourcePath); err != nil {
		return nil, errors.Join(err, bound.cleanup())
	}
	return bound, nil
}

func validateScriptSource(scriptPath, sourcePath string) (os.FileInfo, [sha256.Size]byte, error) {
	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return nil, [sha256.Size]byte{}, fmt.Errorf("script not found: %s: %w", scriptPath, err)
	}
	if !execfile.ModeIsExecutable(sourceInfo.Mode()) {
		return nil, [sha256.Size]byte{}, fmt.Errorf("script is not an executable regular file: %s", scriptPath)
	}
	digest, err := readScriptDigest(sourcePath, sourceInfo)
	if err != nil {
		return nil, [sha256.Size]byte{}, fmt.Errorf("validate script %s: %w", scriptPath, err)
	}
	return sourceInfo, digest, nil
}

// openValidatedScript closes the pathname race by comparing the opened
// descriptor to the non-symlink executable identity already hashed.
func openValidatedScript(scriptPath, sourcePath string, sourceInfo os.FileInfo) (*os.File, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("open script %s: %w", scriptPath, err)
	}
	openedInfo, err := source.Stat()
	if err != nil || !sameScriptIdentity(sourceInfo, openedInfo) {
		return nil, errors.Join(fmt.Errorf("script changed before it could be opened: %s", scriptPath), err, source.Close())
	}
	return source, nil
}

// createPrivateScriptSnapshot streams the opened source into a fresh 0700
// directory, retaining only read/execute permissions and a SHA-256 identity.
// Repository path replacement can no longer select the bytes passed to exec.
func createPrivateScriptSnapshot(source *os.File, sourceInfo os.FileInfo, sourceDigest [sha256.Size]byte, scriptPath string) (string, os.FileInfo, string, error) {
	tempDir, err := os.MkdirTemp("", "reconc-script-")
	if err != nil {
		return "", nil, "", errors.Join(fmt.Errorf("create private script snapshot directory: %w", err), source.Close())
	}
	if err := privatefs.RepairDirectory(tempDir); err != nil {
		return "", nil, "", errors.Join(fmt.Errorf("secure private script snapshot directory: %w", err), source.Close(), os.Remove(tempDir))
	}
	execution, err := os.CreateTemp(tempDir, "program-*"+filepath.Ext(source.Name()))
	if err != nil {
		return "", nil, "", errors.Join(fmt.Errorf("create private script snapshot: %w", err), source.Close(), os.Remove(tempDir))
	}
	executionPath := execution.Name()
	if err := privatefs.SecureFile(execution); err != nil {
		return "", nil, "", errors.Join(fmt.Errorf("secure private script snapshot: %w", err), execution.Close(), source.Close(), removeScriptSnapshot(executionPath, tempDir))
	}
	digest := sha256.New()
	written, copyErr := copyScriptBytes(execution, digest, source, sourceInfo.Size())
	afterFile, fileStatErr := source.Stat()
	afterPath, pathStatErr := os.Lstat(source.Name())
	chmodErr := execution.Chmod(sourceInfo.Mode().Perm()&0o555 | 0o400)
	closeExecutionErr := execution.Close()
	closeSourceErr := source.Close()
	if err := errors.Join(copyErr, fileStatErr, pathStatErr, chmodErr, closeExecutionErr, closeSourceErr); err != nil {
		return "", nil, "", errors.Join(fmt.Errorf("snapshot script %s: %w", scriptPath, err), removeScriptSnapshot(executionPath, tempDir))
	}
	if written != sourceInfo.Size() || digestSum(digest) != sourceDigest ||
		!sameScriptIdentity(sourceInfo, afterFile) || !sameScriptIdentity(afterFile, afterPath) {
		return "", nil, "", errors.Join(fmt.Errorf("script changed while it was snapshotted: %s", scriptPath), removeScriptSnapshot(executionPath, tempDir))
	}
	executionInfo, err := os.Lstat(executionPath)
	if err != nil || !execfile.ModeIsExecutable(executionInfo.Mode()) || executionInfo.Size() != written {
		return "", nil, "", errors.Join(fmt.Errorf("private script snapshot is invalid: %s", scriptPath), err, removeScriptSnapshot(executionPath, tempDir))
	}
	return executionPath, executionInfo, tempDir, nil
}

func (s *boundScript) revalidate() error {
	resolved, err := resolveRepoScriptPath(s.repoRoot, s.scriptPath)
	if err != nil {
		return fmt.Errorf("revalidate script containment: %w", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(s.sourcePath) {
		return fmt.Errorf("script path changed during execution preparation: %s", s.scriptPath)
	}
	if err := validateScriptDigest(s.sourcePath, s.sourceInfo, s.sourceDigest); err != nil {
		return fmt.Errorf("revalidate script %s: %w", s.scriptPath, err)
	}
	if err := validateScriptDigest(s.executionPath, s.executionInfo, s.sourceDigest); err != nil {
		return fmt.Errorf("revalidate private script snapshot for %s: %w", s.scriptPath, err)
	}
	return nil
}

func (s *boundScript) cleanup() error {
	if s == nil {
		return nil
	}
	return removeScriptSnapshot(s.executionPath, s.tempDir)
}

func validateScriptDigest(path string, expected os.FileInfo, want [sha256.Size]byte) error {
	digest, err := readScriptDigest(path, expected)
	if err != nil {
		return err
	}
	if digest != want {
		return errors.New("file content digest changed")
	}
	return nil
}

func readScriptDigest(path string, expected os.FileInfo) ([sha256.Size]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	if !sameScriptIdentity(expected, before) {
		return [sha256.Size]byte{}, errors.New("file identity or metadata changed")
	}
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	opened, statErr := file.Stat()
	digest := sha256.New()
	read, copyErr := copyScriptBytes(io.Discard, digest, file, expected.Size())
	afterFile, afterFileErr := file.Stat()
	afterPath, afterPathErr := os.Lstat(path)
	closeErr := file.Close()
	if err := errors.Join(statErr, copyErr, afterFileErr, afterPathErr, closeErr); err != nil {
		return [sha256.Size]byte{}, err
	}
	if read != expected.Size() || !sameScriptIdentity(expected, opened) ||
		!sameScriptIdentity(opened, afterFile) || !sameScriptIdentity(afterFile, afterPath) {
		return [sha256.Size]byte{}, errors.New("file changed while its digest was read")
	}
	return digestSum(digest), nil
}

func copyScriptBytes(dst io.Writer, digest hash.Hash, source io.Reader, size int64) (int64, error) {
	if size < 0 || size == math.MaxInt64 {
		return 0, errors.New("script size is invalid")
	}
	return io.Copy(io.MultiWriter(dst, digest), io.LimitReader(source, size+1))
}

func sameScriptIdentity(left, right os.FileInfo) bool {
	return left != nil && right != nil && execfile.ModeIsExecutable(left.Mode()) &&
		execfile.ModeIsExecutable(right.Mode()) && os.SameFile(left, right) &&
		left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func digestSum(digest hash.Hash) [sha256.Size]byte {
	var sum [sha256.Size]byte
	copy(sum[:], digest.Sum(nil))
	return sum
}

func runScriptExecutionHook(hook scriptExecutionHook, stage scriptExecutionStage, sourcePath string) error {
	if hook == nil {
		return nil
	}
	if err := hook(stage, sourcePath); err != nil {
		return fmt.Errorf("script execution hook %s: %w", stage, err)
	}
	return nil
}

func removeScriptSnapshot(path, directory string) error {
	var removeFileErr error
	if path != "" {
		removeFileErr = os.Remove(path)
		if errors.Is(removeFileErr, os.ErrNotExist) {
			removeFileErr = nil
		}
	}
	var removeDirectoryErr error
	if directory != "" {
		removeDirectoryErr = os.Remove(directory)
		if errors.Is(removeDirectoryErr, os.ErrNotExist) {
			removeDirectoryErr = nil
		}
	}
	return errors.Join(removeFileErr, removeDirectoryErr)
}
