package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/commandproof"
	"reconc.dev/reconc/internal/gitexec"
)

const (
	syncGitCommandTimeout = 30 * time.Second
	maxSyncGitOutputBytes = 1 << 20
)

// captureReadOnlyGitSnapshot binds a sync plan to HEAD and the staged index
// without creating command-proof state or writing into the repository object
// database. git write-tree receives an ephemeral object database and can only
// read existing objects through the repository object directory as an
// alternate.
func captureReadOnlyGitSnapshot(root string) (*commandproof.Snapshot, error) {
	present, err := inspectRepositoryGitMetadata(root)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	first, err := captureReadOnlyGitState(root)
	if err != nil {
		return nil, err
	}
	second, err := captureReadOnlyGitState(root)
	if err != nil {
		return nil, err
	}
	if first != second {
		return nil, errors.New("git HEAD or staged index changed while building the repository sync plan")
	}
	return &first, nil
}

func captureReadOnlyGitState(root string) (commandproof.Snapshot, error) {
	commonDirectory, err := syncGitOutput(root, nil, "rev-parse", "--git-common-dir")
	if err != nil {
		return commandproof.Snapshot{}, fmt.Errorf("resolve repository Git object directory: %w", err)
	}
	if !filepath.IsAbs(commonDirectory) {
		commonDirectory = filepath.Join(root, commonDirectory)
	}
	commonDirectory, err = filepath.Abs(commonDirectory)
	if err != nil {
		return commandproof.Snapshot{}, fmt.Errorf("resolve repository Git common directory: %w", err)
	}
	objectDirectory := filepath.Join(commonDirectory, "objects")
	info, err := os.Lstat(objectDirectory)
	if err != nil {
		return commandproof.Snapshot{}, fmt.Errorf("inspect repository Git object directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return commandproof.Snapshot{}, fmt.Errorf("repository Git object path is not a real directory: %s", objectDirectory)
	}

	ephemeralObjects, err := os.MkdirTemp("", "reconc-sync-git-objects-")
	if err != nil {
		return commandproof.Snapshot{}, fmt.Errorf("create ephemeral Git object directory: %w", err)
	}
	if err := os.Chmod(ephemeralObjects, 0o700); err != nil {
		removeErr := os.RemoveAll(ephemeralObjects)
		return commandproof.Snapshot{}, errors.Join(fmt.Errorf("secure ephemeral Git object directory: %w", err), removeErr)
	}
	defer os.RemoveAll(ephemeralObjects)

	objects := &gitexec.ObjectDirectories{
		ObjectDirectory:            ephemeralObjects,
		AlternateObjectDirectories: gitPathListEntry(objectDirectory),
	}
	head, err := syncGitHead(root)
	if err != nil {
		return commandproof.Snapshot{}, err
	}
	indexTree, err := syncGitOutput(root, objects, "write-tree")
	if err != nil {
		return commandproof.Snapshot{}, fmt.Errorf("capture repository sync index tree: %w", err)
	}
	if strings.TrimSpace(indexTree) == "" {
		return commandproof.Snapshot{}, errors.New("capture repository sync index tree: git returned an empty object identity")
	}
	return commandproof.Snapshot{RepoRoot: root, Head: head, IndexTree: indexTree}, nil
}

func syncGitHead(root string) (string, error) {
	head, err := syncGitOutput(root, nil, "rev-parse", "--verify", "HEAD")
	if err == nil {
		return head, nil
	}
	if _, symbolicErr := syncGitOutput(root, nil, "symbolic-ref", "--quiet", "HEAD"); symbolicErr == nil {
		return "UNBORN", nil
	}
	return "", fmt.Errorf("capture repository sync HEAD: %w", err)
}

func syncGitOutput(root string, objects *gitexec.ObjectDirectories, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), syncGitCommandTimeout)
	defer cancel()
	cmd := gitexec.CommandContext(ctx, root, objects, args...)
	stdout, err := boundedexec.NewBuffer(maxSyncGitOutputBytes)
	if err != nil {
		return "", fmt.Errorf("initialize repository sync git stdout capture: %w", err)
	}
	stderr, err := boundedexec.NewBuffer(maxSyncGitOutputBytes)
	if err != nil {
		return "", fmt.Errorf("initialize repository sync git stderr capture: %w", err)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), syncGitCommandTimeout)
	}
	if stdout.Truncated() || stderr.Truncated() {
		return "", fmt.Errorf("git %s output exceeds %d bytes", strings.Join(args, " "), maxSyncGitOutputBytes)
	}
	if runErr != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), runErr, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func gitPathListEntry(path string) string {
	if strings.ContainsRune(path, os.PathListSeparator) ||
		strings.ContainsAny(path, "\"\\\n\r\t") {
		return strconv.Quote(path)
	}
	return path
}
