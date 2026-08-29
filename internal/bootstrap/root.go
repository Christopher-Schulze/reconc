package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type bootstrapRootRef struct {
	root *os.Root
}

func closeBootstrapRootRef(ref *bootstrapRootRef) error {
	if ref == nil || ref.root == nil {
		return nil
	}
	err := ref.root.Close()
	ref.root = nil
	return err
}

func openBootstrapRoot(path string) (*bootstrapRootRef, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, nil, fmt.Errorf("bootstrap repository root must be a real directory: %s", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open bootstrap repository root: %w", err)
	}
	opened, statErr := root.Stat(".")
	after, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.IsDir() || after.Mode()&os.ModeSymlink != 0 ||
		!after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, nil, errors.Join(
			fmt.Errorf("bootstrap repository root changed identity while opening: %s", path),
			statErr, lstatErr, root.Close(),
		)
	}
	return &bootstrapRootRef{root: root}, opened, nil
}

func bootstrapRootPath(root, target string) (string, error) {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bootstrap path escapes repository: %s", target)
	}
	if relative == "." {
		return relative, nil
	}
	components := strings.Split(relative, string(filepath.Separator))
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("invalid bootstrap path component %q", component)
		}
	}
	return relative, nil
}

func bootstrapRootRefReferenced(directories []createdDirectory, ref *bootstrapRootRef) bool {
	if ref == nil {
		return false
	}
	for _, directory := range directories {
		if directory.parentRef == ref {
			return true
		}
	}
	return false
}

func closeUnretainedBootstrapRootRefs(protected *bootstrapRootRef, directories []createdDirectory, refs ...*bootstrapRootRef) error {
	seen := map[*bootstrapRootRef]bool{}
	var closeErr error
	for _, ref := range refs {
		if ref == nil || seen[ref] || ref == protected || bootstrapRootRefReferenced(directories, ref) {
			continue
		}
		seen[ref] = true
		closeErr = errors.Join(closeErr, closeBootstrapRootRef(ref))
	}
	return closeErr
}

func createSafeParentsWithRoot(
	rootPath string,
	rootRef *bootstrapRootRef,
	parentPath string,
) (finalRef *bootstrapRootRef, finalInfo os.FileInfo, created []createdDirectory, resultErr error) {
	if rootRef == nil || rootRef.root == nil {
		return nil, nil, nil, errors.New("bootstrap repository root handle is unavailable")
	}
	relative, err := bootstrapRootPath(rootPath, parentPath)
	if err != nil {
		return nil, nil, nil, err
	}
	currentRef := rootRef
	currentInfo, err := rootRef.root.Stat(".")
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() {
		if resultErr != nil {
			_ = closeUnretainedBootstrapRootRefs(rootRef, created, currentRef)
		}
	}()
	if relative == "." {
		return currentRef, currentInfo, []createdDirectory{}, nil
	}
	currentRetained := true
	components := strings.Split(relative, string(filepath.Separator))
	for componentIndex, component := range components {
		if component == "" || component == "." || component == ".." {
			return nil, nil, created, fmt.Errorf("invalid bootstrap parent component %q", component)
		}
		parent := currentRef.root
		parentInfo, err := parent.Stat(".")
		if err != nil {
			return nil, nil, created, err
		}
		before, err := parent.Lstat(component)
		createdHere := false
		if errors.Is(err, os.ErrNotExist) {
			mkdirErr := parent.Mkdir(component, 0o755)
			if mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				return nil, nil, created, fmt.Errorf("create bootstrap parent %s: %w", component, mkdirErr)
			}
			createdHere = mkdirErr == nil
			before, err = parent.Lstat(component)
		}
		if err != nil {
			return nil, nil, created, fmt.Errorf("inspect bootstrap parent %s: %w", component, err)
		}
		if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			return nil, nil, created, fmt.Errorf("bootstrap parent is not a real directory: %s", component)
		}
		child, err := parent.OpenRoot(component)
		if err != nil {
			return nil, nil, created, fmt.Errorf("open bootstrap parent %s: %w", component, err)
		}
		opened, statErr := child.Stat(".")
		after, lstatErr := parent.Lstat(component)
		if statErr != nil || lstatErr != nil || !opened.IsDir() || after.Mode()&os.ModeSymlink != 0 ||
			!after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
			return nil, nil, created, errors.Join(
				fmt.Errorf("bootstrap parent %s changed identity while opening", component),
				statErr, lstatErr, child.Close(),
			)
		}
		childRef := &bootstrapRootRef{root: child}
		if createdHere {
			path := filepath.Join(rootPath, strings.Join(components[:componentIndex+1], string(filepath.Separator)))
			identity, identityErr := captureDirectoryIdentityFromRoot(child, path)
			if identityErr != nil {
				return nil, nil, created, errors.Join(identityErr, child.Close())
			}
			created = append(created, createdDirectory{
				path: path, identity: identity, parent: parent, parentInfo: parentInfo,
				name: component, parentRef: currentRef,
			})
			if syncErr := syncBoundBootstrapParent(parent, parentInfo); syncErr != nil {
				return nil, nil, created, errors.Join(
					fmt.Errorf("commit created bootstrap parent %s: %w", path, syncErr),
					child.Close(),
				)
			}
			currentRetained = true
		}
		if currentRef != rootRef && !currentRetained {
			_ = closeBootstrapRootRef(currentRef)
		}
		currentRef = childRef
		currentInfo = opened
		currentRetained = false
	}
	return currentRef, currentInfo, created, nil
}
