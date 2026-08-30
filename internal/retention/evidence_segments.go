package retention

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"reconc.dev/reconc/internal/boundedio"
)

const (
	maxEvidenceSegmentDirectories = 16_384
	maxEvidenceSegmentsPerSession = 64
	maxEvidenceSegmentBytes       = 1024 * 1024
	maxEvidenceChainBytes         = maxEvidenceSegmentsPerSession * maxEvidenceSegmentBytes
)

func pruneEvidenceSegmentDirectories(
	dir string,
	policy ClassPolicy,
	now time.Time,
	dryRun bool,
	protected map[string]bool,
	protectAll bool,
	report *Report,
) ClassReport {
	class := ClassReport{Name: "evidence-segments"}
	candidates, ok := evidenceSegmentCandidates(dir, protected, protectAll, report)
	if !ok {
		return class
	}
	for _, item := range candidates {
		class.BytesBefore += item.size
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].mtime.Equal(candidates[j].mtime) {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].mtime.After(candidates[j].mtime)
	})
	keptDirectories := 0
	keptBytes := int64(0)
	for _, item := range candidates {
		expired := policy.MaxAge > 0 && now.Sub(item.mtime) > policy.MaxAge
		exceeds := policy.MaxFiles >= 0 && keptDirectories >= policy.MaxFiles ||
			policy.MaxBytes >= 0 && keptBytes+item.size > policy.MaxBytes
		if !item.active && (expired || exceeds) && removeCandidate(item, dryRun, report) {
			class.FilesDeleted++
			class.BytesFreed += item.size
			continue
		}
		class.FilesKept++
		class.BytesAfter += item.size
		keptDirectories++
		keptBytes += item.size
	}
	return class
}

func evidenceSegmentCandidates(
	dir string,
	protected map[string]bool,
	protectAll bool,
	report *Report,
) ([]candidate, bool) {
	entries, err := boundedio.ReadDirNoSymlink(dir, maxEvidenceSegmentDirectories)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true
	}
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("read evidence-segments: %v", err))
		return nil, false
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, infoErr := entry.Info()
		if infoErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("stat evidence-segments entry %s: %v", path, infoErr))
			return nil, false
		}
		if !entry.IsDir() || info.Mode()&os.ModeSymlink != 0 || !validSessionDirectoryName(entry.Name()) {
			report.Errors = append(report.Errors, fmt.Sprintf("inspect evidence-segments entry %s: not an owned session directory", path))
			return nil, false
		}
		size, latest, inspectErr := inspectEvidenceSegmentDirectory(path, info)
		if inspectErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("inspect evidence-segments entry %s: %v", path, inspectErr))
			return nil, false
		}
		expectedSize := size
		item := candidate{
			path: path, name: entry.Name(), size: size, mtime: latest, dir: true, info: info,
			active:    protectAll || protected[entry.Name()],
			leasePath: filepath.Join(filepath.Dir(dir), "locks", entry.Name()+".lock"),
		}
		item.validate = func(path, _ string, current os.FileInfo) error {
			revalidatedSize, _, err := inspectEvidenceSegmentDirectory(path, current)
			if err != nil {
				return err
			}
			if revalidatedSize != expectedSize {
				return errors.New("evidence segment directory changed size before deletion")
			}
			return nil
		}
		candidates = append(candidates, item)
	}
	return candidates, true
}

func inspectEvidenceSegmentDirectory(path string, discovered os.FileInfo) (int64, time.Time, error) {
	current, err := os.Lstat(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	if discovered == nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(discovered, current) {
		return 0, time.Time{}, errors.New("evidence segment directory changed identity")
	}
	entries, err := boundedio.ReadDirNoSymlink(path, maxEvidenceSegmentsPerSession)
	if err != nil {
		return 0, time.Time{}, err
	}
	latest := current.ModTime()
	var total int64
	for index, entry := range entries {
		wantName := fmt.Sprintf("%08d.json", index+1)
		if entry.Name() != wantName || entry.IsDir() {
			return 0, time.Time{}, fmt.Errorf("expected chain member %s, found %s", wantName, entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return 0, time.Time{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return 0, time.Time{}, fmt.Errorf("chain member %s is not a non-symlink regular file", entry.Name())
		}
		if info.Size() < 0 || info.Size() > maxEvidenceSegmentBytes {
			return 0, time.Time{}, fmt.Errorf("chain member %s exceeds %d bytes", entry.Name(), maxEvidenceSegmentBytes)
		}
		total += info.Size()
		if total > maxEvidenceChainBytes {
			return 0, time.Time{}, fmt.Errorf("evidence chain exceeds %d bytes", maxEvidenceChainBytes)
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(current, after) || current.Mode() != after.Mode() {
		return 0, time.Time{}, errors.Join(err, errors.New("evidence segment directory changed during inspection"))
	}
	return total, latest, nil
}

func validSessionDirectoryName(name string) bool {
	return validSessionArtifactName(name + ".json")
}

func cloneProtectedNames(values map[string]bool) map[string]bool {
	clone := make(map[string]bool, len(values)+1)
	for name, protected := range values {
		if protected {
			clone[name] = true
		}
	}
	return clone
}
