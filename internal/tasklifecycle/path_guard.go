package tasklifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// taskPathGuard records the filesystem identities observed while resolving a
// TASK path. It is intentionally local to one read transaction; the cache is
// never a path-string allowlist and every accepted result must revalidate it.
type taskPathGuard struct {
	root string
	seen map[string]taskPathIdentity
}

type taskPathIdentity struct {
	info os.FileInfo
}

func newTaskPathGuard(root string, capacity int) *taskPathGuard {
	return &taskPathGuard{root: root, seen: make(map[string]taskPathIdentity, capacity)}
}

func (guard *taskPathGuard) reject(abs string) error {
	if err := guard.revalidate(); err != nil {
		return err
	}
	rel, err := filepath.Rel(guard.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes the repository")
	}
	current := guard.root
	components := strings.Split(rel, string(filepath.Separator))
	for index, component := range components {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect path component %s: %w", current, statErr)
		}
		if err := validateTaskPathComponent(current, info, index < len(components)-1); err != nil {
			return err
		}
		if previous, ok := guard.seen[current]; ok && !sameTaskPathIdentity(previous.info, info) {
			return fmt.Errorf("TASK path component identity changed: %s", current)
		}
		guard.seen[current] = taskPathIdentity{info: info}
	}
	return nil
}

func (guard *taskPathGuard) revalidate() error {
	for path, expected := range guard.seen {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("TASK path component changed: %s: %w", path, err)
		}
		if err := validateTaskPathComponent(path, info, expected.info.IsDir()); err != nil {
			return err
		}
		if !sameTaskPathIdentity(expected.info, info) {
			return fmt.Errorf("TASK path component identity changed: %s", path)
		}
	}
	return nil
}

func validateTaskPathComponent(path string, info os.FileInfo, intermediate bool) error {
	if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return fmt.Errorf("path uses symlink component %s", path)
	}
	if intermediate && !info.IsDir() {
		return fmt.Errorf("path component %s is not a directory", path)
	}
	return nil
}

func sameTaskPathIdentity(before, after os.FileInfo) bool {
	return os.SameFile(before, after) && before.Mode() == after.Mode() &&
		before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}
