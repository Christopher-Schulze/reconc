//go:build !windows

package atomicfile

import "os"

func reconcileMode(_ *os.Root, _ string, file *os.File, current, requested os.FileMode) (bool, error) {
	if current.Perm() == requested.Perm() {
		return false, nil
	}
	if err := file.Chmod(requested); err != nil {
		return false, err
	}
	return true, nil
}

// syncReconciledMode persists a mode change through the descriptor that made
// it and reports whether the change is durable at the publication's level.
func syncReconciledMode(file *os.File, durability publicationDurability) (bool, error) {
	if err := syncTemporary(file, durability); err != nil {
		return false, err
	}
	return durability == durabilityFull, nil
}
