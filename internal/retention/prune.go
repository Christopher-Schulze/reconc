package retention

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/jsonl"
)

type candidate struct {
	path   string
	name   string
	size   int64
	mtime  time.Time
	active bool
	dir    bool
}

// Run executes an immediate, cross-process-serialized retention pass.
func Run(options Options) Report {
	options = normalizeOptions(options)
	if options.DryRun {
		return runLocked(options, true)
	}
	return withPruneLock(options, func() Report {
		return runLocked(options, true)
	})
}

// RunIfDue executes at most once per Policy.Interval. A not-due call performs
// one stat and one lock round-trip but writes nothing.
func RunIfDue(options Options) Report {
	options = normalizeOptions(options)
	if options.DryRun {
		marker := filepath.Join(ProjectDir(options.StateRoot, options.RepoRoot), ".last-retention")
		if info, err := os.Stat(marker); err == nil && options.Now.Sub(info.ModTime()) < options.Policy.Interval {
			return Report{DryRun: true, StateByteBudget: options.Policy.StateTotalBytes, RepoByteBudget: options.Policy.RepoRuntimeBytes, OwnedTempBudget: options.Policy.OwnedTempTotalBytes}
		}
		return runLocked(options, false)
	}
	return withPruneLock(options, func() Report {
		marker := filepath.Join(ProjectDir(options.StateRoot, options.RepoRoot), ".last-retention")
		if info, err := os.Stat(marker); err == nil && options.Now.Sub(info.ModTime()) < options.Policy.Interval {
			return Report{DryRun: options.DryRun, StateByteBudget: options.Policy.StateTotalBytes, RepoByteBudget: options.Policy.RepoRuntimeBytes, OwnedTempBudget: options.Policy.OwnedTempTotalBytes}
		}
		report := runLocked(options, false)
		if !options.DryRun {
			body := []byte(options.Now.UTC().Format(time.RFC3339Nano) + "\n")
			if _, err := atomicfile.WriteIfChanged(marker, body, 0o600); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("write retention marker: %v", err))
			}
		}
		return report
	})
}

func runLocked(options Options, forceOwnedTemp bool) Report {
	policy := options.Policy
	report := Report{
		Ran:             true,
		DryRun:          options.DryRun,
		StateByteBudget: policy.StateTotalBytes,
		RepoByteBudget:  policy.RepoRuntimeBytes,
		OwnedTempBudget: policy.OwnedTempTotalBytes,
	}
	project := ProjectDir(options.StateRoot, options.RepoRoot)
	active := liveActiveSession(project, options.ActiveSession, options.Now, policy.Locks.MaxAge)
	activeID := sessionFileID(active)

	sessions := filepath.Join(project, "sessions")
	reports := filepath.Join(project, "reports")
	locks := filepath.Join(project, "locks")
	report.Classes = append(report.Classes,
		pruneClass("sessions", sessions, policy.Sessions, options.Now, options.DryRun, map[string]bool{activeID + ".json": active != ""}, nil, &report),
		pruneClass("reports", reports, policy.Reports, options.Now, options.DryRun, map[string]bool{activeID + ".json": active != ""}, nil, &report),
		pruneClass("locks", locks, policy.Locks, options.Now, options.DryRun, map[string]bool{
			activeID + ".lock":             active != "",
			activeID + ".stop-policy.lock": active != "",
		}, nil, &report),
	)
	projectedStateBefore, projectedStateAfter := classTotals(report.Classes, "sessions", "reports", "locks")
	stateTotal := enforceStateTotal(options, project, activeID, active != "", &report)
	if options.DryRun {
		stateTotal.BytesBefore = projectedStateBefore
		stateTotal.BytesAfter = minInt64(stateTotal.BytesAfter, projectedStateAfter)
		stateTotal.BytesFreed = stateTotal.BytesBefore - stateTotal.BytesAfter
	}
	report.Classes = append(report.Classes, stateTotal)
	report.StateBytesAfter = stateTotal.BytesAfter

	auditPath := filepath.Join(options.RepoRoot, ".reconc", "audit.jsonl")
	runLoopPath := filepath.Join(options.RepoRoot, ".reconc", "runloop", "decisions.jsonl")
	report.Classes = append(report.Classes,
		enforceJSONL("audit", auditPath, policy.AuditFileBytes, policy.AuditArchives, options.DryRun, &report),
		enforceJSONL("run-decisions", runLoopPath, policy.RunLoopFileBytes, policy.RunLoopArchives, options.DryRun, &report),
	)
	cacheDir := filepath.Join(options.RepoRoot, ".reconc", "cache")
	report.Classes = append(report.Classes,
		pruneClass("generated-binaries", cacheDir, policy.GeneratedBinaries, options.Now, options.DryRun, generatedBinaryActiveNames(cacheDir, options.Now, policy.AbandonedTempAge), isGeneratedBinary, &report),
		pruneRepoTemps(options, &report),
	)
	ownedTempClass, ownedTempScanned := pruneOwnedTempRootsInterval(options, forceOwnedTemp, &report)
	report.Classes = append(report.Classes, ownedTempClass)
	projectedRepoBefore, projectedRepoAfter := classTotals(report.Classes, "audit", "run-decisions", "generated-binaries")
	repoTotal := enforceRepoTotal(options, &report)
	if options.DryRun {
		repoTotal.BytesBefore = projectedRepoBefore
		repoTotal.BytesAfter = minInt64(repoTotal.BytesAfter, projectedRepoAfter)
		repoTotal.BytesFreed = repoTotal.BytesBefore - repoTotal.BytesAfter
	}
	report.Classes = append(report.Classes, repoTotal)
	report.RepoBytesAfter = repoTotal.BytesAfter
	if ownedTempScanned {
		report.OwnedTempBytes = ownedTempClass.BytesAfter
	}
	if report.StateBytesAfter > policy.StateTotalBytes {
		report.Errors = append(report.Errors, fmt.Sprintf("protected state uses %d bytes above %d-byte total budget", report.StateBytesAfter, policy.StateTotalBytes))
	}
	if report.RepoBytesAfter > policy.RepoRuntimeBytes {
		report.Errors = append(report.Errors, fmt.Sprintf("protected repo runtime uses %d bytes above %d-byte total budget", report.RepoBytesAfter, policy.RepoRuntimeBytes))
	}
	if ownedTempScanned && report.OwnedTempBytes > policy.OwnedTempTotalBytes {
		report.Errors = append(report.Errors, fmt.Sprintf("recent owned temp trees use %d bytes above %d-byte budget; active-age grace preserved them", report.OwnedTempBytes, policy.OwnedTempTotalBytes))
	}
	return report
}

func pruneOwnedTempRootsInterval(options Options, force bool, report *Report) (ClassReport, bool) {
	class := ClassReport{Name: "abandoned-owned-temp"}
	if options.DryRun {
		return pruneOwnedTempRoots(options, report), true
	}
	if err := os.MkdirAll(options.StateRoot, 0o700); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("create global retention dir: %v", err))
		return class, false
	}
	lockPath := filepath.Join(options.StateRoot, ".owned-temp-retention.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("open global retention lock: %v", err))
		return class, false
	}
	defer lock.Close()
	unlock, err := filelock.Lock(lock)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("lock global retention: %v", err))
		return class, false
	}
	defer func() { _ = unlock() }()
	marker := filepath.Join(options.StateRoot, ".last-owned-temp-retention")
	if !force {
		if info, err := os.Stat(marker); err == nil && options.Now.Sub(info.ModTime()) < options.Policy.Interval {
			return class, false
		}
	}
	class = pruneOwnedTempRoots(options, report)
	if !options.DryRun {
		body := []byte(options.Now.UTC().Format(time.RFC3339Nano) + "\n")
		if _, err := atomicfile.WriteIfChanged(marker, body, 0o600); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("write global retention marker: %v", err))
		}
	}
	return class, true
}

func normalizeOptions(options Options) Options {
	defaults := DefaultPolicy()
	if options.Policy.Interval <= 0 {
		options.Policy = defaults
	}
	if options.StateRoot == "" {
		options.StateRoot = ResolveStateRoot()
	}
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	if options.TempRoot == "" {
		options.TempRoot = os.TempDir()
	}
	return options
}

func withPruneLock(options Options, run func() Report) Report {
	project := ProjectDir(options.StateRoot, options.RepoRoot)
	if err := os.MkdirAll(project, 0o700); err != nil {
		return Report{Errors: []string{fmt.Sprintf("create retention project dir: %v", err)}}
	}
	path := filepath.Join(project, ".retention.lock")
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Report{Errors: []string{fmt.Sprintf("open retention lock: %v", err)}}
	}
	defer lock.Close()
	unlock, err := filelock.Lock(lock)
	if err != nil {
		return Report{Errors: []string{fmt.Sprintf("lock retention: %v", err)}}
	}
	defer func() { _ = unlock() }()
	return run()
}

func pruneClass(name, dir string, policy ClassPolicy, now time.Time, dryRun bool, activeNames map[string]bool, include func(os.DirEntry) bool, report *Report) ClassReport {
	class := ClassReport{Name: name}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return class
	}
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("read %s: %v", dir, err))
		return class
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || include != nil && !include(entry) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		item := candidate{path: filepath.Join(dir, entry.Name()), name: entry.Name(), size: info.Size(), mtime: info.ModTime(), active: activeNames != nil && activeNames[entry.Name()]}
		candidates = append(candidates, item)
		class.BytesBefore += item.size
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].mtime.Equal(candidates[j].mtime) {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].mtime.After(candidates[j].mtime)
	})
	keptBytes := int64(0)
	keptFiles := 0
	for _, item := range candidates {
		expired := policy.MaxAge > 0 && now.Sub(item.mtime) > policy.MaxAge
		exceeds := policy.MaxFiles >= 0 && keptFiles >= policy.MaxFiles || policy.MaxBytes >= 0 && keptBytes+item.size > policy.MaxBytes
		if !item.active && (expired || exceeds) {
			if removeCandidate(item, dryRun, report) {
				class.FilesDeleted++
				class.BytesFreed += item.size
				continue
			}
		}
		class.FilesKept++
		class.BytesAfter += item.size
		keptFiles++
		keptBytes += item.size
	}
	return class
}

func removeCandidate(item candidate, dryRun bool, report *Report) bool {
	if dryRun {
		return true
	}
	var err error
	if item.dir {
		err = os.RemoveAll(item.path)
	} else {
		err = os.Remove(item.path)
	}
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("remove %s: %v", item.path, err))
		return false
	}
	return true
}

func liveActiveSession(project, requested string, now time.Time, maxAge time.Duration) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		data, err := os.ReadFile(filepath.Join(project, "active-session.txt"))
		if err == nil {
			requested = strings.TrimSpace(string(data))
		}
	}
	if requested == "" {
		return ""
	}
	info, err := os.Stat(filepath.Join(project, "sessions", sessionFileID(requested)+".json"))
	if err != nil || maxAge > 0 && now.Sub(info.ModTime()) > maxAge {
		return ""
	}
	return requested
}

func enforceJSONL(name, path string, maxBytes int64, archives int, dryRun bool, report *Report) ClassReport {
	class := ClassReport{Name: name}
	class.BytesBefore, class.FilesKept = jsonlRingSize(path, archives+32)
	if dryRun {
		result, err := jsonl.Inspect(path, jsonl.Policy{MaxBytes: maxBytes, MaxArchives: archives})
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("inspect %s: %v", name, err))
		}
		class.BytesFreed = result.BytesFreed
		class.FilesDeleted = result.FilesRemoved
		class.BytesAfter = class.BytesBefore - result.BytesFreed
		class.FilesKept -= result.FilesRemoved
		return class
	}
	result, err := jsonl.Enforce(path, jsonl.Policy{MaxBytes: maxBytes, MaxArchives: archives})
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("enforce %s: %v", name, err))
	}
	class.BytesFreed = result.BytesFreed
	class.FilesDeleted = result.FilesRemoved
	class.BytesAfter, class.FilesKept = jsonlRingSize(path, archives)
	return class
}

func jsonlRingSize(path string, maxArchives int) (int64, int) {
	var bytes int64
	files := 0
	for index := 0; index <= maxArchives; index++ {
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			bytes += info.Size()
			files++
		}
	}
	return bytes, files
}

func isGeneratedBinary(entry os.DirEntry) bool {
	name := entry.Name()
	return strings.HasPrefix(name, "workflow-audit-") || name == "generated-reference-audit" || name == "promote-task-done"
}

func generatedBinaryActiveNames(cacheDir string, now time.Time, grace time.Duration) map[string]bool {
	active := map[string]bool{}
	entries, _ := os.ReadDir(cacheDir)
	for _, entry := range entries {
		if entry.IsDir() || !isGeneratedBinary(entry) {
			continue
		}
		active[entry.Name()] = generatedBinaryBuildActive(cacheDir, entry.Name(), now, grace)
	}
	return active
}

func classTotals(classes []ClassReport, names ...string) (int64, int64) {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	var before, after int64
	for _, class := range classes {
		if wanted[class.Name] {
			before += class.BytesBefore
			after += class.BytesAfter
		}
	}
	return before, after
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
