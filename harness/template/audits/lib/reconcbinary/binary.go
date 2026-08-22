// Package reconcbinary verifies and snapshots the repo-local Reconc binary
// used by harness audits and claim helpers.
package reconcbinary

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"reconc.dev/reconc/buildprovenance"
)

const maxBinaryBytes = 256 << 20

// Verified is an open, source-bound identity for the canonical repo-local
// Reconc binary. Callers must close it.
type Verified struct {
	path       string
	relative   string
	file       *os.File
	identity   os.FileInfo
	provenance buildprovenance.Provenance
}

// Snapshot is a private executable copy of one Verified identity. Holding the
// open file lets callers prove that the path still identifies the bytes they
// selected before and immediately after process start.
type Snapshot struct {
	path     string
	dir      string
	file     *os.File
	identity os.FileInfo
}

// RelativePath returns the platform-specific repo-local binary path.
func RelativePath() string {
	name := "reconc-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.ToSlash(filepath.Join("tools", "reconc", "dist", name))
}

// Open validates the canonical binary's filesystem and embedded source
// identity, then keeps that exact file open. If required is false, an absent
// binary returns (nil, nil); every other defect fails closed.
func Open(root string, required bool) (*Verified, error) {
	relative := RelativePath()
	path := filepath.Join(root, filepath.FromSlash(relative))
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s is missing or cannot be inspected: %w", relative, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a non-symlink regular file", relative)
	}
	if runtime.GOOS != "windows" && before.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("%s is not executable; live agent hooks need an executable repo-local Reconc binary", relative)
	}
	if before.Size() > maxBinaryBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", relative, maxBinaryBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", relative, err)
	}
	opened, err := file.Stat()
	if err != nil || !sameIdentity(before, opened) {
		return nil, errors.Join(err, file.Close(), fmt.Errorf("%s changed while opening", relative))
	}
	verified := &Verified{path: path, relative: relative, file: file, identity: opened}
	if err := verified.Revalidate(); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	expected, err := buildprovenance.ComputeSourceDigest(
		filepath.Join(root, "tools", "reconc"), runtime.GOOS, runtime.GOARCH,
	)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("%s production source digest failed: %w", relative, err), file.Close())
	}
	provenance, err := buildprovenance.InspectOpenFile(file)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("%s has missing or malformed embedded build provenance: %w", relative, err), file.Close())
	}
	verified.provenance = provenance
	if provenance.GOOS != runtime.GOOS || provenance.GOARCH != runtime.GOARCH {
		return nil, errors.Join(fmt.Errorf(
			"%s embeds target %s/%s, want %s/%s",
			relative, provenance.GOOS, provenance.GOARCH, runtime.GOOS, runtime.GOARCH,
		), file.Close())
	}
	if provenance.SourceDigest != expected {
		return nil, errors.Join(fmt.Errorf(
			"%s source digest does not match current production inputs; rebuild the live Reconc binary before relying on agent hooks",
			relative,
		), file.Close())
	}
	if err := verified.Revalidate(); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return verified, nil
}

// Revalidate proves that the canonical path and open file still denote the
// identity that passed provenance validation.
func (verified *Verified) Revalidate() error {
	opened, fileErr := verified.file.Stat()
	current, pathErr := os.Lstat(verified.path)
	if err := errors.Join(fileErr, pathErr); err != nil {
		return fmt.Errorf("revalidate %s: %w", verified.relative, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !sameIdentity(verified.identity, opened) ||
		!sameIdentity(opened, current) {
		return fmt.Errorf("%s filesystem identity changed after verification", verified.relative)
	}
	return nil
}

// Snapshot copies the verified open file into a private directory, verifies
// the copied provenance, and revalidates the source identity after the copy.
func (verified *Verified) Snapshot() (*Snapshot, error) {
	if err := verified.Revalidate(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "reconc-task-claim-")
	if err != nil {
		return nil, fmt.Errorf("create private Reconc execution directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, errors.Join(fmt.Errorf("protect private Reconc execution directory: %w", err), os.RemoveAll(dir))
	}
	name := "reconc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	target, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create private Reconc execution snapshot: %w", err), os.RemoveAll(dir))
	}
	if _, err := verified.file.Seek(0, io.SeekStart); err != nil {
		return nil, errors.Join(err, target.Close(), os.RemoveAll(dir))
	}
	written, copyErr := io.Copy(target, io.LimitReader(verified.file, maxBinaryBytes+1))
	syncErr := target.Sync()
	closeErr := target.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return nil, errors.Join(fmt.Errorf("copy private Reconc execution snapshot: %w", err), os.RemoveAll(dir))
	}
	if written != verified.identity.Size() || written > maxBinaryBytes {
		return nil, errors.Join(fmt.Errorf("repo-local Reconc binary changed while snapshotting"), os.RemoveAll(dir))
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return nil, errors.Join(fmt.Errorf("make private Reconc execution snapshot executable: %w", err), os.RemoveAll(dir))
	}
	if err := verified.Revalidate(); err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	identity, err := file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close(), os.RemoveAll(dir))
	}
	provenance, err := buildprovenance.InspectOpenFile(file)
	if err != nil || provenance != verified.provenance {
		return nil, errors.Join(err, file.Close(), os.RemoveAll(dir), fmt.Errorf("private Reconc execution snapshot failed provenance identity verification"))
	}
	snapshot := &Snapshot{path: path, dir: dir, file: file, identity: identity}
	if err := snapshot.Revalidate(); err != nil {
		return nil, errors.Join(err, snapshot.Close())
	}
	return snapshot, nil
}

// Path returns the private executable path.
func (snapshot *Snapshot) Path() string {
	return snapshot.path
}

// Revalidate proves that the private executable path still denotes the open
// snapshot identity.
func (snapshot *Snapshot) Revalidate() error {
	opened, fileErr := snapshot.file.Stat()
	current, pathErr := os.Lstat(snapshot.path)
	if err := errors.Join(fileErr, pathErr); err != nil {
		return fmt.Errorf("revalidate private Reconc execution snapshot: %w", err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !sameIdentity(snapshot.identity, opened) ||
		!sameIdentity(opened, current) {
		return fmt.Errorf("private Reconc execution snapshot identity changed")
	}
	return nil
}

// Close releases and removes the private snapshot.
func (snapshot *Snapshot) Close() error {
	return errors.Join(snapshot.file.Close(), os.RemoveAll(snapshot.dir))
}

// Close releases the canonical binary identity.
func (verified *Verified) Close() error {
	return verified.file.Close()
}

func sameIdentity(left, right os.FileInfo) bool {
	return os.SameFile(left, right) && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
