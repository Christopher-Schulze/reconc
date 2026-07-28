package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/commandproof"
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
	if _, err := os.Lstat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect Git metadata for repository sync: %w", err)
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

	overrides := map[string]string{
		"GIT_OBJECT_DIRECTORY":             ephemeralObjects,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": gitPathListEntry(objectDirectory),
	}
	head, err := syncGitHead(root)
	if err != nil {
		return commandproof.Snapshot{}, err
	}
	indexTree, err := syncGitOutput(root, overrides, "write-tree")
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

func syncGitOutput(root string, overrides map[string]string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), syncGitCommandTimeout)
	defer cancel()
	gitArgs := append([]string{
		"--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
		"-c", "core.hooksPath=",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = root
	cmd.Env = hermeticSyncGitEnvironment(overrides)
	stdout := &syncBoundedOutput{limit: maxSyncGitOutputBytes}
	stderr := &syncBoundedOutput{limit: maxSyncGitOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), syncGitCommandTimeout)
	}
	if stdout.overflow || stderr.overflow {
		return "", fmt.Errorf("git %s output exceeds %d bytes", strings.Join(args, " "), maxSyncGitOutputBytes)
	}
	if runErr != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), runErr, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func hermeticSyncGitEnvironment(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+8+len(overrides))
	for _, entry := range os.Environ() {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") || strings.EqualFold(key, "LC_ALL") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	)
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := overrides[key]
		environment = append(environment, key+"="+value)
	}
	return environment
}

func gitPathListEntry(path string) string {
	if strings.ContainsRune(path, os.PathListSeparator) ||
		strings.ContainsAny(path, "\"\\\n\r\t") {
		return strconv.Quote(path)
	}
	return path
}

type syncBoundedOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (output *syncBoundedOutput) Write(data []byte) (int, error) {
	remaining := output.limit - output.Len()
	if remaining > 0 {
		count := len(data)
		if count > remaining {
			count = remaining
		}
		_, _ = output.Buffer.Write(data[:count])
	}
	if len(data) > remaining {
		output.overflow = true
	}
	return len(data), nil
}
