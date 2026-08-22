package bootstrap

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

type applyOptions struct {
	failAfter int
}

type createdRecord struct {
	path       string
	sha256     string
	file       *os.File
	info       os.FileInfo
	parent     *os.Root
	parentInfo os.FileInfo
	name       string
}

type createdDirectory struct {
	path string
	// identity is a pointer so closeDirectoryIdentity can nil the handle
	// after the first close, making repeated closes (the rollback path and
	// the caller defers share the same slice backing array) a safe no-op.
	identity *directoryIdentity
}

func Apply(plan *Plan, productVersion string) (*Report, error) {
	if err := ValidatePlan(plan); err != nil {
		return apply(plan, productVersion, applyOptions{})
	}
	var report *Report
	err := withRepositoryTransactionLock(plan.RepoRoot, func() error {
		var applyErr error
		report, applyErr = apply(plan, productVersion, applyOptions{})
		return applyErr
	})
	if report == nil {
		report = &Report{
			FormatVersion: ReportFormatVersion, Status: ApplyRolledBack,
			Created: []string{}, Unchanged: []string{}, Candidates: []string{}, RolledBack: []string{},
			RepoRoot: plan.RepoRoot, PlanDigest: plan.PlanDigest,
		}
	}
	return report, err
}

func apply(plan *Plan, productVersion string, options applyOptions) (*Report, error) {
	report := &Report{
		FormatVersion: ReportFormatVersion, Status: ApplyRolledBack,
		Created: []string{}, Unchanged: []string{}, Candidates: []string{}, RolledBack: []string{},
	}
	if err := ValidatePlan(plan); err != nil {
		return report, err
	}
	if err := ensureNoPendingRepositorySync(plan.RepoRoot); err != nil {
		return report, err
	}
	report.RepoRoot = plan.RepoRoot
	report.PlanDigest = plan.PlanDigest
	if plan.ProductVersion != productVersion {
		return report, fmt.Errorf("bootstrap plan was built by reconc %s, not the running %s; rebuild the plan", plan.ProductVersion, productVersion)
	}
	if len(plan.BlockingIssues) > 0 {
		return report, fmt.Errorf("bootstrap plan has blocking issue: %s", plan.BlockingIssues[0])
	}
	if err := preflightPlanState(plan); err != nil {
		return report, err
	}
	artifacts, err := buildDesiredArtifacts(plan.RepoRoot, plan.Selection, productVersion)
	if err != nil {
		return report, err
	}
	artifactByPath, err := validateArtifactsMatchPlan(artifacts, plan.Actions)
	if err != nil {
		return report, err
	}
	conflicts := []Action{}
	for _, action := range plan.Actions {
		switch action.State {
		case ActionConflict:
			conflicts = append(conflicts, action)
		case ActionUnchanged:
			report.Unchanged = append(report.Unchanged, action.Path)
		}
	}
	if len(conflicts) > 0 {
		created, dirs, err := materializeCandidates(plan, conflicts, artifactByPath, options)
		defer func() { closeCreatedRecords(created) }()
		defer closeCreatedDirectoryIdentities(dirs)
		if err != nil {
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, dirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(err, rollbackErr)
		}
		for _, action := range conflicts {
			report.Candidates = append(report.Candidates, action.CandidatePath)
		}
		report.Status = ApplyDrift
		report.NextAction = "Review each candidate against its existing target, integrate approved changes surgically, then rebuild the bootstrap plan."
		return report, nil
	}

	created := []createdRecord{}
	createdDirs := []createdDirectory{}
	compiledLockSHA := ""
	defer func() {
		closeCreatedRecords(created)
		closeCreatedDirectoryIdentities(createdDirs)
	}()
	for _, action := range plan.Actions {
		if action.State != ActionCreate {
			continue
		}
		artifact := artifactByPath[action.Path]
		record, dirs, err := publishArtifact(plan.RepoRoot, artifact, action.Path, action.DesiredSHA256, plan.PlanDigest)
		createdDirs = appendUniqueDirectories(createdDirs, dirs...)
		if record.path != "" {
			created = append(created, record)
		}
		if err != nil {
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(err, rollbackErr)
		}
		report.Created = append(report.Created, action.Path)
		if options.failAfter > 0 && len(created) >= options.failAfter {
			failure := fmt.Errorf("injected bootstrap apply failure after %d published artifacts", len(created))
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(failure, rollbackErr)
		}
	}
	if plan.CompileRequired {
		lockPath := filepath.Join(plan.RepoRoot, policyLockfilePath)
		if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
			if err == nil {
				err = fmt.Errorf("policy lockfile appeared after planning")
			}
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(err, rollbackErr)
		}
		reconcDir := filepath.Join(plan.RepoRoot, ".reconc")
		compileDirs, err := createSafeParents(plan.RepoRoot, reconcDir)
		createdDirs = appendUniqueDirectories(createdDirs, compileDirs...)
		if err != nil {
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(err, rollbackErr)
		}
		if _, err := compiler.CompileRepoPolicy(plan.RepoRoot, productVersion); err != nil {
			if record, captureErr := captureCreatedRecord(lockPath); captureErr == nil {
				created = append(created, record)
			} else if !os.IsNotExist(captureErr) {
				err = fmt.Errorf("%v; inspect failed compile output: %w", err, captureErr)
			}
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(fmt.Errorf("compile bootstrap policy: %w", err), rollbackErr)
		}
		lockRecord, err := captureCreatedRecord(lockPath)
		if err != nil {
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(fmt.Errorf("inspect compiled bootstrap lockfile: %w", err), rollbackErr)
		}
		created = append(created, lockRecord)
		compiledLockSHA = lockRecord.sha256
		report.Created = append(report.Created, policyLockfilePath)
	}
	verification, err := Verify(plan)
	if err != nil || !verification.Valid {
		if err == nil {
			err = fmt.Errorf("bootstrap verification failed: %s", verification.NextAction)
		}
		rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
		report.RolledBack = rolledBack
		return report, joinApplyRollbackError(err, rollbackErr)
	}
	recordedPath := ""
	recordedSHA := ""
	if len(created) > 0 {
		recordedPath = recordedPlanPath(plan)
		planBody, encodeErr := encodePlan(plan)
		if encodeErr != nil {
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(encodeErr, rollbackErr)
		}
		recordedSHA = bytesSHA256(planBody)
		artifact := desiredArtifact{component: "bootstrap-plan", path: recordedPath, mode: 0o600, content: planBody}
		record, dirs, publishErr := publishArtifact(plan.RepoRoot, artifact, recordedPath, recordedSHA, plan.PlanDigest)
		createdDirs = appendUniqueDirectories(createdDirs, dirs...)
		if record.path != "" {
			created = append(created, record)
		}
		if publishErr != nil {
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(publishErr, rollbackErr)
		}
		report.Created = append(report.Created, recordedPath)
	}
	receipt, err := buildInstallReceipt(plan, productVersion, compiledLockSHA, recordedPath, recordedSHA)
	if err != nil {
		rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
		report.RolledBack = rolledBack
		return report, joinApplyRollbackError(err, rollbackErr)
	}
	receiptRecord, receiptDirs, receiptPath, err := writeInstallReceipt(plan, receipt)
	createdDirs = appendUniqueDirectories(createdDirs, receiptDirs...)
	if receiptRecord.path != "" {
		created = append(created, receiptRecord)
	}
	if err != nil {
		rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
		report.RolledBack = rolledBack
		return report, joinApplyRollbackError(err, rollbackErr)
	}
	if receipt != nil {
		repositoryReceipt, receiptErr := BuildRepositoryReceipt(plan, receipt, 1, plan.PlanDigest)
		if receiptErr != nil {
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(receiptErr, rollbackErr)
		}
		repositoryRecord, repositoryDirs, publishErr := writeRepositoryReceiptCreate(plan, repositoryReceipt)
		createdDirs = appendUniqueDirectories(createdDirs, repositoryDirs...)
		if repositoryRecord.path != "" {
			created = append(created, repositoryRecord)
		}
		if publishErr != nil {
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, createdDirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(publishErr, rollbackErr)
		}
		report.Created = append(report.Created, RepositoryReceiptRelativePath)
	}
	report.ReceiptPath = receiptPath
	pruneObsoleteBootstrapReceipts(plan.RepoRoot, plan.PlanDigest)
	report.Status = ApplyComplete
	sort.Strings(report.Created)
	sort.Strings(report.Unchanged)
	report.Summary = summarizeApply(plan, report)
	report.NextAction = "reconc check " + quoteBootstrapArgument(plan.RepoRoot)
	return report, nil
}

func recordedPlanPath(plan *Plan) string {
	return filepath.ToSlash(filepath.Join(".reconc", "bootstrap-plan-"+plan.PlanDigest+".json"))
}

func summarizeApply(plan *Plan, report *Report) ApplySummary {
	summary := ApplySummary{
		Created: len(report.Created), Preserved: len(report.Unchanged),
		Drifted: len(report.Candidates), Skipped: len(plan.BlockingIssues),
	}
	statuses, statusErr := hooks.InspectPlatforms(plan.RepoRoot)
	byKind := map[string]hooks.PlatformStatus{}
	if statusErr != nil {
		summary.InspectionErrors = append(summary.InspectionErrors, "inspect hook platforms: "+statusErr.Error())
	} else {
		for _, status := range statuses {
			byKind[status.Kind] = status
		}
		for _, kind := range plan.Selection.Hooks {
			status := byKind[kind]
			if status.Installed {
				summary.Installed++
			}
			if status.Configured {
				summary.Configured++
			}
		}
	}
	liveness, livenessErr := agentsession.ReadHookLiveness(plan.RepoRoot)
	if livenessErr != nil {
		summary.InspectionErrors = append(summary.InspectionErrors, "read hook liveness: "+livenessErr.Error())
	} else {
		summary.LivenessKnown = true
		for _, kind := range plan.Selection.Hooks {
			if record, ok := liveness[bootstrapHookRuntimeName(kind)]; ok && record.LastSeen != "" {
				summary.Live++
			}
		}
	}
	return summary
}

func bootstrapHookRuntimeName(kind string) string {
	switch kind {
	case hooks.KindClaudeCode:
		return "claude"
	case hooks.KindDevinCLI:
		return "devin"
	default:
		return kind
	}
}

func quoteBootstrapArgument(value string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func renderBootstrapCommand(program string, args ...string) string {
	parts := make([]string, 1, len(args)+1)
	parts[0] = program
	for _, argument := range args {
		parts = append(parts, quoteBootstrapArgument(argument))
	}
	return strings.Join(parts, " ")
}

func materializeCandidates(plan *Plan, conflicts []Action, artifacts map[string]desiredArtifact, options applyOptions) ([]createdRecord, []createdDirectory, error) {
	created := []createdRecord{}
	dirs := []createdDirectory{}
	for _, action := range conflicts {
		artifact := artifacts[action.Path]
		candidate := artifact
		candidate.path = action.CandidatePath
		target := filepath.Join(plan.RepoRoot, filepath.FromSlash(action.CandidatePath))
		if info, err := os.Lstat(target); err == nil {
			if !info.Mode().IsRegular() || !modeSatisfies(info.Mode(), action.Mode) {
				return created, dirs, fmt.Errorf("candidate path already exists with incompatible type or mode: %s", action.CandidatePath)
			}
			digest, err := fileSHA256(target)
			if err != nil {
				return created, dirs, err
			}
			if digest != action.DesiredSHA256 {
				return created, dirs, fmt.Errorf("candidate path already exists with different content: %s", action.CandidatePath)
			}
			continue
		} else if !os.IsNotExist(err) {
			return created, dirs, fmt.Errorf("inspect candidate %s: %w", action.CandidatePath, err)
		}
		record, createdParents, err := publishArtifact(plan.RepoRoot, candidate, action.CandidatePath, action.DesiredSHA256, plan.PlanDigest)
		dirs = appendUniqueDirectories(dirs, createdParents...)
		if record.path != "" {
			created = append(created, record)
		}
		if err != nil {
			return created, dirs, err
		}
		if options.failAfter > 0 && len(created) >= options.failAfter {
			return created, dirs, fmt.Errorf("injected bootstrap candidate failure after %d published artifacts", len(created))
		}
	}
	return created, dirs, nil
}

func validateArtifactsMatchPlan(artifacts []desiredArtifact, actions []Action) (map[string]desiredArtifact, error) {
	if len(artifacts) != len(actions) {
		return nil, fmt.Errorf("bootstrap plan artifact count changed: plan=%d current=%d", len(actions), len(artifacts))
	}
	byPath := make(map[string]desiredArtifact, len(artifacts))
	for _, artifact := range artifacts {
		byPath[artifact.path] = artifact
	}
	for _, action := range actions {
		artifact, ok := byPath[action.Path]
		if !ok {
			return nil, fmt.Errorf("bootstrap plan artifact no longer resolves: %s", action.Path)
		}
		digest, err := artifactSHA256(artifact)
		if err != nil {
			return nil, err
		}
		if digest != action.DesiredSHA256 || artifact.mode != action.Mode || artifact.component != action.Component {
			return nil, fmt.Errorf("bootstrap plan artifact drifted since planning: %s", action.Path)
		}
	}
	return byPath, nil
}

func preflightPlanState(plan *Plan) error {
	for _, action := range plan.Actions {
		target := filepath.Join(plan.RepoRoot, filepath.FromSlash(action.Path))
		info, err := os.Lstat(target)
		if action.State == ActionCreate {
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("recheck bootstrap target %s: %w", action.Path, err)
			}
			return fmt.Errorf("bootstrap plan is stale: %s appeared after planning", action.Path)
		}
		if err != nil {
			return fmt.Errorf("bootstrap plan is stale: %s changed after planning: %w", action.Path, err)
		}
		kind := fileKind(info)
		if kind != action.ExistingKind || uint32(info.Mode().Perm()) != action.ExistingMode {
			return fmt.Errorf("bootstrap plan is stale: %s type or mode changed after planning", action.Path)
		}
		if kind == "file" {
			digest, err := fileSHA256(target)
			if err != nil {
				return err
			}
			if digest != action.ExistingSHA256 {
				return fmt.Errorf("bootstrap plan is stale: %s content changed after planning", action.Path)
			}
		}
	}
	return nil
}

type publicationHooks struct {
	beforeChmod   func(string) error
	beforeHash    func(string) error
	beforeCleanup func(string) error
}

func publishArtifact(root string, artifact desiredArtifact, relative, expectedSHA, planDigest string) (createdRecord, []createdDirectory, error) {
	return publishArtifactWithHooks(root, artifact, relative, expectedSHA, planDigest, publicationHooks{})
}

func publishArtifactWithHooks(root string, artifact desiredArtifact, relative, expectedSHA, planDigest string, hooks publicationHooks) (createdRecord, []createdDirectory, error) {
	target, err := safeBootstrapTarget(root, relative)
	if err != nil {
		return createdRecord{}, nil, err
	}
	createdDirs, err := createSafeParents(root, filepath.Dir(target))
	if err != nil {
		return createdRecord{}, createdDirs, err
	}
	parent, parentInfo, name, err := openCreatedParent(target)
	if err != nil {
		return createdRecord{}, createdDirs, err
	}
	closeParent := true
	defer func() {
		if closeParent {
			_ = parent.Close()
		}
	}()
	stageName := "." + name + ".reconc-bootstrap-" + planDigest[:12] + ".tmp"
	stagePath := filepath.Join(filepath.Dir(target), stageName)
	file, err := parent.OpenFile(stageName, os.O_CREATE|os.O_EXCL|os.O_RDWR, os.FileMode(artifact.mode))
	if err != nil {
		return createdRecord{}, createdDirs, fmt.Errorf("create bootstrap staging file for %s: %w", relative, err)
	}
	stageOpen := true
	cleanupStage := func() error {
		var cleanupErr error
		if stageOpen {
			cleanupErr = errors.Join(cleanupErr, file.Close())
			stageOpen = false
		}
		removeErr := parent.Remove(stageName)
		if !errors.Is(removeErr, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, removeErr)
		}
		return cleanupErr
	}
	writeErr := writeArtifactBody(file, artifact)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if writeErr != nil {
		return createdRecord{}, createdDirs, combineWriteFailure("stage bootstrap artifact "+relative, writeErr, nil, cleanupStage())
	}
	stagedSHA, stagedInfo, err := hashOpenedCreatedFile(file, stagePath)
	if err != nil {
		return createdRecord{}, createdDirs, combineWriteFailure("verify staged bootstrap artifact "+relative, err, nil, cleanupStage())
	}
	if stagedSHA != expectedSHA {
		return createdRecord{}, createdDirs, combineWriteFailure("verify staged bootstrap artifact "+relative, fmt.Errorf("checksum mismatch: expected %s, got %s", expectedSHA, stagedSHA), nil, cleanupStage())
	}
	stagedPathInfo, err := parent.Lstat(stageName)
	if err != nil || !sameCreatedFile(stagedInfo, stagedPathInfo) {
		if err == nil {
			err = errors.New("staging file changed identity")
		}
		return createdRecord{}, createdDirs, combineWriteFailure("inspect staged bootstrap artifact "+relative, err, nil, cleanupStage())
	}
	var published *os.File
	if err := parent.Link(stageName, name); err != nil {
		if os.IsExist(err) {
			return createdRecord{}, createdDirs, combineWriteFailure("publish bootstrap artifact "+relative, err, nil, cleanupStage())
		}
		// Filesystems without hardlink support (FAT/exFAT, some network
		// mounts) fail here. Fall back to an O_EXCL copy that keeps the
		// create-only guarantee; the published-checksum re-verification
		// below still guards content integrity.
		var copyErr error
		published, copyErr = copyStagedExclusiveRoot(parent, file, name, os.FileMode(artifact.mode))
		if copyErr != nil {
			return createdRecord{}, createdDirs, combineWriteFailure("publish bootstrap artifact "+relative, fmt.Errorf("hardlink: %v; exclusive copy: %w", err, copyErr), nil, cleanupStage())
		}
	} else {
		published = file
		file = nil
		stageOpen = false
	}
	publishedInfo, err := published.Stat()
	if err != nil {
		_ = published.Close()
		return createdRecord{}, createdDirs, combineWriteFailure("inspect published bootstrap artifact "+relative, err, nil, cleanupStage())
	}
	currentTarget, err := parent.Lstat(name)
	if err != nil || !sameCreatedFile(publishedInfo, currentTarget) {
		if err == nil {
			err = errors.New("published artifact changed identity")
		}
		_ = published.Close()
		return createdRecord{}, createdDirs, combineWriteFailure("inspect published bootstrap artifact "+relative, err, nil, cleanupStage())
	}
	record := createdRecord{
		path: target, sha256: expectedSHA, file: published, info: publishedInfo,
		parent: parent, parentInfo: parentInfo, name: name,
	}
	closeParent = false
	if err := validateCreatedParent(parent, parentInfo, target); err != nil {
		cleanupErr := cleanupStage()
		_ = record.close()
		return createdRecord{}, createdDirs, combineWriteFailure("verify published bootstrap artifact "+relative, err, nil, cleanupErr)
	}
	var modeErr error
	if hooks.beforeChmod != nil {
		modeErr = hooks.beforeChmod(target)
	}
	if modeErr == nil {
		modeErr = published.Chmod(os.FileMode(artifact.mode))
	}
	if modeErr != nil {
		removeStageErr := cleanupStage()
		removeTargetErr := removeCreatedRecord(&record)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure("set bootstrap artifact mode "+relative, modeErr, removeTargetErr, removeStageErr)
	}
	var hashErr error
	if hooks.beforeHash != nil {
		hashErr = hooks.beforeHash(target)
	}
	var publishedSHA string
	var hashedInfo os.FileInfo
	if hashErr == nil {
		publishedSHA, hashedInfo, hashErr = hashOpenedCreatedFile(published, target)
	}
	if hashErr != nil || publishedSHA != expectedSHA {
		if hashErr == nil {
			hashErr = fmt.Errorf("published checksum mismatch: expected %s, got %s", expectedSHA, publishedSHA)
		}
		removeStageErr := cleanupStage()
		removeTargetErr := removeCreatedRecord(&record)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure("verify published bootstrap artifact "+relative, hashErr, removeTargetErr, removeStageErr)
	}
	record.info = hashedInfo
	if err := validateCreatedTarget(&record); err != nil {
		removeStageErr := cleanupStage()
		removeTargetErr := removeCreatedRecord(&record)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure("verify published bootstrap artifact "+relative, err, removeTargetErr, removeStageErr)
	}
	var cleanupHookErr error
	if hooks.beforeCleanup != nil {
		cleanupHookErr = hooks.beforeCleanup(target)
	}
	cleanupErr := errors.Join(cleanupHookErr, cleanupStage())
	if cleanupErr != nil {
		removeTargetErr := removeCreatedRecord(&record)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure("remove bootstrap staging file "+relative, cleanupErr, removeTargetErr, nil)
	}
	if err := validateCreatedTarget(&record); err != nil {
		removeTargetErr := removeCreatedRecord(&record)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure("verify published bootstrap artifact "+relative, err, removeTargetErr, nil)
	}
	return record, createdDirs, nil
}

func copyStagedExclusiveRoot(parent *os.Root, source *os.File, target string, mode os.FileMode) (*os.File, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	out, err := parent.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_RDWR, mode)
	if err != nil {
		return nil, err
	}
	cleanup := func(primary error) (*os.File, error) {
		return nil, errors.Join(primary, out.Close(), parent.Remove(target))
	}
	if _, err := io.Copy(out, source); err != nil {
		return cleanup(err)
	}
	if err := out.Sync(); err != nil {
		return cleanup(err)
	}
	return out, nil
}

// copyStagedExclusive publishes the staged file to target with the same
// create-only semantics as os.Link: an existing target fails with
// os.ErrExist and is never overwritten.
func copyStagedExclusive(stage, target string, mode os.FileMode) error {
	source, err := os.Open(stage)
	if err != nil {
		return err
	}
	defer source.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, source); err != nil {
		_ = out.Close()
		_ = os.Remove(target)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(target)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

func writeArtifactBody(target *os.File, artifact desiredArtifact) error {
	if artifact.sourcePath == "" {
		written, err := target.Write(artifact.content)
		if err != nil {
			return err
		}
		if written != len(artifact.content) {
			return fmt.Errorf("short bootstrap artifact write: %d of %d bytes", written, len(artifact.content))
		}
		return nil
	}
	source, err := os.Open(artifact.sourcePath)
	if err != nil {
		return fmt.Errorf("open binary source: %w", err)
	}
	written, copyErr := io.Copy(target, io.LimitReader(source, maxBinaryBytes+1))
	closeErr := source.Close()
	if copyErr != nil {
		return fmt.Errorf("copy binary source: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close binary source: %w", closeErr)
	}
	if written > maxBinaryBytes {
		return fmt.Errorf("binary source exceeds %d bytes", maxBinaryBytes)
	}
	return nil
}

func safeBootstrapTarget(root, relative string) (string, error) {
	if relative == "" || strings.Contains(relative, "\\") {
		return "", fmt.Errorf("unsafe bootstrap path %q", relative)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bootstrap path escapes repository: %q", relative)
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bootstrap path escapes repository: %q", relative)
	}
	return target, nil
}

func createSafeParents(root, parent string) ([]createdDirectory, error) {
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("bootstrap parent escapes repository: %s", parent)
	}
	if relative == "." {
		return []createdDirectory{}, nil
	}
	created := []createdDirectory{}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return created, fmt.Errorf("create bootstrap parent %s: %w", current, err)
			}
			directory, err := captureCreatedDirectory(current)
			if err != nil {
				removeErr := os.Remove(current)
				return created, combineWriteFailure("inspect created bootstrap parent "+current, err, nil, removeErr)
			}
			created = append(created, directory)
			continue
		}
		if err != nil {
			return created, fmt.Errorf("inspect bootstrap parent %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return created, fmt.Errorf("bootstrap parent is not a real directory: %s", current)
		}
	}
	return created, nil
}

func rollbackCreated(root string, created []createdRecord, dirs []createdDirectory) ([]string, error) {
	defer closeCreatedDirectoryIdentities(dirs)
	rolledBack := []string{}
	errors := []string{}
	for index := len(created) - 1; index >= 0; index-- {
		record := &created[index]
		if err := removeCreatedRecord(record); err != nil {
			errors = append(errors, "remove rollback target "+record.path+": "+err.Error())
			continue
		}
		relative, relErr := filepath.Rel(root, record.path)
		if relErr != nil {
			relative = record.path
		}
		rolledBack = append(rolledBack, filepath.ToSlash(relative))
	}
	for _, directory := range deepestDirectoriesFirst(dirs) {
		info, err := os.Lstat(directory.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			errors = append(errors, "inspect rollback directory "+directory.path+": "+err.Error())
			continue
		}
		if !sameDirectoryIdentity(directory.identity, info) {
			errors = append(errors, "refuse rollback of externally replaced directory "+directory.path)
			continue
		}
		entries, err := boundedio.ReadDirNoSymlink(directory.path, maxBootstrapDirectoryEntries)
		if err != nil {
			errors = append(errors, "read rollback directory "+directory.path+": "+err.Error())
			continue
		}
		if len(entries) > 0 {
			errors = append(errors, "refuse rollback of non-empty transaction directory "+directory.path)
			continue
		}
		current, err := os.Lstat(directory.path)
		if err != nil || !sameDirectoryIdentity(directory.identity, current) {
			if err == nil {
				err = fmt.Errorf("directory identity changed")
			}
			errors = append(errors, "refuse rollback of externally replaced directory "+directory.path+": "+err.Error())
			continue
		}
		if err := os.Remove(directory.path); err != nil && !os.IsNotExist(err) {
			errors = append(errors, "remove rollback directory "+directory.path+": "+err.Error())
		}
	}
	sort.Strings(rolledBack)
	if len(errors) > 0 {
		return rolledBack, fmt.Errorf("rollback incomplete: %s", strings.Join(errors, "; "))
	}
	return rolledBack, nil
}

func captureCreatedDirectory(path string) (createdDirectory, error) {
	identity, err := captureDirectoryIdentity(path)
	if err != nil {
		return createdDirectory{}, err
	}
	return createdDirectory{path: path, identity: identity}, nil
}

func removeCreatedRecord(record *createdRecord) error {
	defer record.close()
	if record.parent == nil || record.file == nil || record.info == nil {
		if _, err := os.Lstat(record.path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("created file identity is unavailable")
	}
	current, err := record.parent.Lstat(record.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect created file: %w", err)
	}
	if err := validateCreatedParent(record.parent, record.parentInfo, record.path); err != nil {
		return fmt.Errorf("refuse removal after parent replacement: %w", err)
	}
	opened, err := record.file.Stat()
	if err != nil || !sameCreatedFile(record.info, opened) || !sameCreatedFile(opened, current) {
		return fmt.Errorf("refuse removal of externally replaced file")
	}
	digest, info, err := hashOpenedCreatedFile(record.file, record.path)
	if err != nil {
		return fmt.Errorf("verify created file: %w", err)
	}
	if digest != record.sha256 {
		return fmt.Errorf("refuse removal of externally changed file")
	}
	record.info = info
	if err := validateCreatedTarget(record); err != nil {
		return fmt.Errorf("refuse removal after target replacement: %w", err)
	}
	if err := record.parent.Remove(record.name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func captureCreatedRecord(path string) (createdRecord, error) {
	parent, parentInfo, name, err := openCreatedParent(path)
	if err != nil {
		return createdRecord{}, err
	}
	file, _, err := openCreatedFile(parent, name, path)
	if err != nil {
		_ = parent.Close()
		return createdRecord{}, err
	}
	digest, info, err := hashOpenedCreatedFile(file, path)
	if err != nil {
		_ = file.Close()
		_ = parent.Close()
		return createdRecord{}, err
	}
	record := createdRecord{
		path: path, sha256: digest, file: file, info: info,
		parent: parent, parentInfo: parentInfo, name: name,
	}
	if err := validateCreatedTarget(&record); err != nil {
		_ = record.close()
		return createdRecord{}, err
	}
	return record, nil
}

func appendUniqueDirectories(directories []createdDirectory, additions ...createdDirectory) []createdDirectory {
	seen := map[string]bool{}
	for _, directory := range directories {
		seen[directory.path] = true
	}
	for _, directory := range additions {
		if !seen[directory.path] {
			seen[directory.path] = true
			directories = append(directories, directory)
			continue
		}
		closeDirectoryIdentity(directory.identity)
	}
	return directories
}

func closeCreatedDirectoryIdentities(directories []createdDirectory) {
	for _, directory := range directories {
		closeDirectoryIdentity(directory.identity)
	}
}

func deepestDirectoriesFirst(directories []createdDirectory) []createdDirectory {
	result := append([]createdDirectory{}, directories...)
	sort.Slice(result, func(i, j int) bool {
		depthI := strings.Count(filepath.Clean(result[i].path), string(filepath.Separator))
		depthJ := strings.Count(filepath.Clean(result[j].path), string(filepath.Separator))
		if depthI == depthJ {
			return result[i].path > result[j].path
		}
		return depthI > depthJ
	})
	return result
}

func fileKind(info os.FileInfo) string {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	case info.IsDir():
		return "directory"
	case info.Mode().IsRegular():
		return "file"
	default:
		return "special"
	}
}

func joinApplyRollbackError(applyErr, rollbackErr error) error {
	if rollbackErr == nil {
		return applyErr
	}
	// errors.Join preserves both error chains so errors.Is/As reach the
	// primary apply failure as well as the rollback failure.
	return errors.Join(applyErr, fmt.Errorf("automatic rollback failed: %w", rollbackErr))
}
