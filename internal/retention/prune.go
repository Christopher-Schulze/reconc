package retention

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/audit"
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
			return emptyReport(options, true)
		}
		return runLocked(options, false)
	}
	return withPruneLock(options, func() Report {
		marker := filepath.Join(ProjectDir(options.StateRoot, options.RepoRoot), ".last-retention")
		if info, err := os.Stat(marker); err == nil && options.Now.Sub(info.ModTime()) < options.Policy.Interval {
			return emptyReport(options, options.DryRun)
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
		Ran:                true,
		DryRun:             options.DryRun,
		ProjectStateBudget: policy.ProjectRoots.MaxBytes,
		StateByteBudget:    policy.StateTotalBytes,
		RepoByteBudget:     policy.RepoRuntimeBytes,
		OwnedTempBudget:    policy.OwnedTempTotalBytes,
	}
	project := ProjectDir(options.StateRoot, options.RepoRoot)
	active, activeErr := liveActiveSession(project, options.ActiveSession, options.Now, policy.Locks.MaxAge)
	stateOptions := options
	if activeErr != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("resolve active session: %v", activeErr))
		noDelete := ClassPolicy{MaxFiles: -1, MaxBytes: -1}
		stateOptions.Policy.Sessions = noDelete
		stateOptions.Policy.Reports = noDelete
		stateOptions.Policy.Locks = noDelete
		stateOptions.Policy.CommandProofs = noDelete
		stateOptions.Policy.PolicyDecisions = noDelete
		stateOptions.Policy.StateTotalBytes = int64(^uint64(0) >> 1)
	}
	activeID := SessionFileID(active)

	sessions := filepath.Join(project, "sessions")
	reports := filepath.Join(project, "reports")
	locks := filepath.Join(project, "locks")
	commandProofs := filepath.Join(project, "command-proofs")
	policyDecisions := filepath.Join(project, "policy-decisions")
	report.Classes = append(report.Classes,
		pruneClass("sessions", sessions, stateOptions.Policy.Sessions, options.Now, options.DryRun, map[string]bool{activeID + ".json": active != ""}, nil, &report),
		pruneClass("reports", reports, stateOptions.Policy.Reports, options.Now, options.DryRun, map[string]bool{activeID + ".json": active != ""}, nil, &report),
		pruneClass("locks", locks, stateOptions.Policy.Locks, options.Now, options.DryRun, map[string]bool{
			activeID + ".lock":             active != "",
			activeID + ".stop-policy.lock": active != "",
		}, nil, &report),
		pruneClass("command-proofs", commandProofs, stateOptions.Policy.CommandProofs, options.Now, options.DryRun, nil, nil, &report),
		pruneClass("policy-decisions", policyDecisions, stateOptions.Policy.PolicyDecisions, options.Now, options.DryRun, map[string]bool{"latest.json": true}, nil, &report),
	)
	projectedStateBefore, projectedStateAfter := classTotals(report.Classes, "sessions", "reports", "locks", "command-proofs", "policy-decisions")
	stateTotal := enforceStateTotal(stateOptions, project, activeID, active != "", &report)
	if options.DryRun {
		stateTotal.BytesBefore = projectedStateBefore
		stateTotal.BytesAfter = minInt64(stateTotal.BytesAfter, projectedStateAfter)
		stateTotal.BytesFreed = stateTotal.BytesBefore - stateTotal.BytesAfter
	}
	report.Classes = append(report.Classes, stateTotal)
	report.StateBytesAfter = stateTotal.BytesAfter

	runDecisionPath := filepath.Join(options.RepoRoot, ".reconc", "run", "decisions.jsonl")
	report.Classes = append(report.Classes,
		inspectChainedAudit("audit", options.RepoRoot, policy.AuditFileBytes, policy.AuditArchives, &report),
		enforceJSONL("run-decisions", runDecisionPath, policy.RunDecisionFileBytes, policy.RunDecisionArchives, options.DryRun, &report),
	)
	cacheDir := filepath.Join(options.RepoRoot, ".reconc", "cache")
	generatedActive, generatedActiveErr := generatedBinaryActiveNames(cacheDir, options.Now, policy.AbandonedTempAge)
	generatedPolicy := policy.GeneratedBinaries
	if generatedActiveErr != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect generated binary activity: %v", generatedActiveErr))
		generatedPolicy = ClassPolicy{MaxFiles: -1, MaxBytes: -1}
	}
	report.Classes = append(report.Classes,
		pruneClass("generated-binaries", cacheDir, generatedPolicy, options.Now, options.DryRun, generatedActive, isGeneratedBinary, &report),
		pruneRepoTemps(options, &report),
	)
	ownedTempClass, ownedTempScanned := pruneOwnedTempRootsInterval(options, forceOwnedTemp, &report)
	projectRootsClass, projectRootsScanned := pruneProjectRootsInterval(options, forceOwnedTemp, &report)
	report.Classes = append(report.Classes, ownedTempClass, projectRootsClass)
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
	if projectRootsScanned {
		report.ProjectStateBytes = projectRootsClass.BytesAfter
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
	overProjectBytes := policy.ProjectRoots.MaxBytes >= 0 && report.ProjectStateBytes > policy.ProjectRoots.MaxBytes
	overProjectCount := policy.ProjectRoots.MaxFiles >= 0 && projectRootsClass.FilesKept > policy.ProjectRoots.MaxFiles
	if projectRootsScanned && (overProjectBytes || overProjectCount) {
		report.Errors = append(report.Errors, fmt.Sprintf("protected project state uses %d bytes in %d roots above the %d-byte/%d-root global budget", report.ProjectStateBytes, projectRootsClass.FilesKept, policy.ProjectRoots.MaxBytes, policy.ProjectRoots.MaxFiles))
	}
	return report
}

func emptyReport(options Options, dryRun bool) Report {
	return Report{
		DryRun:             dryRun,
		ProjectStateBudget: options.Policy.ProjectRoots.MaxBytes,
		StateByteBudget:    options.Policy.StateTotalBytes,
		RepoByteBudget:     options.Policy.RepoRuntimeBytes,
		OwnedTempBudget:    options.Policy.OwnedTempTotalBytes,
	}
}

func pruneProjectRootsInterval(options Options, force bool, report *Report) (ClassReport, bool) {
	class := ClassReport{Name: "project-state-roots"}
	if options.DryRun {
		return pruneProjectRoots(options, report, !force), true
	}
	if err := os.MkdirAll(options.StateRoot, 0o700); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("create project retention dir: %v", err))
		return class, false
	}
	lockPath := filepath.Join(options.StateRoot, ".project-root-retention.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("open project retention lock: %v", err))
		return class, false
	}
	defer lock.Close()
	unlock, err := filelock.Lock(lock)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("lock project retention: %v", err))
		return class, false
	}
	defer func() {
		if err := unlock(); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("unlock project retention: %v", err))
		}
	}()
	marker := filepath.Join(options.StateRoot, ".last-project-root-retention")
	if !force {
		if info, err := os.Stat(marker); err == nil && options.Now.Sub(info.ModTime()) < options.Policy.Interval {
			return class, false
		}
	}
	class = pruneProjectRoots(options, report, !force)
	body := []byte(options.Now.UTC().Format(time.RFC3339Nano) + "\n")
	if _, err := atomicfile.WriteIfChanged(marker, body, 0o600); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("write project retention marker: %v", err))
	}
	return class, true
}

func pruneProjectRoots(options Options, report *Report, preserveRecent bool) ClassReport {
	class := ClassReport{Name: "project-state-roots"}
	projects := filepath.Join(options.StateRoot, "projects")
	entries, err := os.ReadDir(projects)
	if errors.Is(err, os.ErrNotExist) {
		return class
	}
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("read project state roots: %v", err))
		return class
	}
	current := filepath.Clean(ProjectDir(options.StateRoot, options.RepoRoot))
	protected := make([]candidate, 0, len(entries))
	removable := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !isProjectKey(entry.Name()) {
			continue
		}
		path := filepath.Join(projects, entry.Name())
		info, infoErr := entry.Info()
		if infoErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("stat project state root %s: %v", path, infoErr))
			return class
		}
		size, latest, sizeErr := projectTreeSizeAndLatest(path, info.ModTime())
		if sizeErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("inspect project state root %s: %v", path, sizeErr))
			return class
		}
		activeSession, activeErr := liveActiveSession(path, "", options.Now, options.Policy.Locks.MaxAge)
		if activeErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("resolve active session for project state root %s: %v", path, activeErr))
		}
		decisionPresent, decisionErr := policyDecisionPresent(path)
		if decisionErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("inspect policy decision for project state root %s: %v", path, decisionErr))
		}
		live := path == current || activeSession != "" || activeErr != nil || decisionPresent || decisionErr != nil
		recent := preserveRecent && options.Policy.Locks.MaxAge > 0 && options.Now.Sub(latest) <= options.Policy.Locks.MaxAge
		item := candidate{path: path, name: entry.Name(), size: size, mtime: latest, active: live || recent, dir: true}
		class.BytesBefore += size
		if item.active {
			protected = append(protected, item)
		} else {
			removable = append(removable, item)
		}
	}
	class.FilesKept = len(protected)
	for _, item := range protected {
		class.BytesAfter += item.size
	}
	sort.Slice(removable, func(i, j int) bool {
		if removable[i].mtime.Equal(removable[j].mtime) {
			return removable[i].name < removable[j].name
		}
		return removable[i].mtime.After(removable[j].mtime)
	})
	policy := options.Policy.ProjectRoots
	for _, item := range removable {
		expired := policy.MaxAge > 0 && options.Now.Sub(item.mtime) > policy.MaxAge
		exceeds := policy.MaxFiles >= 0 && class.FilesKept >= policy.MaxFiles || policy.MaxBytes >= 0 && class.BytesAfter+item.size > policy.MaxBytes
		if expired || exceeds {
			if removeCandidate(item, options.DryRun, report) {
				class.FilesDeleted++
				class.BytesFreed += item.size
				continue
			}
		}
		class.FilesKept++
		class.BytesAfter += item.size
	}
	return class
}

func policyDecisionPresent(project string) (bool, error) {
	info, err := os.Lstat(filepath.Join(project, "policy-decisions", "latest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("latest policy decision is not a regular file")
	}
	return true, nil
}

func projectTreeSizeAndLatest(root string, initial time.Time) (int64, time.Time, error) {
	var size int64
	latest := initial
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
		case info.Mode().IsRegular():
			size += info.Size()
		default:
			return fmt.Errorf("unsupported non-regular entry %s", entry.Name())
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return size, latest, err
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
	defer func() {
		if err := unlock(); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("unlock global retention: %v", err))
		}
	}()
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
	report := run()
	if err := unlock(); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("unlock retention: %v", err))
	}
	return report
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
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("stat %s entry %s: %v", name, filepath.Join(dir, entry.Name()), err))
			return class
		}
		if !info.Mode().IsRegular() {
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

func liveActiveSession(project, requested string, now time.Time, maxAge time.Duration) (string, error) {
	if requested == "" {
		path := filepath.Join(project, "active-session.txt")
		file, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", nil
			}
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, MaxSessionIDBytes+2))
		closeErr := file.Close()
		if readErr != nil {
			return "", fmt.Errorf("read %s: %w", path, readErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close %s: %w", path, closeErr)
		}
		if len(data) > MaxSessionIDBytes+1 {
			return "", fmt.Errorf("%s exceeds %d bytes", path, MaxSessionIDBytes+1)
		}
		requested = strings.TrimSuffix(string(data), "\n")
	}
	if requested == "" {
		return "", nil
	}
	if err := ValidateSessionID(requested); err != nil {
		return "", err
	}
	info, err := os.Stat(filepath.Join(project, "sessions", SessionFileID(requested)+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stat active session state: %w", err)
	}
	if maxAge > 0 && now.Sub(info.ModTime()) > maxAge {
		return "", nil
	}
	return requested, nil
}

func enforceJSONL(name, path string, maxBytes int64, archives int, dryRun bool, report *Report) ClassReport {
	class := ClassReport{Name: name}
	var err error
	class.BytesBefore, class.FilesKept, err = jsonlRingSize(path, archives+32)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect %s ring: %v", name, err))
		return class
	}
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
	class.BytesAfter, class.FilesKept, err = jsonlRingSize(path, archives)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect enforced %s ring: %v", name, err))
	}
	return class
}

func inspectChainedAudit(name, repoRoot string, maxBytes int64, archives int, report *Report) ClassReport {
	path := filepath.Join(repoRoot, audit.AuditFileRelative)
	class := ClassReport{Name: name}
	var err error
	class.BytesBefore, class.FilesKept, err = jsonlRingSize(path, audit.MaxArchiveFiles+32)
	class.BytesAfter = class.BytesBefore
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect %s ring: %v", name, err))
		return class
	}
	if maxBytes != audit.DefaultMaxSizeBytes || archives != audit.MaxArchiveFiles {
		report.Errors = append(report.Errors, fmt.Sprintf(
			"%s retention is writer-owned and requires %d bytes with %d archives; configured %d bytes with %d archives",
			name, audit.DefaultMaxSizeBytes, audit.MaxArchiveFiles, maxBytes, archives,
		))
		return class
	}
	pendingCleanup, err := jsonl.Inspect(path, jsonl.Policy{
		MaxBytes:    audit.DefaultMaxSizeBytes,
		MaxArchives: audit.MaxArchiveFiles,
	})
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect %s bounds: %v", name, err))
		return class
	}
	if pendingCleanup.BytesFreed != 0 || pendingCleanup.FilesRemoved != 0 {
		report.Errors = append(report.Errors, fmt.Sprintf(
			"%s ring violates its writer-owned bound; refusing generic compaction of chained evidence",
			name,
		))
		return class
	}
	if _, err := audit.EnforceRetention(repoRoot); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("verify %s chain: %v", name, err))
	}
	return class
}

func jsonlRingSize(path string, maxArchives int) (int64, int, error) {
	var bytes int64
	files := 0
	for index := 0; index <= maxArchives; index++ {
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		info, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return bytes, files, err
		}
		if info.Mode().IsRegular() {
			bytes += info.Size()
			files++
		}
	}
	return bytes, files, nil
}

func isGeneratedBinary(entry os.DirEntry) bool {
	name := entry.Name()
	return strings.HasPrefix(name, "workflow-audit-") || name == "generated-reference-audit" || name == "promote-task-done"
}

func generatedBinaryActiveNames(cacheDir string, now time.Time, grace time.Duration) (map[string]bool, error) {
	active := map[string]bool{}
	entries, err := readOwnedDirectory(cacheDir)
	if errors.Is(err, os.ErrNotExist) {
		return active, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !isGeneratedBinary(entry) {
			continue
		}
		buildActive, err := generatedBinaryBuildActive(cacheDir, entry.Name(), now, grace)
		if err != nil {
			return nil, err
		}
		active[entry.Name()] = buildActive
	}
	return active, nil
}

func readOwnedDirectory(path string) ([]os.DirEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
	}
	return os.ReadDir(path)
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
