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
	parentRef  *bootstrapRootRef
	parentInfo os.FileInfo
	name       string
}

type createdDirectory struct {
	path string
	// identity is a pointer so closeDirectoryIdentity can nil the handle
	// after the first close, making repeated closes (the rollback path and
	// the caller defers share the same slice backing array) a safe no-op.
	identity   *directoryIdentity
	parent     *os.Root
	parentRef  *bootstrapRootRef
	parentInfo os.FileInfo
	name       string
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
	retentionWarnings := pruneObsoleteBootstrapReceipts(plan.RepoRoot, plan.PlanDigest)
	report.Status = ApplyComplete
	sort.Strings(report.Created)
	sort.Strings(report.Unchanged)
	report.Summary = summarizeApply(plan, report)
	report.Summary.InspectionErrors = append(report.Summary.InspectionErrors, retentionWarnings...)
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
			if len(plan.PlanDigest) >= 12 {
				if recoveryErr := validateBootstrapRecoveryPair(plan, action, target); recoveryErr == nil {
					continue
				} else {
					return fmt.Errorf("bootstrap plan is stale: %s appeared after planning and has no exact recoverable stage: %w", action.Path, recoveryErr)
				}
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

func validateBootstrapRecoveryPair(plan *Plan, action Action, target string) (resultErr error) {
	parent, parentInfo, targetName, err := openCreatedParent(target)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, parent.Close())
	}()
	stageName := "." + targetName + ".reconc-bootstrap-" + plan.PlanDigest[:12] + ".tmp"
	stagePath := filepath.Join(filepath.Dir(target), stageName)
	stage, _, err := openCreatedFile(parent, stageName, stagePath)
	if err != nil {
		return err
	}
	stageSHA, _, stageErr := hashOpenedCreatedFile(stage, stagePath)
	stageCloseErr := stage.Close()
	if stageErr != nil || stageCloseErr != nil || stageSHA != action.DesiredSHA256 {
		if stageErr == nil && stageSHA != action.DesiredSHA256 {
			stageErr = fmt.Errorf("staging digest is %s, expected %s", stageSHA, action.DesiredSHA256)
		}
		return errors.Join(stageErr, stageCloseErr)
	}
	published, _, err := openCreatedFile(parent, targetName, target)
	if err != nil {
		return err
	}
	publishedSHA, _, publishedErr := hashOpenedCreatedFile(published, target)
	publishedCloseErr := published.Close()
	if publishedErr != nil || publishedCloseErr != nil || publishedSHA != action.DesiredSHA256 {
		if publishedErr == nil && publishedSHA != action.DesiredSHA256 {
			publishedErr = fmt.Errorf("published digest is %s, expected %s", publishedSHA, action.DesiredSHA256)
		}
		return errors.Join(publishedErr, publishedCloseErr)
	}
	return validateCreatedParent(parent, parentInfo, target)
}

type publicationHooks struct {
	beforeParentValidation func(string) error
	beforeChmod            func(string) error
	beforeHash             func(string) error
	beforeCleanup          func(string) error
	link                   func(*os.Root, string, string) error
}

func publishArtifact(root string, artifact desiredArtifact, relative, expectedSHA, planDigest string) (createdRecord, []createdDirectory, error) {
	return publishArtifactWithHooks(root, artifact, relative, expectedSHA, planDigest, publicationHooks{})
}

func publishArtifactWithHooks(root string, artifact desiredArtifact, relative, expectedSHA, planDigest string, hooks publicationHooks) (createdRecord, []createdDirectory, error) {
	target, err := safeBootstrapTarget(root, relative)
	if err != nil {
		return createdRecord{}, nil, err
	}
	rootRef, _, err := openBootstrapRoot(root)
	if err != nil {
		return createdRecord{}, nil, err
	}
	parentRef, parentInfo, createdDirs, err := createSafeParentsWithRoot(root, rootRef, filepath.Dir(target))
	if err != nil {
		_ = closeUnretainedBootstrapRootRefs(nil, createdDirs, parentRef, rootRef)
		return createdRecord{}, createdDirs, err
	}
	if parentRef == nil || parentRef.root == nil {
		_ = closeUnretainedBootstrapRootRefs(nil, createdDirs, parentRef, rootRef)
		return createdRecord{}, createdDirs, errors.New("bootstrap artifact parent handle is unavailable")
	}
	parent := parentRef.root
	name := filepath.Base(target)
	transferred := false
	defer func() {
		if transferred {
			_ = closeUnretainedBootstrapRootRefs(parentRef, createdDirs, rootRef)
			return
		}
		_ = closeUnretainedBootstrapRootRefs(nil, createdDirs, parentRef, rootRef)
	}()
	stageName := "." + name + ".reconc-bootstrap-" + planDigest[:12] + ".tmp"
	stagePath := filepath.Join(filepath.Dir(target), stageName)
	file, recovered, err := openBootstrapStage(parent, parentInfo, name, target, stageName, stagePath, expectedSHA, os.FileMode(artifact.mode))
	if err != nil {
		return createdRecord{}, createdDirs, fmt.Errorf("create bootstrap staging file for %s: %w", relative, err)
	}
	if recovered != nil {
		recovered.parentRef = parentRef
		transferred = true
		return *recovered, createdDirs, nil
	}
	stageOpen := true
	cleanupStage := func() error {
		var cleanupErr error
		if stageOpen {
			cleanupErr = errors.Join(cleanupErr, file.Close())
			stageOpen = false
		}
		removeErr := parent.Remove(stageName)
		if removeErr == nil {
			cleanupErr = errors.Join(cleanupErr, syncMutatedBootstrapParent(parent, parentInfo, stagePath))
		} else if !errors.Is(removeErr, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, removeErr)
		}
		return cleanupErr
	}
	writeErr := writeArtifactBody(file, artifact)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if writeErr == nil {
		writeErr = syncMutatedBootstrapParent(parent, parentInfo, stagePath)
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
	var linkErr error
	if hooks.link != nil {
		linkErr = hooks.link(parent, stageName, name)
	} else {
		linkErr = parent.Link(stageName, name)
	}
	if linkErr != nil {
		if os.IsExist(linkErr) {
			return createdRecord{}, createdDirs, combineWriteFailure("publish bootstrap artifact "+relative, linkErr, nil, cleanupStage())
		}
		// Filesystems without hardlink support (FAT/exFAT, some network
		// mounts) fail here. Fall back to an O_EXCL copy that keeps the
		// create-only guarantee; the published-checksum re-verification
		// below still guards content integrity.
		var copyErr error
		published, copyErr = copyStagedExclusiveRoot(parent, file, name, os.FileMode(artifact.mode))
		if copyErr != nil {
			return createdRecord{}, createdDirs, combineWriteFailure("publish bootstrap artifact "+relative, fmt.Errorf("hardlink: %v; exclusive copy: %w", linkErr, copyErr), nil, cleanupStage())
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
		parent: parent, parentRef: parentRef, parentInfo: parentInfo, name: name,
	}
	transferred = true
	if err := syncMutatedBootstrapParent(parent, parentInfo, target); err != nil {
		removeStageErr := cleanupStage()
		removeTargetErr := removeCreatedRecord(&record)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure(
			"commit published bootstrap artifact "+relative,
			err,
			removeTargetErr,
			removeStageErr,
		)
	}
	var parentValidationErr error
	if hooks.beforeParentValidation != nil {
		parentValidationErr = hooks.beforeParentValidation(target)
	}
	if parentValidationErr == nil {
		parentValidationErr = validateCreatedParent(parent, parentInfo, target)
	}
	if parentValidationErr != nil {
		cleanupErr := cleanupStage()
		removeTargetErr := removeCreatedRecord(&record)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure("verify published bootstrap artifact "+relative, parentValidationErr, removeTargetErr, cleanupErr)
	}
	var modeErr error
	if hooks.beforeChmod != nil {
		modeErr = hooks.beforeChmod(target)
	}
	if modeErr == nil {
		modeErr = published.Chmod(os.FileMode(artifact.mode))
	}
	if modeErr == nil {
		modeErr = published.Sync()
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

// openBootstrapStage is called only below the repository transaction lock. A
// deterministic stage left by a crashed transaction is recoverable only when
// its exact path, regular-file identity, and content digest match the current
// publication. Ambiguous residue is preserved for manual inspection.
func openBootstrapStage(
	parent *os.Root,
	parentInfo os.FileInfo,
	targetName, targetPath, stageName, stagePath, expectedSHA string,
	mode os.FileMode,
) (*os.File, *createdRecord, error) {
	file, err := parent.OpenFile(stageName, os.O_CREATE|os.O_EXCL|os.O_RDWR, mode)
	if err == nil {
		return file, nil, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, nil, err
	}
	recovered, recoverErr := recoverBootstrapStage(
		parent, parentInfo, targetName, targetPath, stageName, stagePath, expectedSHA, mode,
	)
	if recoverErr != nil {
		return nil, nil, recoverErr
	}
	if recovered != nil {
		return nil, recovered, nil
	}
	file, err = parent.OpenFile(stageName, os.O_CREATE|os.O_EXCL|os.O_RDWR, mode)
	if err != nil {
		return nil, nil, fmt.Errorf("recreate after exact stale-stage recovery: %w", err)
	}
	return file, nil, nil
}

func recoverBootstrapStage(
	parent *os.Root,
	parentInfo os.FileInfo,
	targetName, targetPath, stageName, stagePath, expectedSHA string,
	mode os.FileMode,
) (*createdRecord, error) {
	if err := validateCreatedParent(parent, parentInfo, targetPath); err != nil {
		return nil, fmt.Errorf("refuse stale-stage recovery after parent replacement: %w", err)
	}
	stage, _, err := openCreatedFile(parent, stageName, stagePath)
	if err != nil {
		return nil, staleBootstrapStageError(stagePath, err)
	}
	stageSHA, stableStageInfo, err := hashOpenedCreatedFile(stage, stagePath)
	if err != nil || stageSHA != expectedSHA {
		if err == nil {
			err = fmt.Errorf("content digest is %s, expected %s", stageSHA, expectedSHA)
		}
		return nil, errors.Join(staleBootstrapStageError(stagePath, err), stage.Close())
	}
	stageInfo := stableStageInfo

	targetPathInfo, targetErr := parent.Lstat(targetName)
	if errors.Is(targetErr, os.ErrNotExist) {
		closeErr := stage.Close()
		if closeErr != nil {
			return nil, staleBootstrapStageError(stagePath, closeErr)
		}
		currentStage, inspectErr := parent.Lstat(stageName)
		if inspectErr != nil || !sameCreatedFile(stageInfo, currentStage) {
			if inspectErr == nil && !sameCreatedFile(stageInfo, currentStage) {
				inspectErr = errors.New("staging file changed identity before recovery")
			}
			return nil, staleBootstrapStageError(stagePath, inspectErr)
		}
		if err := removeBoundBootstrapEntry(parent, parentInfo, stageName, stagePath); err != nil {
			return nil, staleBootstrapStageError(stagePath, fmt.Errorf("remove exact residue: %w", err))
		}
		return nil, nil
	}
	if targetErr != nil {
		return nil, errors.Join(staleBootstrapStageError(stagePath, targetErr), stage.Close())
	}
	if targetPathInfo.Mode()&os.ModeSymlink != 0 || !targetPathInfo.Mode().IsRegular() {
		return nil, errors.Join(staleBootstrapStageError(stagePath, errors.New("published target is not a real regular file")), stage.Close())
	}
	target, _, err := openCreatedFileForMutation(parent, targetName, targetPath)
	if err != nil {
		return nil, errors.Join(staleBootstrapStageError(stagePath, err), stage.Close())
	}
	closeBoth := func(primary error) (*createdRecord, error) {
		return nil, errors.Join(staleBootstrapStageError(stagePath, primary), target.Close(), stage.Close())
	}
	targetSHA, _, err := hashOpenedCreatedFile(target, targetPath)
	if err != nil || targetSHA != expectedSHA {
		if err == nil {
			err = fmt.Errorf("published target digest is %s, expected %s", targetSHA, expectedSHA)
		}
		return closeBoth(err)
	}
	if err := target.Chmod(mode); err != nil {
		return closeBoth(fmt.Errorf("restore published target mode: %w", err))
	}
	if err := target.Sync(); err != nil {
		return closeBoth(fmt.Errorf("sync restored published target mode: %w", err))
	}
	targetSHA, stableTargetInfo, err := hashOpenedCreatedFile(target, targetPath)
	if err != nil || targetSHA != expectedSHA {
		if err == nil {
			err = errors.New("published target changed while restoring its mode")
		}
		return closeBoth(err)
	}
	if err := stage.Close(); err != nil {
		return nil, errors.Join(staleBootstrapStageError(stagePath, err), target.Close())
	}
	currentStage, err := parent.Lstat(stageName)
	if err != nil || !sameCreatedFile(stageInfo, currentStage) {
		if err == nil {
			err = errors.New("staging file changed identity before recovery")
		}
		return nil, errors.Join(staleBootstrapStageError(stagePath, err), target.Close())
	}
	if err := removeBoundBootstrapEntry(parent, parentInfo, stageName, stagePath); err != nil {
		return nil, errors.Join(staleBootstrapStageError(stagePath, fmt.Errorf("remove exact residue: %w", err)), target.Close())
	}
	record := &createdRecord{
		path: targetPath, sha256: expectedSHA, file: target, info: stableTargetInfo,
		parent: parent, parentInfo: parentInfo, name: targetName,
	}
	if err := validateCreatedTarget(record); err != nil {
		return nil, errors.Join(staleBootstrapStageError(stagePath, err), record.close())
	}
	return record, nil
}

func staleBootstrapStageError(path string, cause error) error {
	return fmt.Errorf(
		"reserved bootstrap staging residue is ambiguous at %s; inspect it and remove it manually only after confirming no repository transaction is active: %w",
		path, cause,
	)
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
		closeErr := out.Close()
		removeErr := parent.Remove(target)
		var syncErr error
		if removeErr == nil {
			syncErr = bootstrapDirectorySync(parent)
		}
		return nil, errors.Join(primary, closeErr, removeErr, syncErr)
	}
	if _, err := io.Copy(out, source); err != nil {
		return cleanup(err)
	}
	if err := out.Sync(); err != nil {
		return cleanup(err)
	}
	return out, nil
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
	rootPath := filepath.Clean(root)
	rootRef, _, err := openBootstrapRoot(rootPath)
	if err != nil {
		return nil, err
	}
	finalRef, _, created, err := createSafeParentsWithRoot(rootPath, rootRef, filepath.Clean(parent))
	if err != nil {
		_ = closeUnretainedBootstrapRootRefs(nil, created, finalRef, rootRef)
		return created, err
	}
	_ = closeUnretainedBootstrapRootRefs(nil, created, finalRef, rootRef)
	return created, nil
}

func rollbackCreatedDirectoryBound(directory createdDirectory) error {
	if directory.parentRef == nil || directory.parentRef.root == nil {
		return errors.New("rollback directory parent handle is unavailable")
	}
	parent := directory.parentRef.root
	if err := validateBoundBootstrapParent(parent, directory.parentInfo); err != nil {
		return err
	}
	boundInfo, err := parent.Lstat(directory.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if boundInfo.Mode()&os.ModeSymlink != 0 || !boundInfo.IsDir() || !sameDirectoryIdentity(directory.identity, boundInfo) {
		return errors.New("refuse rollback of externally replaced directory")
	}
	opened, err := parent.Open(directory.name)
	if err != nil {
		return err
	}
	openedInfo, statErr := opened.Stat()
	entries, readErr := opened.ReadDir(maxBootstrapDirectoryEntries + 1)
	afterInfo, afterErr := opened.Stat()
	closeErr := opened.Close()
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if err := errors.Join(statErr, readErr, afterErr, closeErr); err != nil {
		return err
	}
	if !sameDirectoryIdentity(directory.identity, openedInfo) || !sameDirectoryIdentity(directory.identity, afterInfo) {
		return errors.New("refuse rollback of externally replaced directory")
	}
	if len(entries) > maxBootstrapDirectoryEntries {
		return fmt.Errorf("refuse rollback of transaction directory with more than %d entries", maxBootstrapDirectoryEntries)
	}
	if len(entries) > 0 {
		return errors.New("refuse rollback of non-empty transaction directory")
	}
	if err := validateBoundBootstrapParent(parent, directory.parentInfo); err != nil {
		return err
	}
	current, err := parent.Lstat(directory.name)
	if err != nil {
		return err
	}
	if !sameDirectoryIdentity(directory.identity, current) {
		return errors.New("refuse rollback of externally replaced directory")
	}
	if err := parent.Remove(directory.name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncBoundBootstrapParent(parent, directory.parentInfo)
}

func rollbackCreated(root string, created []createdRecord, dirs []createdDirectory) ([]string, error) {
	defer closeCreatedDirectoryIdentities(dirs)
	rolledBack := []string{}
	problems := []string{}
	for index := len(created) - 1; index >= 0; index-- {
		record := &created[index]
		if err := removeCreatedRecord(record); err != nil {
			problems = append(problems, "remove rollback target "+record.path+": "+err.Error())
			continue
		}
		relative, relErr := filepath.Rel(root, record.path)
		if relErr != nil {
			relative = record.path
		}
		rolledBack = append(rolledBack, filepath.ToSlash(relative))
	}
	for _, directory := range deepestDirectoriesFirst(dirs) {
		if directory.parentRef != nil {
			if err := rollbackCreatedDirectoryBound(directory); err != nil {
				problems = append(problems, "remove rollback directory "+directory.path+": "+err.Error())
			}
			continue
		}
		info, err := os.Lstat(directory.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			problems = append(problems, "inspect rollback directory "+directory.path+": "+err.Error())
			continue
		}
		if !sameDirectoryIdentity(directory.identity, info) {
			problems = append(problems, "refuse rollback of externally replaced directory "+directory.path)
			continue
		}
		entries, err := boundedio.ReadDirNoSymlink(directory.path, maxBootstrapDirectoryEntries)
		if err != nil {
			problems = append(problems, "read rollback directory "+directory.path+": "+err.Error())
			continue
		}
		if len(entries) > 0 {
			problems = append(problems, "refuse rollback of non-empty transaction directory "+directory.path)
			continue
		}
		current, err := os.Lstat(directory.path)
		if err != nil || !sameDirectoryIdentity(directory.identity, current) {
			if err == nil {
				err = fmt.Errorf("directory identity changed")
			}
			problems = append(problems, "refuse rollback of externally replaced directory "+directory.path+": "+err.Error())
			continue
		}
		parent, parentInfo, name, err := openCreatedParent(directory.path)
		if err != nil {
			problems = append(problems, "open rollback directory parent "+directory.path+": "+err.Error())
			continue
		}
		boundInfo, inspectErr := parent.Lstat(name)
		if inspectErr != nil || !sameDirectoryIdentity(directory.identity, boundInfo) {
			if inspectErr == nil {
				inspectErr = errors.New("directory identity changed")
			}
			problems = append(problems, "refuse rollback of externally replaced directory "+directory.path+": "+inspectErr.Error())
			_ = parent.Close()
			continue
		}
		removeErr := removeBoundBootstrapEntry(parent, parentInfo, name, directory.path)
		closeErr := parent.Close()
		if err := errors.Join(removeErr, closeErr); err != nil {
			problems = append(problems, "remove rollback directory "+directory.path+": "+err.Error())
		}
	}
	sort.Strings(rolledBack)
	if len(problems) > 0 {
		return rolledBack, fmt.Errorf("rollback incomplete: %s", strings.Join(problems, "; "))
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
	if record.parent == nil || record.file == nil || record.info == nil {
		if _, err := os.Lstat(record.path); errors.Is(err, os.ErrNotExist) {
			return record.close()
		}
		return fmt.Errorf("created file identity is unavailable")
	}
	current, err := record.parent.Lstat(record.name)
	if errors.Is(err, os.ErrNotExist) {
		return record.close()
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
	syncErr := syncMutatedBootstrapParent(record.parent, record.parentInfo, record.path)
	return errors.Join(syncErr, record.close())
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
	duplicates := []createdDirectory{}
	for _, directory := range additions {
		if !seen[directory.path] {
			seen[directory.path] = true
			directories = append(directories, directory)
			continue
		}
		duplicates = append(duplicates, directory)
	}
	for _, directory := range duplicates {
		closeDirectoryIdentity(directory.identity)
		if directory.parentRef != nil && !bootstrapRootRefReferenced(directories, directory.parentRef) {
			_ = closeBootstrapRootRef(directory.parentRef)
		}
	}
	return directories
}

func closeCreatedDirectoryIdentities(directories []createdDirectory) {
	refs := map[*bootstrapRootRef]bool{}
	for _, directory := range directories {
		closeDirectoryIdentity(directory.identity)
		if directory.parentRef != nil && !refs[directory.parentRef] {
			refs[directory.parentRef] = true
			_ = closeBootstrapRootRef(directory.parentRef)
		}
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
