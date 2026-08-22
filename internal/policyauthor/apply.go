package policyauthor

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/bootstrap"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/runtime"
)

type fileSnapshot struct {
	exists bool
	body   []byte
	mode   os.FileMode
	info   os.FileInfo
}

func Apply(request Request, expected Preview) (Adoption, error) {
	return apply(request, expected, runtime.ValidatePolicyLockfile)
}

func apply(request Request, expected Preview, verify func(string) error) (adoption Adoption, resultErr error) {
	request.Body = append([]byte(nil), request.Body...)
	adoption = Adoption{Requested: true, Confirmed: true, Target: expected.Target}
	if !expected.Validation.Ready {
		return adoption, fmt.Errorf("candidate has unresolved static conflicts")
	}
	err := bootstrap.WithRepositoryTransaction(expected.physicalRoot, func(root string) error {
		fresh, err := Prepare(request)
		if err != nil {
			return err
		}
		freshRoot, err := pathidentity.ResolveExisting(fresh.physicalRoot)
		if err != nil {
			return fmt.Errorf("resolve prepared repository identity: %w", err)
		}
		if freshRoot != root || fresh.Target != expected.Target ||
			fresh.CandidateSHA256 != expected.CandidateSHA256 ||
			fresh.BaseSourceDigest != expected.BaseSourceDigest ||
			fresh.CompiledSourceDigest != expected.CompiledSourceDigest ||
			!bytes.Equal(fresh.lockfile, expected.lockfile) {
			return fmt.Errorf("repository or candidate changed after preview; retry")
		}
		targetPath := filepath.Join(root, filepath.FromSlash(fresh.Target))
		targetParent := filepath.Dir(targetPath)
		lockPath := filepath.Join(root, filepath.FromSlash(ingest.LockfilePath))
		parentExisted, err := realDirectoryExists(targetParent)
		if err != nil {
			return fmt.Errorf("inspect policy target directory: %w", err)
		}
		targetBefore, err := snapshotOptional(targetPath, MaxCandidateBytes)
		if err != nil {
			return fmt.Errorf("snapshot policy target: %w", err)
		}
		lockBefore, err := snapshotOptional(lockPath, compiler.MaxLockfileBytes)
		if err != nil {
			return fmt.Errorf("snapshot policy lock: %w", err)
		}
		if lockBefore.exists {
			adoption.PreviousLockSHA256 = digest(lockBefore.body)
		}
		mode := os.FileMode(0o644)
		if targetBefore.exists {
			mode = targetBefore.mode.Perm()
		}
		if _, err := atomicfile.WriteIfChanged(targetPath, request.Body, mode); err != nil {
			return fmt.Errorf("publish policy target: %w", err)
		}
		targetAfter, err := snapshotOptional(targetPath, MaxCandidateBytes)
		if err != nil {
			rollbackErr := restoreSnapshot(targetPath, MaxCandidateBytes, targetBefore, fileSnapshot{})
			return errors.Join(fmt.Errorf("snapshot published policy target: %w", err), rollbackErr)
		}
		rollback := func(cause error, lockAfter fileSnapshot) error {
			rollbackErr := errors.Join(
				restoreSnapshot(lockPath, compiler.MaxLockfileBytes, lockBefore, lockAfter),
				restoreSnapshot(targetPath, MaxCandidateBytes, targetBefore, targetAfter),
				removeOwnedEmptyDirectory(targetParent, parentExisted),
			)
			adoption.RolledBack = rollbackErr == nil
			return errors.Join(cause, rollbackErr)
		}
		compiled, err := compiler.CompileRepoPolicy(root, request.Version)
		if err != nil {
			lockAfter, snapshotErr := snapshotOptional(lockPath, compiler.MaxLockfileBytes)
			return rollback(errors.Join(fmt.Errorf("recompile adopted policy: %w", err), snapshotErr), lockAfter)
		}
		lockAfter, snapshotErr := snapshotOptional(lockPath, compiler.MaxLockfileBytes)
		if snapshotErr != nil {
			return rollback(fmt.Errorf("snapshot adopted policy lock: %w", snapshotErr), fileSnapshot{})
		}
		if compiled.SourceDigest != fresh.CompiledSourceDigest || !bytes.Equal(lockAfter.body, fresh.lockfile) {
			return rollback(fmt.Errorf("adopted policy compile differs from preview"), lockAfter)
		}
		if err := verify(root); err != nil {
			return rollback(fmt.Errorf("verify adopted policy: %w", err), lockAfter)
		}
		adoption.Applied = true
		adoption.LockSHA256 = digest(lockAfter.body)
		return nil
	})
	return adoption, err
}

func realDirectoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("%s must be a non-symlink directory", path)
	}
	return true, nil
}

func removeOwnedEmptyDirectory(path string, existedBefore bool) error {
	if existedBefore {
		return nil
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func snapshotOptional(path string, maxBytes int64) (fileSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fileSnapshot{}, fmt.Errorf("%s must be a non-symlink regular file", path)
	}
	body, opened, err := boundedio.ReadRegularFileSnapshot(path, maxBytes)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{exists: true, body: body, mode: opened.Mode(), info: opened}, nil
}

func restoreSnapshot(path string, maxBytes int64, before, expectedCurrent fileSnapshot) error {
	current, err := snapshotOptional(path, maxBytes)
	if err != nil {
		return fmt.Errorf("inspect rollback target %s: %w", path, err)
	}
	if before.exists == current.exists && bytes.Equal(before.body, current.body) {
		return nil
	}
	if !expectedCurrent.exists || !current.exists || !os.SameFile(expectedCurrent.info, current.info) ||
		!bytes.Equal(expectedCurrent.body, current.body) {
		return fmt.Errorf("refuse rollback because %s changed after publication", path)
	}
	if before.exists {
		_, err = atomicfile.WriteIfChanged(path, before.body, before.mode.Perm())
		return err
	}
	return os.Remove(path)
}
