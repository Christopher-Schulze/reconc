package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
)

const maxGitMetadataFileBytes = 16 << 10

func inspectRepositoryGitMetadata(root string) (bool, error) {
	path := filepath.Join(root, ".git")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect repository Git metadata: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("repository Git metadata must not be a symlink: %s", path)
	}
	if info.IsDir() {
		return true, nil
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("repository Git metadata must be a directory or worktree metadata file: %s", path)
	}
	body, err := boundedio.ReadRegularFile(path, maxGitMetadataFileBytes)
	if err != nil {
		return false, fmt.Errorf("read repository Git worktree metadata: %w", err)
	}
	line := strings.TrimSpace(string(body))
	if strings.ContainsAny(line, "\r\n") || !strings.HasPrefix(line, "gitdir: ") {
		return false, fmt.Errorf("repository Git worktree metadata is malformed: %s", path)
	}
	gitDirectory := strings.TrimSpace(strings.TrimPrefix(line, "gitdir: "))
	if gitDirectory == "" {
		return false, fmt.Errorf("repository Git worktree metadata has an empty gitdir: %s", path)
	}
	if !filepath.IsAbs(gitDirectory) {
		gitDirectory = filepath.Join(root, gitDirectory)
	}
	resolved, err := pathidentity.ResolveExisting(gitDirectory)
	if err != nil {
		return false, fmt.Errorf("resolve repository Git worktree directory: %w", err)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil || !resolvedInfo.IsDir() {
		return false, fmt.Errorf("repository Git worktree directory is not a directory: %s", resolved)
	}
	return true, nil
}
