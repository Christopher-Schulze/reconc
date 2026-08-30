package retention

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/jsonl"
	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/repositorycontrol"
)

type candidate struct {
	path             string
	name             string
	size             int64
	mtime            time.Time
	active           bool
	dir              bool
	probeLock        bool
	probeProjectRoot bool
	info             os.FileInfo
	validate         candidateValidator
}

const (
	maxRetentionDirectoryEntries = 16_384
	maxRetentionWalkEntries      = 100_000
	// ProjectRootRetentionLockName serializes root deletion with creation of
	// durable action and agent-session state in another process.
	ProjectRootRetentionLockName = ".project-root-retention.lock"
)

// Run executes an immediate, cross-process-serialized retention pass.
func Run(options Options) Report {
	return RunContext(context.Background(), options)
}

// RunContext executes an immediate retention pass under the caller lifecycle.
func RunContext(ctx context.Context, options Options) Report {
	options = normalizeOptions(options)
	if ctx == nil {
		return retentionContextError(options, errors.New("retention context is required"))
	}
	if err := ctx.Err(); err != nil {
		return retentionContextError(options, err)
	}
	if options.DryRun {
		return runLockedContext(ctx, options, true)
	}
	return withPruneLockContext(ctx, options, func() Report {
		return runLockedContext(ctx, options, true)
	})
}

// RunIfDue executes at most once per Policy.Interval. A not-due call performs
// one stat and one lock round-trip but writes nothing.
func RunIfDue(options Options) Report {
	return RunIfDueContext(context.Background(), options)
}

// RunIfDueContext executes a due retention pass under the caller lifecycle.
func RunIfDueContext(ctx context.Context, options Options) Report {
	options = normalizeOptions(options)
	if ctx == nil {
		return retentionContextError(options, errors.New("retention context is required"))
	}
	if err := ctx.Err(); err != nil {
		return retentionContextError(options, err)
	}
	if options.DryRun {
		marker := filepath.Join(ProjectDir(options.StateRoot, options.RepoRoot), ".last-retention")
		if info, err := os.Stat(marker); err == nil && options.Now.Sub(info.ModTime()) < options.Policy.Interval {
			return emptyReport(options, true)
		}
		return runLockedContext(ctx, options, false)
	}
	return withPruneLockContext(ctx, options, func() Report {
		marker := filepath.Join(ProjectDir(options.StateRoot, options.RepoRoot), ".last-retention")
		if info, err := os.Stat(marker); err == nil && options.Now.Sub(info.ModTime()) < options.Policy.Interval {
			return emptyReport(options, options.DryRun)
		}
		report := runLockedContext(ctx, options, false)
		if !options.DryRun {
			body := []byte(options.Now.UTC().Format(time.RFC3339Nano) + "\n")
			if _, err := privatefs.WritePrivateIfChanged(marker, body, 0o600); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("write retention marker: %v", err))
			}
		}
		return report
	})
}

func runLocked(options Options, forceOwnedTemp bool) Report {
	return runLockedContext(context.Background(), options, forceOwnedTemp)
}

func runLockedContext(ctx context.Context, options Options, forceOwnedTemp bool) Report {
	policy := options.Policy
	report := Report{
		Ran:                true,
		DryRun:             options.DryRun,
		ProjectStateBudget: policy.ProjectRoots.MaxBytes,
		StateByteBudget:    policy.StateTotalBytes,
		RepoByteBudget:     policy.RepoRuntimeBytes,
		OwnedTempBudget:    policy.OwnedTempTotalBytes,
	}
	if err := ctx.Err(); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("retention canceled: %v", err))
		return report
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
		stateOptions.Policy.PreDecisions = noDelete
		stateOptions.Policy.TaintResolutions = noDelete
		stateOptions.Policy.StateTotalBytes = int64(^uint64(0) >> 1)
	}
	activeID := SessionFileID(active)
	taintProtection, taintProtectionErr := resolveTaintResolutionProtection(project, options.RepoRoot)
	if taintProtectionErr != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("resolve taint-resolution protection: %v", taintProtectionErr))
	}
	if active != "" || activeErr != nil {
		taintProtection.all = true
	}
	if taintProtection.all {
		stateOptions.Policy.TaintResolutions = ClassPolicy{MaxFiles: -1, MaxBytes: -1}
	}

	sessions := filepath.Join(project, "sessions")
	reports := filepath.Join(project, "reports")
	locks := filepath.Join(project, "locks")
	commandProofs := filepath.Join(project, "command-proofs")
	policyDecisions := filepath.Join(project, "policy-decisions")
	preDecisions := filepath.Join(project, "pre-decisions")
	taintResolutions := filepath.Join(project, "evidence-taint-resolutions")
	report.Classes = append(report.Classes,
		pruneClass("sessions", sessions, stateOptions.Policy.Sessions, options.Now, options.DryRun, map[string]bool{activeID + ".json": active != ""}, nil, &report),
		pruneClass("reports", reports, stateOptions.Policy.Reports, options.Now, options.DryRun, map[string]bool{activeID + ".json": active != ""}, nil, &report),
		pruneClass("locks", locks, stateOptions.Policy.Locks, options.Now, options.DryRun, map[string]bool{
			activeID + ".lock":             active != "",
			activeID + ".stop-policy.lock": active != "",
		}, nil, &report),
		pruneClass("command-proofs", commandProofs, stateOptions.Policy.CommandProofs, options.Now, options.DryRun, nil, nil, &report),
		pruneClass("policy-decisions", policyDecisions, stateOptions.Policy.PolicyDecisions, options.Now, options.DryRun, map[string]bool{"latest.json": true}, nil, &report),
		pruneClassInspected("pre-decisions", preDecisions, stateOptions.Policy.PreDecisions, options.Now, options.DryRun, map[string]bool{activeID + ".json": active != ""}, nil, inspectPreDecisionArtifact, &report),
		pruneClassInspected("evidence-taint-resolutions", taintResolutions, stateOptions.Policy.TaintResolutions, options.Now, options.DryRun, taintProtection.names, nil, inspectTaintResolutionArtifact(options.RepoRoot), &report),
	)
	projectedStateBefore, projectedStateAfter := classTotals(report.Classes, "sessions", "reports", "locks", "command-proofs", "policy-decisions", "pre-decisions", "evidence-taint-resolutions")
	stateTotal := enforceStateTotal(stateOptions, project, activeID, active != "", taintProtection, &report)
	if options.DryRun {
		stateTotal.BytesBefore = projectedStateBefore
		stateTotal.BytesAfter = minInt64(stateTotal.BytesAfter, projectedStateAfter)
		stateTotal.BytesFreed = stateTotal.BytesBefore - stateTotal.BytesAfter
	}
	report.Classes = append(report.Classes, stateTotal)
	report.StateBytesAfter = stateTotal.BytesAfter

	runDecisionPath := filepath.Join(options.RepoRoot, ".reconc", "run", "decisions.jsonl")
	report.Classes = append(report.Classes,
		inspectChainedAuditContext(ctx, "audit", options.RepoRoot, policy.AuditFileBytes, policy.AuditArchives, &report),
		enforceRunDecisionJSONLContext(ctx, "run-decisions", runDecisionPath, policy.RunDecisionFileBytes, policy.RunDecisionArchives, options.DryRun, &report),
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
	ownedTempClass, ownedTempScanned := pruneOwnedTempRootsIntervalContext(ctx, options, forceOwnedTemp, &report)
	projectRootsClass, projectRootsScanned := pruneProjectRootsIntervalContext(ctx, options, forceOwnedTemp, &report)
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

func retentionContextError(options Options, err error) Report {
	report := emptyReport(options, options.DryRun)
	report.Errors = []string{fmt.Sprintf("retention context: %v", err)}
	return report
}

func pruneProjectRootsInterval(options Options, force bool, report *Report) (ClassReport, bool) {
	return pruneProjectRootsIntervalContext(context.Background(), options, force, report)
}

func pruneProjectRootsIntervalContext(ctx context.Context, options Options, force bool, report *Report) (ClassReport, bool) {
	class := ClassReport{Name: "project-state-roots"}
	if err := ctx.Err(); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("project retention canceled: %v", err))
		return class, false
	}
	if options.DryRun {
		return pruneProjectRoots(options, report, !force), true
	}
	if err := privatefs.RepairDirectory(options.StateRoot); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("create project retention dir: %v", err))
		return class, false
	}
	lockPath := filepath.Join(options.StateRoot, ProjectRootRetentionLockName)
	lock, err := privatefs.OpenLock(lockPath)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("open project retention lock: %v", err))
		return class, false
	}
	defer lock.Close()
	unlock, err := filelock.LockContext(ctx, lock, filelock.DefaultTimeout)
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
	if _, err := privatefs.WritePrivateIfChanged(marker, body, 0o600); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("write project retention marker: %v", err))
	}
	return class, true
}

func pruneProjectRoots(options Options, report *Report, preserveRecent bool) ClassReport {
	class := ClassReport{Name: "project-state-roots"}
	projects := filepath.Join(options.StateRoot, "projects")
	entries, err := boundedio.ReadDirNoSymlink(projects, maxRetentionDirectoryEntries)
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
		actionStatePresent, actionStateErr := durableActionStateBoundaryPresent(path)
		if actionStateErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("inspect action state for project state root %s: %v", path, actionStateErr))
		}
		live := path == current || activeSession != "" || activeErr != nil || decisionPresent || decisionErr != nil ||
			actionStatePresent || actionStateErr != nil
		recent := preserveRecent && options.Policy.Locks.MaxAge > 0 && options.Now.Sub(latest) <= options.Policy.Locks.MaxAge
		item := candidate{
			path: path, name: entry.Name(), size: size, mtime: latest,
			active: live || recent, dir: true, probeProjectRoot: true, info: info,
		}
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

func durableActionStateBoundaryPresent(project string) (bool, error) {
	path := filepath.Join(project, "action")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("action state boundary must be a non-symlink directory")
	}
	return true, nil
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
	visited := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxRetentionWalkEntries {
			return fmt.Errorf("project state tree exceeds %d entries", maxRetentionWalkEntries)
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
	return pruneOwnedTempRootsIntervalContext(context.Background(), options, force, report)
}

func pruneOwnedTempRootsIntervalContext(ctx context.Context, options Options, force bool, report *Report) (ClassReport, bool) {
	class := ClassReport{Name: "abandoned-owned-temp"}
	if err := ctx.Err(); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("owned-temp retention canceled: %v", err))
		return class, false
	}
	if options.DryRun {
		return pruneOwnedTempRoots(options, report), true
	}
	if err := privatefs.RepairDirectory(options.StateRoot); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("create global retention dir: %v", err))
		return class, false
	}
	lockPath := filepath.Join(options.StateRoot, ".owned-temp-retention.lock")
	lock, err := privatefs.OpenLock(lockPath)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("open global retention lock: %v", err))
		return class, false
	}
	defer lock.Close()
	unlock, err := filelock.LockContext(ctx, lock, filelock.DefaultTimeout)
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
		if _, err := privatefs.WritePrivateIfChanged(marker, body, 0o600); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("write global retention marker: %v", err))
		}
	}
	return class, true
}

func normalizeOptions(options Options) Options {
	defaults := DefaultPolicy()
	if options.Policy.Interval <= 0 {
		// Callers that leave the policy unconfigured (Interval zero) get the
		// full default policy. Several production entry points rely on this:
		// they pass Options without a Policy and expect sane class limits,
		// not the zero ClassPolicy that would prune everything.
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
	return withPruneLockContext(context.Background(), options, run)
}

func withPruneLockContext(ctx context.Context, options Options, run func() Report) Report {
	if ctx == nil {
		return retentionContextError(options, errors.New("retention context is required"))
	}
	project := ProjectDir(options.StateRoot, options.RepoRoot)
	if err := privatefs.RepairDirectory(project); err != nil {
		return Report{Errors: []string{fmt.Sprintf("create retention project dir: %v", err)}}
	}
	path := filepath.Join(project, ".retention.lock")
	lock, err := privatefs.OpenLock(path)
	if err != nil {
		return Report{Errors: []string{fmt.Sprintf("open retention lock: %v", err)}}
	}
	defer lock.Close()
	unlock, err := filelock.LockContext(ctx, lock, filelock.DefaultTimeout)
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
	return pruneClassInspected(name, dir, policy, now, dryRun, activeNames, include, nil, report)
}

type candidateValidator func(string, string, os.FileInfo) error

type stateArtifactInspector = candidateValidator

func pruneClassInspected(
	name, dir string,
	policy ClassPolicy,
	now time.Time,
	dryRun bool,
	activeNames map[string]bool,
	include func(os.DirEntry) bool,
	inspect stateArtifactInspector,
	report *Report,
) ClassReport {
	class := ClassReport{Name: name}
	entries, err := boundedio.ReadDirNoSymlink(dir, maxRetentionDirectoryEntries)
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
			if inspect != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("inspect %s entry %s: not a regular file", name, filepath.Join(dir, entry.Name())))
			}
			continue
		}
		item := candidate{
			path: filepath.Join(dir, entry.Name()), name: entry.Name(), size: info.Size(), mtime: info.ModTime(),
			active: activeNames != nil && activeNames[entry.Name()], probeLock: name == "locks", info: info,
		}
		if inspect != nil {
			if err := inspect(item.path, entry.Name(), info); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("inspect %s entry %s: %v", name, item.path, err))
				item.active = true
			} else {
				item.validate = inspect
			}
		}
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
	return removeCandidateWithHooks(item, dryRun, report, candidateRemovalHooks{})
}

type candidateRemovalHooks struct {
	afterValidation func(candidate) error
}

func removeCandidateWithHooks(item candidate, dryRun bool, report *Report, hooks candidateRemovalHooks) bool {
	if item.info == nil {
		report.Errors = append(report.Errors, fmt.Sprintf("remove %s: missing discovered identity", item.path))
		return false
	}
	parentPath := filepath.Dir(item.path)
	parentInfo, err := os.Lstat(parentPath)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("remove %s: inspect parent: %v", item.path, err))
		return false
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		report.Errors = append(report.Errors, fmt.Sprintf("remove %s: parent is not a non-symlink directory", item.path))
		return false
	}
	root, err := os.OpenRoot(parentPath)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("remove %s: open parent: %v", item.path, err))
		return false
	}
	openedParent, statErr := root.Stat(".")
	if statErr != nil || !openedParent.IsDir() || !os.SameFile(parentInfo, openedParent) {
		closeErr := root.Close()
		report.Errors = append(report.Errors, fmt.Sprintf("remove %s: parent changed identity: %v", item.path, errors.Join(statErr, closeErr)))
		return false
	}
	name := filepath.Base(item.path)
	if err := validateCandidateAt(root, name, item); err != nil {
		closeErr := root.Close()
		report.Errors = append(report.Errors, fmt.Sprintf("remove %s: %v", item.path, errors.Join(err, closeErr)))
		return false
	}
	if item.validate != nil {
		if err := item.validate(item.path, filepath.Base(item.path), item.info); err != nil {
			closeErr := root.Close()
			report.Errors = append(report.Errors, fmt.Sprintf("remove %s: revalidate content: %v", item.path, errors.Join(err, closeErr)))
			return false
		}
	}
	if hooks.afterValidation != nil {
		if err := hooks.afterValidation(item); err != nil {
			closeErr := root.Close()
			report.Errors = append(report.Errors, fmt.Sprintf("remove %s: before-delete hook: %v", item.path, errors.Join(err, closeErr)))
			return false
		}
		if err := validateCandidateAt(root, name, item); err != nil {
			closeErr := root.Close()
			report.Errors = append(report.Errors, fmt.Sprintf("remove %s: %v", item.path, errors.Join(err, closeErr)))
			return false
		}
		if item.validate != nil {
			if err := item.validate(item.path, filepath.Base(item.path), item.info); err != nil {
				closeErr := root.Close()
				report.Errors = append(report.Errors, fmt.Sprintf("remove %s: revalidate content: %v", item.path, errors.Join(err, closeErr)))
				return false
			}
		}
	}
	lease, live, leaseErr := acquireCandidateLease(root, name, item)
	if leaseErr != nil {
		closeErr := root.Close()
		report.Errors = append(report.Errors, fmt.Sprintf("remove %s: validate liveness: %v", item.path, errors.Join(leaseErr, closeErr)))
		return false
	}
	if live {
		if closeErr := root.Close(); closeErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("remove %s: close parent after live lock probe: %v", item.path, closeErr))
		}
		return false
	}
	if dryRun {
		if closeErr := errors.Join(lease.close(), root.Close()); closeErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("inspect removal of %s: %v", item.path, closeErr))
			return false
		}
		return true
	}
	if item.dir {
		target, openErr := root.OpenRoot(name)
		if openErr == nil {
			targetInfo, targetStatErr := target.Stat(".")
			closeTargetErr := target.Close()
			if targetStatErr != nil || !sameCandidateType(item, targetInfo) || !os.SameFile(item.info, targetInfo) {
				openErr = errors.Join(targetStatErr, closeTargetErr, errors.New("directory identity changed before recursive removal"))
			} else {
				openErr = closeTargetErr
			}
		}
		if openErr == nil {
			openErr = root.RemoveAll(name)
		}
		err = openErr
	} else {
		err = root.Remove(name)
	}
	leaseErr = lease.close()
	closeErr := root.Close()
	if err := errors.Join(err, leaseErr, closeErr); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("remove %s: %v", item.path, err))
		return false
	}
	return true
}

type candidateLease struct {
	locks []heldCandidateLock
}

type heldCandidateLock struct {
	file   *os.File
	unlock func() error
}

func acquireCandidateLease(parent *os.Root, name string, item candidate) (*candidateLease, bool, error) {
	lease := &candidateLease{}
	if item.probeLock {
		live, err := lease.tryLockCandidate(parent, name, item.info)
		return lease, live, err
	}
	if !item.probeProjectRoot {
		return lease, false, nil
	}
	project, err := parent.OpenRoot(name)
	if err != nil {
		return lease, false, err
	}
	live, probeErr := lease.tryLockOptionalCandidate(project, ".retention.lock")
	if probeErr != nil || live {
		closeErr := project.Close()
		return lease, live, errors.Join(probeErr, closeErr, lease.close())
	}
	live, probeErr = lease.tryLockDirectory(project, "locks")
	closeErr := project.Close()
	if probeErr != nil || closeErr != nil {
		return lease, false, errors.Join(probeErr, closeErr, lease.close())
	}
	if live {
		return lease, true, lease.close()
	}
	return lease, false, nil
}

func (lease *candidateLease) tryLockOptionalCandidate(root *os.Root, name string) (bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular lock file", name)
	}
	return lease.tryLockCandidate(root, name, info)
}

func (lease *candidateLease) tryLockDirectory(project *os.Root, name string) (bool, error) {
	directory, err := project.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	directoryInfo, statErr := directory.Stat()
	if statErr != nil || !directoryInfo.IsDir() {
		return false, errors.Join(statErr, directory.Close(), fmt.Errorf("%s is not a directory", name))
	}
	entries, readErr := directory.ReadDir(maxRetentionDirectoryEntries + 1)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, errors.Join(readErr, directory.Close())
	}
	if len(entries) > maxRetentionDirectoryEntries {
		return false, errors.Join(directory.Close(), fmt.Errorf("%s exceeds %d entries", name, maxRetentionDirectoryEntries))
	}
	lockRoot, openErr := project.OpenRoot(name)
	if openErr != nil {
		return false, errors.Join(openErr, directory.Close())
	}
	openedInfo, openedStatErr := lockRoot.Stat(".")
	if openedStatErr != nil || !openedInfo.IsDir() || !os.SameFile(directoryInfo, openedInfo) {
		return false, errors.Join(openedStatErr, lockRoot.Close(), directory.Close(), fmt.Errorf("%s changed identity", name))
	}
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return false, errors.Join(infoErr, lockRoot.Close(), directory.Close(), lease.close(), fmt.Errorf("%s/%s is not a regular lock file", name, entry.Name()))
		}
		live, lockErr := lease.tryLockCandidate(lockRoot, entry.Name(), info)
		if lockErr != nil || live {
			return live, errors.Join(lockErr, lockRoot.Close(), directory.Close())
		}
	}
	currentInfo, currentErr := project.Lstat(name)
	closeErr := errors.Join(lockRoot.Close(), directory.Close())
	if currentErr != nil || !currentInfo.IsDir() || !os.SameFile(directoryInfo, currentInfo) {
		return false, errors.Join(currentErr, closeErr, fmt.Errorf("%s changed identity during lock probe", name))
	}
	return false, closeErr
}

func (lease *candidateLease) tryLockCandidate(root *os.Root, name string, expected os.FileInfo) (bool, error) {
	file, err := root.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return false, errors.Join(err, file.Close(), errors.New("lock identity changed before ownership probe"))
	}
	unlock, err := filelock.TryLock(file)
	if err != nil {
		closeErr := file.Close()
		if filelock.IsContended(err) {
			return true, closeErr
		}
		return false, errors.Join(err, closeErr)
	}
	current, statErr := root.Lstat(name)
	if statErr != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return false, errors.Join(statErr, unlock(), file.Close(), errors.New("lock identity changed during ownership probe"))
	}
	lease.locks = append(lease.locks, heldCandidateLock{file: file, unlock: unlock})
	return false, nil
}

func (lease *candidateLease) close() error {
	if lease == nil {
		return nil
	}
	var result error
	for index := len(lease.locks) - 1; index >= 0; index-- {
		lock := lease.locks[index]
		result = errors.Join(result, lock.unlock(), lock.file.Close())
	}
	lease.locks = nil
	return result
}

func validateCandidateAt(root *os.Root, name string, item candidate) error {
	current, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !sameCandidateType(item, current) || !os.SameFile(item.info, current) {
		return errors.New("target identity or type changed before deletion")
	}
	return nil
}

func sameCandidateType(item candidate, info os.FileInfo) bool {
	if info == nil {
		return false
	}
	if item.dir {
		return info.Mode()&os.ModeSymlink == 0 && info.IsDir()
	}
	return info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular()
}

func liveActiveSession(project, requested string, now time.Time, maxAge time.Duration) (string, error) {
	if requested != "" {
		if err := ValidateSessionID(requested); err != nil {
			return "", err
		}
		return requested, nil
	}
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
	return enforceJSONLContext(context.Background(), name, path, maxBytes, archives, dryRun, report)
}

func enforceJSONLContext(ctx context.Context, name, path string, maxBytes int64, archives int, dryRun bool, report *Report) ClassReport {
	class := ClassReport{Name: name, InspectionStatus: InspectionUnknown}
	var err error
	class.BytesBefore, class.FilesKept, err = jsonl.RingSizeContext(ctx, path, jsonl.MaxArchiveFiles)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect %s ring: %v", name, err))
		return class
	}
	if dryRun {
		result, err := jsonl.InspectContext(ctx, path, jsonl.Policy{MaxBytes: maxBytes, MaxArchives: archives})
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("inspect %s: %v", name, err))
			return class
		}
		class.InspectionStatus = InspectionComplete
		class.BytesFreed = result.BytesFreed
		class.FilesDeleted = result.FilesRemoved
		class.BytesAfter = class.BytesBefore - result.BytesFreed
		class.FilesKept -= result.FilesRemoved
		return class
	}
	result, enforceErr := jsonl.EnforceContext(ctx, path, jsonl.Policy{MaxBytes: maxBytes, MaxArchives: archives})
	if enforceErr != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("enforce %s: %v", name, enforceErr))
	}
	class.BytesFreed = result.BytesFreed
	class.FilesDeleted = result.FilesRemoved
	class.BytesAfter, class.FilesKept, err = jsonl.RingSizeContext(ctx, path, archives)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect enforced %s ring: %v", name, err))
	} else if enforceErr == nil {
		class.InspectionStatus = InspectionComplete
	}
	return class
}

func enforceRunDecisionJSONL(name, path string, maxBytes int64, archives int, dryRun bool, report *Report) ClassReport {
	return enforceRunDecisionJSONLContext(context.Background(), name, path, maxBytes, archives, dryRun, report)
}

func enforceRunDecisionJSONLContext(ctx context.Context, name, path string, maxBytes int64, archives int, dryRun bool, report *Report) ClassReport {
	if err := ctx.Err(); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("enforce %s: %v", name, err))
		return ClassReport{Name: name, InspectionStatus: InspectionUnknown}
	}
	if _, err := os.Lstat(filepath.Dir(path)); errors.Is(err, os.ErrNotExist) {
		return ClassReport{Name: name, InspectionStatus: InspectionComplete}
	} else if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect %s directory: %v", name, err))
		return ClassReport{Name: name, InspectionStatus: InspectionUnknown}
	}
	if err := repositorycontrol.ValidateRunDirectory(filepath.Dir(path)); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("validate %s directory: %v", name, err))
		return ClassReport{Name: name, InspectionStatus: InspectionUnknown}
	}
	if err := validateRunDecisionSecurityContext(ctx, path); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("validate %s access: %v", name, err))
		return ClassReport{Name: name, InspectionStatus: InspectionUnknown}
	}
	if dryRun {
		return enforceJSONLContext(ctx, name, path, maxBytes, archives, true, report)
	}
	class := ClassReport{Name: name, InspectionStatus: InspectionUnknown}
	var err error
	class.BytesBefore, class.FilesKept, err = jsonl.RingSizeContext(ctx, path, jsonl.MaxArchiveFiles)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect %s ring: %v", name, err))
		return class
	}
	result, enforceErr := jsonl.EnforceContextWithLayout(
		ctx,
		path,
		jsonl.Policy{MaxBytes: maxBytes, MaxArchives: archives},
		repositorycontrol.RunDecisionLayout(path),
	)
	if enforceErr != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("enforce %s: %v", name, enforceErr))
	}
	class.BytesFreed = result.BytesFreed
	class.FilesDeleted = result.FilesRemoved
	class.BytesAfter, class.FilesKept, err = jsonl.RingSizeContext(ctx, path, archives)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("inspect enforced %s ring: %v", name, err))
	} else if enforceErr == nil {
		class.InspectionStatus = InspectionComplete
	}
	return class
}

func validateRunDecisionSecurity(path string) error {
	return validateRunDecisionSecurityContext(context.Background(), path)
}

func validateRunDecisionSecurityContext(ctx context.Context, path string) error {
	sources, err := jsonl.PathsOldestFirstContext(ctx, path, jsonl.MaxArchiveFiles)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if err := boundedio.WithRegularFileSnapshot(source, max(info.Size(), 1), func(file *os.File, info os.FileInfo) error {
			return privatefs.ValidateFileAllowLinks(file, info)
		}); err != nil {
			return err
		}
	}
	lockPath := path + ".lock"
	if _, err := os.Lstat(lockPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return boundedio.WithRegularFileSnapshot(lockPath, 4<<10, func(file *os.File, info os.FileInfo) error {
		return privatefs.ValidateFile(file, info)
	})
}

func inspectChainedAudit(name, repoRoot string, maxBytes int64, archives int, report *Report) ClassReport {
	return inspectChainedAuditContext(context.Background(), name, repoRoot, maxBytes, archives, report)
}

func inspectChainedAuditContext(ctx context.Context, name, repoRoot string, maxBytes int64, archives int, report *Report) ClassReport {
	path := filepath.Join(repoRoot, audit.AuditFileRelative)
	class := ClassReport{Name: name}
	var err error
	class.BytesBefore, class.FilesKept, err = jsonl.RingSizeContext(ctx, path, audit.MaxArchiveFiles)
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
	pendingCleanup, err := audit.InspectRetentionContext(ctx, repoRoot)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("verify %s chain: %v", name, err))
		return class
	}
	if pendingCleanup.BytesFreed != 0 || pendingCleanup.FilesRemoved != 0 {
		report.Errors = append(report.Errors, fmt.Sprintf(
			"%s ring violates its writer-owned bound; refusing generic compaction of chained evidence",
			name,
		))
		return class
	}
	return class
}

func isGeneratedBinary(entry os.DirEntry) bool {
	name := entry.Name()
	if strings.HasSuffix(name, ".build.lock") {
		return false
	}
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
	return boundedio.ReadDirNoSymlink(path, maxRetentionDirectoryEntries)
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
