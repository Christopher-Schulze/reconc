package bootstrap

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/compiler"
)

type applyOptions struct {
	failAfter int
}

type createdRecord struct {
	path   string
	sha256 string
	file   os.FileInfo
}

type createdDirectory struct {
	path     string
	identity directoryIdentity
}

func Apply(plan *Plan, productVersion string) (*Report, error) {
	return apply(plan, productVersion, applyOptions{})
}

func apply(plan *Plan, productVersion string, options applyOptions) (*Report, error) {
	report := &Report{
		FormatVersion: ReportFormatVersion, Status: ApplyRolledBack,
		Created: []string{}, Unchanged: []string{}, Candidates: []string{}, RolledBack: []string{},
	}
	if err := ValidatePlan(plan); err != nil {
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
	artifacts, err := buildDesiredArtifacts(plan.RepoRoot, plan.Selection)
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
	defer func() {
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
		lockPath := filepath.Join(plan.RepoRoot, ".reconc", "policy.lock.json")
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
		report.Created = append(report.Created, ".reconc/policy.lock.json")
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
	report.Status = ApplyComplete
	report.NextAction = "Bootstrap verified. Run repository-native tests, then finish the active bootstrap TASK if the governed profile was selected."
	sort.Strings(report.Created)
	sort.Strings(report.Unchanged)
	return report, nil
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

func publishArtifact(root string, artifact desiredArtifact, relative, expectedSHA, planDigest string) (createdRecord, []createdDirectory, error) {
	target, err := safeBootstrapTarget(root, relative)
	if err != nil {
		return createdRecord{}, nil, err
	}
	createdDirs, err := createSafeParents(root, filepath.Dir(target))
	if err != nil {
		return createdRecord{}, createdDirs, err
	}
	stage := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".reconc-bootstrap-"+planDigest[:12]+".tmp")
	file, err := os.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(artifact.mode))
	if err != nil {
		return createdRecord{}, createdDirs, fmt.Errorf("create bootstrap staging file for %s: %w", relative, err)
	}
	writeErr := writeArtifactBody(file, artifact)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		removeErr := os.Remove(stage)
		primary := writeErr
		if primary == nil {
			primary = closeErr
			closeErr = nil
		}
		return createdRecord{}, createdDirs, combineWriteFailure("stage bootstrap artifact "+relative, primary, closeErr, removeErr)
	}
	stagedSHA, err := fileSHA256(stage)
	if err != nil {
		removeErr := os.Remove(stage)
		return createdRecord{}, createdDirs, combineWriteFailure("verify staged bootstrap artifact "+relative, err, nil, removeErr)
	}
	if stagedSHA != expectedSHA {
		removeErr := os.Remove(stage)
		return createdRecord{}, createdDirs, combineWriteFailure("verify staged bootstrap artifact "+relative, fmt.Errorf("checksum mismatch: expected %s, got %s", expectedSHA, stagedSHA), nil, removeErr)
	}
	stagedInfo, err := os.Lstat(stage)
	if err != nil {
		removeErr := os.Remove(stage)
		return createdRecord{}, createdDirs, combineWriteFailure("inspect staged bootstrap artifact "+relative, err, nil, removeErr)
	}
	if !stagedInfo.Mode().IsRegular() || stagedInfo.Mode()&os.ModeSymlink != 0 {
		removeErr := os.Remove(stage)
		return createdRecord{}, createdDirs, combineWriteFailure("inspect staged bootstrap artifact "+relative, fmt.Errorf("staging path is not a real regular file"), nil, removeErr)
	}
	if err := os.Link(stage, target); err != nil {
		removeErr := os.Remove(stage)
		return createdRecord{}, createdDirs, combineWriteFailure("publish bootstrap artifact "+relative, err, nil, removeErr)
	}
	publishedInfo, err := os.Lstat(target)
	if err != nil {
		removeTargetErr := os.Remove(target)
		removeStageErr := os.Remove(stage)
		return createdRecord{}, createdDirs, combineWriteFailure("inspect published bootstrap artifact "+relative, err, removeTargetErr, removeStageErr)
	}
	record := createdRecord{path: target, sha256: expectedSHA, file: publishedInfo}
	if err := os.Chmod(target, os.FileMode(artifact.mode)); err != nil {
		removeTargetErr := removeCreatedRecord(record)
		removeStageErr := os.Remove(stage)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure("set bootstrap artifact mode "+relative, err, removeTargetErr, removeStageErr)
	}
	publishedSHA, err := fileSHA256(target)
	if err != nil || publishedSHA != expectedSHA {
		if err == nil {
			err = fmt.Errorf("published checksum mismatch: expected %s, got %s", expectedSHA, publishedSHA)
		}
		removeTargetErr := removeCreatedRecord(record)
		removeStageErr := os.Remove(stage)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure("verify published bootstrap artifact "+relative, err, removeTargetErr, removeStageErr)
	}
	if err := os.Remove(stage); err != nil {
		removeTargetErr := removeCreatedRecord(record)
		if removeTargetErr == nil {
			record = createdRecord{}
		}
		return record, createdDirs, combineWriteFailure("remove bootstrap staging file "+relative, err, removeTargetErr, nil)
	}
	return record, createdDirs, nil
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
		record := created[index]
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
		entries, err := os.ReadDir(directory.path)
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

func removeCreatedRecord(record createdRecord) error {
	info, err := os.Lstat(record.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect created file: %w", err)
	}
	if record.file == nil || !os.SameFile(info, record.file) {
		return fmt.Errorf("refuse removal of externally replaced file")
	}
	digest, err := fileSHA256(record.path)
	if err != nil {
		return fmt.Errorf("verify created file: %w", err)
	}
	if digest != record.sha256 {
		return fmt.Errorf("refuse removal of externally changed file")
	}
	return os.Remove(record.path)
}

func captureCreatedRecord(path string) (createdRecord, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return createdRecord{}, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return createdRecord{}, fmt.Errorf("created path is not a real regular file: %s", path)
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return createdRecord{}, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return createdRecord{}, err
	}
	if !os.SameFile(before, after) {
		return createdRecord{}, fmt.Errorf("created file changed identity while hashing: %s", path)
	}
	return createdRecord{path: path, sha256: digest, file: after}, nil
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
	return fmt.Errorf("%v; %w", applyErr, rollbackErr)
}
