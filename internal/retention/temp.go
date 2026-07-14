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
	for _, dir := range []string{"sessions", "reports", "locks"} {
		path := filepath.Join(project, dir)
		entries, _ := os.ReadDir(path)
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
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
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || path == root {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasSuffix(name, ".build.lock") {
				info, err := entry.Info()
				if err == nil {
					item := candidate{path: path, name: name, mtime: info.ModTime(), dir: true}
					item.size, item.mtime = treeSizeAndLatest(path, info.ModTime())
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
			continue
		}
		size, latest := treeSizeAndLatest(filepath.Join(options.TempRoot, entry.Name()), info.ModTime())
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
	class.BytesBefore = ownedRepoRuntimeBytes(options.RepoRoot)
	class.BytesAfter = class.BytesBefore
	if class.BytesAfter <= options.Policy.RepoRuntimeBytes {
		return class
	}
	var removable []candidate
	cache := filepath.Join(options.RepoRoot, ".reconc", "cache")
	entries, _ := os.ReadDir(cache)
	for _, entry := range entries {
		if entry.IsDir() || !isGeneratedBinary(entry) || generatedBinaryBuildActive(cache, entry.Name(), options.Now, options.Policy.AbandonedTempAge) {
			continue
		}
		info, err := entry.Info()
		if err == nil {
			removable = append(removable, candidate{path: filepath.Join(cache, entry.Name()), name: entry.Name(), size: info.Size(), mtime: info.ModTime()})
		}
	}
	for _, base := range []string{
		filepath.Join(options.RepoRoot, ".reconc", "audit.jsonl"),
		filepath.Join(options.RepoRoot, ".reconc", "run", "decisions.jsonl"),
	} {
		for index := 1; index <= 32; index++ {
			path := fmt.Sprintf("%s.%d", base, index)
			info, err := os.Stat(path)
			if err == nil {
				removable = append(removable, candidate{path: path, name: filepath.Base(path), size: info.Size(), mtime: info.ModTime()})
			}
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

func generatedBinaryBuildActive(cacheDir, name string, now time.Time, grace time.Duration) bool {
	info, err := os.Stat(filepath.Join(cacheDir, name+".build.lock"))
	return err == nil && now.Sub(info.ModTime()) <= grace
}

func isOwnedTempName(name string) bool {
	return strings.HasSuffix(name, ".tmp") || strings.Contains(name, ".tmp.") || strings.HasPrefix(name, ".audit-jsonl-")
}

func isOwnedTempRoot(name string) bool {
	return strings.HasPrefix(name, "reconc-proof-neg-") || strings.HasPrefix(name, "reconc-proof-neg-copy-") || strings.HasPrefix(name, "reconc-proof-gocache-")
}

func treeSizeAndLatest(root string, initial time.Time) (int64, time.Time) {
	var size int64
	latest := initial
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return size, latest
}

func ownedRepoRuntimeBytes(repoRoot string) int64 {
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
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				total += info.Size()
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Join(repoRoot, ".reconc", "cache"))
	for _, entry := range entries {
		if !entry.IsDir() && isGeneratedBinary(entry) {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
	}
	return total
}
