package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	PublicParentMode  os.FileMode = 0o755
	PrivateParentMode os.FileMode = 0o700
)

type boundParent struct {
	roots      []*os.Root
	components []string
	identities []os.FileInfo
}

func bindParent(path string, createMode os.FileMode) (*boundParent, string, error) {
	return bindParentDepth(path, createMode, 0)
}

func bindParentDepth(path string, createMode os.FileMode, aliasDepth int) (*boundParent, string, error) {
	if aliasDepth > 8 {
		return nil, "", fmt.Errorf("publication filesystem-root alias depth exceeded: %s", path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolve publication path: %w", err)
	}
	directory, name := filepath.Split(filepath.Clean(absolute))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return nil, "", fmt.Errorf("publication target must name a file: %s", path)
	}
	volume := filepath.VolumeName(directory)
	base := volume + string(filepath.Separator)
	if volume == "" {
		base = string(filepath.Separator)
	}
	relative, err := filepath.Rel(base, filepath.Clean(directory))
	if err != nil {
		return nil, "", fmt.Errorf("resolve publication parent %s: %w", directory, err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", fmt.Errorf("open publication filesystem root %s: %w", base, err)
	}
	bound := &boundParent{roots: []*os.Root{root}}
	if relative == "." {
		return bound, name, nil
	}
	components := strings.Split(relative, string(filepath.Separator))
	first, firstErr := root.Lstat(components[0])
	if firstErr == nil && first.Mode()&os.ModeSymlink != 0 {
		target, readErr := root.Readlink(components[0])
		closeErr := bound.close()
		if readErr != nil || closeErr != nil {
			return nil, "", errors.Join(
				fmt.Errorf("resolve publication filesystem-root alias %s", components[0]), readErr, closeErr,
			)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(base, target)
		}
		resolved := filepath.Join(append([]string{target}, components[1:]...)...)
		return bindParentDepth(filepath.Join(resolved, name), createMode, aliasDepth+1)
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return nil, "", errors.Join(
				fmt.Errorf("invalid publication parent component %q", component), bound.close(),
			)
		}
		parent := bound.directory()
		if err := parent.Mkdir(component, createMode.Perm()); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, "", errors.Join(
				fmt.Errorf("create publication parent component %s: %w", component, err), bound.close(),
			)
		}
		before, err := parent.Lstat(component)
		if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			return nil, "", errors.Join(
				fmt.Errorf("publication parent component %s must be a non-symlink directory", component),
				err, bound.close(),
			)
		}
		child, err := parent.OpenRoot(component)
		if err != nil {
			return nil, "", errors.Join(
				fmt.Errorf("open publication parent component %s: %w", component, err), bound.close(),
			)
		}
		opened, statErr := child.Stat(".")
		after, lstatErr := parent.Lstat(component)
		if statErr != nil || lstatErr != nil || after.Mode()&os.ModeSymlink != 0 ||
			!opened.IsDir() || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
			return nil, "", errors.Join(
				fmt.Errorf("publication parent component %s changed identity while opening", component),
				statErr, lstatErr, child.Close(), bound.close(),
			)
		}
		bound.components = append(bound.components, component)
		bound.identities = append(bound.identities, opened)
		bound.roots = append(bound.roots, child)
	}
	return bound, name, nil
}

func (parent *boundParent) directory() *os.Root {
	return parent.roots[len(parent.roots)-1]
}

func (parent *boundParent) validate() error {
	for index, component := range parent.components {
		current, err := parent.roots[index].Lstat(component)
		opened, statErr := parent.roots[index+1].Stat(".")
		if err != nil || statErr != nil || current.Mode()&os.ModeSymlink != 0 ||
			!current.IsDir() || !opened.IsDir() ||
			!os.SameFile(parent.identities[index], current) || !os.SameFile(current, opened) {
			return errors.Join(
				fmt.Errorf("publication parent component %s changed identity", component), err, statErr,
			)
		}
	}
	return nil
}

func (parent *boundParent) close() error {
	var result error
	for index := len(parent.roots) - 1; index >= 0; index-- {
		result = errors.Join(result, parent.roots[index].Close())
	}
	return result
}
