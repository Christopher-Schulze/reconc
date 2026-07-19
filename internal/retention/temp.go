package retention

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func enforceStateTotal(options Options, project, activeID string, hasActive bool, report *Report) ClassReport {
	class := ClassReport{Name: "state-total"}
	candidates := []candidate{}
	for _, dir := range []string{"sessions", "reports", "locks", "command-proofs"} {
		path := filepath.Join(project, dir)
		entries, err := os.ReadDir(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				report.Errors = append(report.Errors, fmt.Sprintf("read state directory %s: %v", path, err))
				return class
			}
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("stat state entry %s: %v", filepath.Join(path, entry.Name()), err))
				return class
			}
			if !info.Mode().IsRegular() {
				continue
			}
			active := hasActive && (dir == "sessions" || dir == "reports") && entry.Name() == activeID+".json"
			active = active || hasActive && dir == "locks" && (entry.Name() == activeID+".lock" || entry.Name() == activeID+".stop-policy.lock")
			item := candidate{path: filepath.Join(path, entry.Name()), name: dir + "/" + entry.Name(), size: info.Size(), mtime: info.ModTime(), active: active}
			candidates = append(candidates, item)
			class.BytesBefore += item.size
		}
	}
	class.BytesAfter = class.BytesBefore
	class.FilesKept = len(candidates)
	if class.BytesAfter <= options.Policy.StateTotalBytes {
		return class
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].mtime.Equal(candidates[j].mtime) {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].mtime.Before(candidates[j].mtime)
	})
	for _, item := range candidates {
		if class.BytesAfter <= options.Policy.StateTotalBytes {
			break
		}
		if item.active || !removeCandidate(item, options.DryRun, report) {
			continue
		}
		class.BytesAfter -= item.size
		class.BytesFreed += item.size
		class.FilesDeleted++
		class.FilesKept--
	}
	return class
}

func pruneRepoTemps(options Options, report *Report) ClassReport {
	class := ClassReport{Name: "abandoned-repo-temp"}
	root := filepath.Join(options.RepoRoot, ".reconc")
	var candidates []candidate
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasSuffix(name, ".build.lock") {
				info, err := entry.Info()
				if err == nil {
					item := candidate{path: path, name: name, mtime: info.ModTime(), dir: true}
					var treeErr error
					item.size, item.mtime, treeErr = treeSizeAndLatest(path, info.ModTime())
					if treeErr != nil {
						return treeErr
					}
					candidates = append(candidates, item)
				}
				return filepath.SkipDir
			}
			return nil
		}
		if !isOwnedTempName(name) {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			candidates = append(candidates, candidate{path: path, name: name, size: info.Size(), mtime: info.ModTime()})
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		report.Errors = append(report.Errors, fmt.Sprintf("walk repo temp root %s: %v", root, err))
		return class
	}
	return pruneExpiredCandidates(class, candidates, options.Now, options.Policy.AbandonedTempAge, options.DryRun, report)
}

func pruneOwnedTempRoots(options Options, report *Report) ClassReport {
	class := ClassReport{Name: "abandoned-owned-temp"}
	entries, err := os.ReadDir(options.TempRoot)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			report.Errors = append(report.Errors, fmt.Sprintf("read temp root %s: %v", options.TempRoot, err))
		}
		return class
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() || !isOwnedTempRoot(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("stat owned temp %s: %v", filepath.Join(options.TempRoot, entry.Name()), err))
			return class
		}
		size, latest, err := treeSizeAndLatest(filepath.Join(options.TempRoot, entry.Name()), info.ModTime())
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("walk owned temp %s: %v", filepath.Join(options.TempRoot, entry.Name()), err))
			return class
		}
		candidates = append(candidates, candidate{path: filepath.Join(options.TempRoot, entry.Name()), name: entry.Name(), size: size, mtime: latest, dir: true})
	}
	return pruneExpiredCandidates(class, candidates, options.Now, options.Policy.AbandonedTempAge, options.DryRun, report)
}

func pruneExpiredCandidates(class ClassReport, candidates []candidate, now time.Time, maxAge time.Duration, dryRun bool, report *Report) ClassReport {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].mtime.Equal(candidates[j].mtime) {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].mtime.Before(candidates[j].mtime)
	})
	for _, item := range candidates {
		class.BytesBefore += item.size
		if maxAge > 0 && now.Sub(item.mtime) > maxAge && removeCandidate(item, dryRun, report) {
			class.FilesDeleted++
			class.BytesFreed += item.size
			continue
		}
		class.FilesKept++
		class.BytesAfter += item.size
	}
	return class
}

func enforceRepoTotal(options Options, report *Report) ClassReport {
	class := ClassReport{Name: "repo-runtime-total"}
	bytesBefore, err := ownedRepoRuntimeBytes(options.RepoRoot)
	class.BytesBefore = bytesBefore
	class.BytesAfter = class.BytesBefore
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return class
	}
	if class.BytesAfter <= options.Policy.RepoRuntimeBytes {
		return class
	}
	var removable []candidate
	cache := filepath.Join(options.RepoRoot, ".reconc", "cache")
	entries, err := os.ReadDir(cache)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		report.Errors = append(report.Errors, fmt.Sprintf("read runtime cache %s: %v", cache, err))
		return class
	}
	for _, entry := range entries {
		if entry.IsDir() || !isGeneratedBinary(entry) {
			continue
		}
		active, activeErr := generatedBinaryBuildActive(cache, entry.Name(), options.Now, options.Policy.AbandonedTempAge)
		if activeErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("inspect generated binary lock %s: %v", entry.Name(), activeErr))
			return class
		}
		if active {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("stat generated binary %s: %v", filepath.Join(cache, entry.Name()), err))
			return class
		}
		removable = append(removable, candidate{path: filepath.Join(cache, entry.Name()), name: entry.Name(), size: info.Size(), mtime: info.ModTime()})
	}
	for _, base := range []string{
		filepath.Join(options.RepoRoot, ".reconc", "audit.jsonl"),
		filepath.Join(options.RepoRoot, ".reconc", "run", "decisions.jsonl"),
	} {
		for index := 1; index <= 32; index++ {
			path := fmt.Sprintf("%s.%d", base, index)
			info, err := os.Stat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("stat runtime archive %s: %v", path, err))
				return class
			}
			removable = append(removable, candidate{path: path, name: filepath.Base(path), size: info.Size(), mtime: info.ModTime()})
		}
	}
	sort.Slice(removable, func(i, j int) bool {
		if removable[i].mtime.Equal(removable[j].mtime) {
			return removable[i].name < removable[j].name
		}
		return removable[i].mtime.Before(removable[j].mtime)
	})
	for _, item := range removable {
		if class.BytesAfter <= options.Policy.RepoRuntimeBytes {
			break
		}
		if !removeCandidate(item, options.DryRun, report) {
			continue
		}
		class.BytesAfter -= item.size
		class.BytesFreed += item.size
		class.FilesDeleted++
	}
	return class
}

func generatedBinaryBuildActive(cacheDir, name string, now time.Time, grace time.Duration) (bool, error) {
	info, err := os.Stat(filepath.Join(cacheDir, name+".build.lock"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return now.Sub(info.ModTime()) <= grace, nil
}

func isOwnedTempName(name string) bool {
	return strings.HasSuffix(name, ".tmp") || strings.Contains(name, ".tmp.") || strings.HasPrefix(name, ".audit-jsonl-")
}

func isOwnedTempRoot(name string) bool {
	return strings.HasPrefix(name, "reconc-proof-neg-") || strings.HasPrefix(name, "reconc-proof-neg-copy-") || strings.HasPrefix(name, "reconc-proof-gocache-")
}

func treeSizeAndLatest(root string, initial time.Time) (int64, time.Time, error) {
	var size int64
	latest := initial
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return size, latest, err
}

func ownedRepoRuntimeBytes(repoRoot string) (int64, error) {
	var total int64
	for _, base := range []string{
		filepath.Join(repoRoot, ".reconc", "audit.jsonl"),
		filepath.Join(repoRoot, ".reconc", "run", "decisions.jsonl"),
	} {
		for index := 0; index <= 32; index++ {
			path := base
			if index > 0 {
				path = fmt.Sprintf("%s.%d", base, index)
			}
			info, err := os.Stat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return total, fmt.Errorf("stat runtime JSONL %s: %w", path, err)
			}
			if info.Mode().IsRegular() {
				total += info.Size()
			}
		}
	}
	cacheDir := filepath.Join(repoRoot, ".reconc", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return total, nil
		}
		return total, fmt.Errorf("read runtime cache %s: %w", cacheDir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && isGeneratedBinary(entry) {
			info, err := entry.Info()
			if err != nil {
				return total, fmt.Errorf("stat runtime cache entry %s: %w", filepath.Join(cacheDir, entry.Name()), err)
			}
			total += info.Size()
		}
	}
	return total, nil
}
