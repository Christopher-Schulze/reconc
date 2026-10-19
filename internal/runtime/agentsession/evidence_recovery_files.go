package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
)

type abandonedEvidenceArtifact struct {
	path, parentPath string
	info, parentInfo os.FileInfo
}

// readRecoverableSessionState reads a session state during an
// operator-confirmed evidence recovery. A state file that exists but cannot
// be decoded as this session's state is set aside under the held session
// lease, and recovery continues from an empty window.
func readRecoverableSessionState(root, sessionID string, setAside *[]string) (SessionState, error) {
	state, err := readSessionStateResolved(root, sessionID)
	var unusable *unusableSessionStateError
	if !errors.As(err, &unusable) {
		return state, err
	}
	body, readErr := boundedio.ReadRegularFile(unusable.path, maxLegacySessionStateBytes)
	if readErr != nil {
		return SessionState{}, errors.Join(err, readErr)
	}
	sum := sha256.Sum256(body)
	target := unusable.path + ".unusable-" + hex.EncodeToString(sum[:])[:16]
	if renameErr := os.Rename(unusable.path, target); renameErr != nil {
		return SessionState{}, errors.Join(err, fmt.Errorf("set aside unusable session state: %w", renameErr))
	}
	if syncErr := syncParentDirectory(target); syncErr != nil {
		return SessionState{}, fmt.Errorf("persist set-aside session state: %w", syncErr)
	}
	*setAside = append(*setAside, target)
	return emptyState(root, sessionID), nil
}

func syncParentDirectory(path string) error {
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(atomicfile.SyncDirectory(parent), parent.Close())
}

func resetResolvedEvidenceWindow(root, sessionID string, setAside *[]string) error {
	artifacts, err := abandonedEvidenceArtifacts(root, sessionID)
	if err != nil {
		return err
	}
	state, err := readRecoverableSessionState(root, sessionID, setAside)
	if err != nil {
		return err
	}
	// Publish the new window before cleanup. A crash keeps the project taint
	// and its immutable receipt, so a retry can finish the owned removals.
	reset := emptyState(root, sessionID)
	reset.Runtime = state.Runtime
	if err := saveSessionStateLocked(reset); err != nil {
		return fmt.Errorf("reset reviewed evidence window: %w", err)
	}
	for _, artifact := range artifacts {
		if err := removeAbandonedEvidenceArtifact(artifact); err != nil {
			return err
		}
	}
	return nil
}

func abandonedEvidenceArtifacts(root, sessionID string) ([]abandonedEvidenceArtifact, error) {
	key := sessionFileKey(sessionID)
	commands, err := collectAbandonedEvidence(filepath.Dir(sessionStatePath(root, sessionID)), func(name string) bool {
		digest, present := strings.CutPrefix(name, key+".command-")
		return present && strings.HasSuffix(digest, ".txt") && evidenceHexIdentity(strings.TrimSuffix(digest, ".txt"), 64)
	}, false)
	if err != nil {
		return nil, err
	}
	segments := evidenceSegmentsDir(root, sessionID)
	files, err := collectAbandonedEvidence(segments, recognizedEvidenceSegmentName, true)
	if err != nil {
		return nil, err
	}
	commands = append(commands, files...)
	info, err := os.Lstat(segments)
	if errors.Is(err, os.ErrNotExist) {
		return commands, nil
	}
	if err != nil {
		return nil, err
	}
	parentInfo, err := os.Stat(filepath.Dir(segments))
	if err != nil {
		return nil, err
	}
	return append(commands, abandonedEvidenceArtifact{path: segments, parentPath: filepath.Dir(segments), info: info, parentInfo: parentInfo}), nil
}

func collectAbandonedEvidence(directory string, owned func(string) bool, rejectUnknown bool) ([]abandonedEvidenceArtifact, error) {
	entries, err := boundedio.ReadDirNoSymlink(directory, 4096)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parentInfo, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	artifacts := []abandonedEvidenceArtifact{}
	for _, entry := range entries {
		if !owned(entry.Name()) {
			if rejectUnknown {
				return nil, fmt.Errorf("evidence recovery preserved unknown entry %s", entry.Name())
			}
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.Join(fmt.Errorf("evidence recovery preserved irregular entry %s", entry.Name()), err)
		}
		artifacts = append(artifacts, abandonedEvidenceArtifact{path: filepath.Join(directory, entry.Name()), parentPath: directory, info: info, parentInfo: parentInfo})
	}
	return artifacts, nil
}

func recognizedEvidenceSegmentName(name string) bool {
	base, orphan, hasOrphan := strings.Cut(name, ".orphaned-")
	if hasOrphan && !evidenceHexIdentity(orphan, 16) {
		return false
	}
	if len(base) != 13 || !strings.HasSuffix(base, ".json") {
		return false
	}
	index, err := strconv.ParseUint(base[:8], 10, 64)
	return err == nil && index > 0 && index <= maxEvidenceSegments && fmt.Sprintf("%08d.json", index) == base
}

func removeAbandonedEvidenceArtifact(artifact abandonedEvidenceArtifact) error {
	parent, err := os.OpenRoot(artifact.parentPath)
	if err != nil {
		return err
	}
	remove := func() error {
		opened, err := parent.Stat(".")
		if err != nil || !os.SameFile(opened, artifact.parentInfo) {
			return errors.Join(errors.New("evidence recovery parent changed"), err)
		}
		currentParent, err := os.Lstat(artifact.parentPath)
		if err != nil || currentParent.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, currentParent) {
			return errors.Join(errors.New("evidence recovery parent path changed"), err)
		}
		current, err := parent.Lstat(filepath.Base(artifact.path))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || !sameAbandonedEvidence(artifact.info, current) {
			return errors.Join(errors.New("evidence recovery artifact changed"), err)
		}
		if err := parent.Remove(filepath.Base(artifact.path)); err != nil {
			return err
		}
		syncErr := atomicfile.SyncDirectory(parent)
		currentParent, err = os.Lstat(artifact.parentPath)
		if err != nil || !os.SameFile(opened, currentParent) {
			return errors.Join(syncErr, errors.New("evidence recovery parent path changed after removal"), err)
		}
		return syncErr
	}
	return errors.Join(remove(), parent.Close())
}

func sameAbandonedEvidence(before, after os.FileInfo) bool {
	if before == nil || after == nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
		return false
	}
	return before.IsDir() || before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func removeResolvedEvidenceTaint(root string) error {
	path := evidenceTaintPath(root)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	parentInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	return removeAbandonedEvidenceArtifact(abandonedEvidenceArtifact{path: path, parentPath: filepath.Dir(path), info: info, parentInfo: parentInfo})
}
