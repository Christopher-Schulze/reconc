package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/hooks"
	reconruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/schema"
)

const maxSyncRollbackBytes = 64 << 20

type syncBackup struct {
	path          string
	body          []byte
	mode          os.FileMode
	expectedAfter string
	created       bool
}

type syncApplyOptions struct {
	failAfter int
}

func ApplySyncPlan(plan *SyncPlan, exactDigest, productVersion string) (*SyncReport, error) {
	return applySyncPlan(plan, exactDigest, productVersion, syncApplyOptions{})
}

func applySyncPlan(plan *SyncPlan, exactDigest, productVersion string, options syncApplyOptions) (*SyncReport, error) {
	report := &SyncReport{
		Schema: schema.Resolve(schema.RepositorySyncReport), FormatVersion: SyncReportFormatVersion,
		Status: SyncRolledBack, Changed: []string{}, Unchanged: []string{},
		Candidates: []string{}, Migrations: []SyncMigration{}, RolledBack: []string{},
		Verification: []Check{},
	}
	if err := ValidateSyncPlan(plan); err != nil {
		return report, err
	}
	report.RepoRoot = plan.RepoRoot
	report.PlanDigest = plan.PlanDigest
	report.ProductFrom = plan.CurrentProductVersion
	report.ProductTo = plan.TargetProductVersion
	report.ReceiptFrom = plan.CurrentReceiptDigest
	report.Candidates = append(report.Candidates, plan.Candidates...)
	report.Migrations = append(report.Migrations, plan.Migrations...)
	if exactDigest != plan.PlanDigest {
		report.Status = SyncRefused
		return report, fmt.Errorf("repository sync digest mismatch: supplied %s, plan is %s", exactDigest, plan.PlanDigest)
	}
	if productVersion != plan.TargetProductVersion {
		report.Status = SyncRefused
		return report, fmt.Errorf("repository sync plan targets reconc %s, not the running %s", plan.TargetProductVersion, productVersion)
	}
	if len(plan.BlockingIssues) > 0 {
		report.Status = SyncRefused
		report.NextAction = plan.BlockingIssues[0]
		return report, fmt.Errorf("repository sync plan requires review: %s", plan.BlockingIssues[0])
	}
	lockAcquired := false
	err := withRepositoryTransactionLock(plan.RepoRoot, func() error {
		lockAcquired = true
		return applySyncPlanLocked(plan, report, productVersion, options)
	})
	if err != nil && !lockAcquired {
		report.Status = SyncRefused
	}
	if err != nil && report.NextAction == "" {
		report.NextAction = err.Error()
	}
	return report, err
}

func applySyncPlanLocked(plan *SyncPlan, report *SyncReport, productVersion string, options syncApplyOptions) error {
	currentPlan, err := BuildSyncPlan(plan.RepoRoot, productVersion)
	if err != nil {
		report.Status = SyncRefused
		return err
	}
	if currentPlan.PlanDigest != plan.PlanDigest {
		report.Status = SyncRefused
		return fmt.Errorf("repository sync plan is stale; rebuild it and review the new digest")
	}
	currentReceipt, _, err := loadRepositoryOwnership(plan.RepoRoot)
	if err != nil {
		report.Status = SyncRefused
		return err
	}
	selection, err := selectionFromRepositoryReceipt(currentReceipt, productVersion)
	if err != nil {
		return err
	}
	artifacts, err := buildDesiredArtifacts(plan.RepoRoot, selection, productVersion)
	if err != nil {
		return err
	}
	artifactByPath := make(map[string]desiredArtifact, len(artifacts))
	for _, artifact := range artifacts {
		artifactByPath[artifact.path] = artifact
	}
	backups := []syncBackup{}
	created := []createdRecord{}
	createdDirs := []createdDirectory{}
	defer closeCreatedDirectoryIdentities(createdDirs)
	rollback := func(primary error) error {
		rolledBack, rollbackErr := rollbackSyncChanges(plan.RepoRoot, backups, created, createdDirs)
		report.RolledBack = append(report.RolledBack, rolledBack...)
		report.Status = SyncRolledBack
		return errors.Join(primary, rollbackErr)
	}
	for _, action := range plan.Actions {
		if action.State == SyncUnchanged {
			report.Unchanged = append(report.Unchanged, action.Path)
			continue
		}
		if !mutableSyncState(action.State) {
			return rollback(fmt.Errorf("repository sync action became non-mutable: %s", action.Path))
		}
		artifact, artifactOK := artifactByPath[action.Path]
		desired, desiredErr := desiredSyncBytes(plan.RepoRoot, action, artifact, artifactOK)
		if desiredErr != nil {
			return rollback(desiredErr)
		}
		if bytesSHA256(desired) != action.DesiredSHA256 {
			return rollback(fmt.Errorf("repository sync desired bytes drifted for %s", action.Path))
		}
		target, targetErr := safeBootstrapTarget(plan.RepoRoot, action.Path)
		if targetErr != nil {
			return rollback(targetErr)
		}
		if action.State == SyncCreateOwned {
			createArtifact := desiredArtifact{
				component: action.Component, path: action.Path,
				mode: action.Mode, content: desired,
			}
			record, dirs, publishErr := publishArtifact(
				plan.RepoRoot, createArtifact, action.Path, action.DesiredSHA256, plan.PlanDigest,
			)
			createdDirs = appendUniqueDirectories(createdDirs, dirs...)
			if record.path != "" {
				created = append(created, record)
			}
			if publishErr != nil {
				return rollback(publishErr)
			}
		} else {
			backup, backupErr := captureSyncBackup(target, action.DesiredSHA256)
			if backupErr != nil {
				return rollback(backupErr)
			}
			if totalSyncBackupBytes(backups)+len(backup.body) > maxSyncRollbackBytes {
				return rollback(fmt.Errorf("repository sync rollback set exceeds %d bytes", maxSyncRollbackBytes))
			}
			backups = append(backups, backup)
			if _, writeErr := atomicfile.WriteIfChanged(target, desired, os.FileMode(action.Mode)); writeErr != nil {
				return rollback(fmt.Errorf("publish repository sync artifact %s: %w", action.Path, writeErr))
			}
		}
		report.Changed = append(report.Changed, action.Path)
		if options.failAfter > 0 && len(report.Changed) >= options.failAfter {
			return rollback(fmt.Errorf("injected repository sync failure after %d artifacts", len(report.Changed)))
		}
	}
	if !repositoryReceiptNeedsAdvance(currentReceipt, plan, len(report.Changed) > 0) {
		report.ReceiptTo = currentReceipt.ReceiptDigest
		verification, err := VerifyRepository(plan.RepoRoot, productVersion)
		if err != nil {
			return rollback(err)
		}
		report.Verification = append(report.Verification, verification.Checks...)
		if !verification.Valid {
			return rollback(fmt.Errorf("repository sync verification failed: %s", verification.NextAction))
		}
		report.Status = SyncComplete
		report.NextAction = "Repository-owned Reconc artifacts already match the running product."
		sort.Strings(report.Unchanged)
		return nil
	}
	targetReceipt, err := advanceRepositoryReceipt(plan.RepoRoot, currentReceipt, plan, artifacts)
	if err != nil {
		return rollback(err)
	}
	receiptPath := filepath.Join(plan.RepoRoot, filepath.FromSlash(RepositoryReceiptRelativePath))
	receiptBackup, err := captureOptionalSyncBackup(receiptPath)
	if err != nil {
		return rollback(err)
	}
	receiptBody, err := encodeRepositoryReceipt(targetReceipt)
	if err != nil {
		return rollback(err)
	}
	receiptBackup.expectedAfter = bytesSHA256(receiptBody)
	if totalSyncBackupBytes(backups)+len(receiptBackup.body) > maxSyncRollbackBytes {
		return rollback(fmt.Errorf("repository sync rollback set exceeds %d bytes", maxSyncRollbackBytes))
	}
	backups = append(backups, receiptBackup)
	if _, err := writeRepositoryReceiptAtomic(plan.RepoRoot, targetReceipt); err != nil {
		return rollback(err)
	}
	report.Changed = append(report.Changed, RepositoryReceiptRelativePath)
	report.ReceiptTo = targetReceipt.ReceiptDigest
	verification, err := VerifyRepository(plan.RepoRoot, productVersion)
	if err != nil {
		return rollback(err)
	}
	report.Verification = append(report.Verification, verification.Checks...)
	if !verification.Valid {
		return rollback(fmt.Errorf("repository sync verification failed: %s", verification.NextAction))
	}
	report.Status = SyncComplete
	report.NextAction = "reconc check " + quoteBootstrapArgument(plan.RepoRoot)
	sort.Strings(report.Changed)
	sort.Strings(report.Unchanged)
	return nil
}

func repositoryReceiptNeedsAdvance(receipt *RepositoryReceipt, plan *SyncPlan, changed bool) bool {
	if changed || plan.LegacyReceiptImport ||
		receipt.ProductVersion != plan.TargetProductVersion ||
		len(receipt.PolicyPacks) != len(plan.TargetPolicyPacks) ||
		len(receipt.HarnessPacks) != len(plan.TargetHarnessPacks) {
		return true
	}
	for index := range receipt.PolicyPacks {
		if receipt.PolicyPacks[index] != plan.TargetPolicyPacks[index] {
			return true
		}
	}
	for index := range receipt.HarnessPacks {
		if receipt.HarnessPacks[index] != plan.TargetHarnessPacks[index] {
			return true
		}
	}
	return false
}

func VerifyRepository(repoRoot, expectedProductVersion string) (*SyncVerification, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	verification := &SyncVerification{
		Schema: schema.Resolve(schema.RepositorySyncReport), FormatVersion: SyncVerifyFormatVersion,
		RepoRoot: root, Valid: true, Checks: []Check{},
	}
	receipt, err := LoadRepositoryReceipt(root)
	if err != nil {
		verification.add("repository-receipt", false, err.Error())
		verification.NextAction = err.Error()
		return verification, nil
	}
	verification.ReceiptDigest = receipt.ReceiptDigest
	verification.add("repository-receipt", true, "portable receipt digest and strict structure verified")
	if expectedProductVersion != "" && receipt.ProductVersion != expectedProductVersion {
		verification.add("product-version", false, "receipt records "+receipt.ProductVersion+", running product is "+expectedProductVersion)
	} else {
		verification.add("product-version", true, receipt.ProductVersion)
	}
	for _, file := range receipt.ManagedFiles {
		body, readErr := readRepositoryRegularFile(root, file.Path)
		if readErr != nil {
			verification.add("managed-file:"+file.Path, false, readErr.Error())
			continue
		}
		info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(file.Path)))
		if statErr != nil || !modeSatisfies(info.Mode(), file.Mode) {
			verification.add("managed-file:"+file.Path, false, "managed file mode drifted")
			continue
		}
		if bytesSHA256(body) != file.SHA256 {
			verification.add("managed-file:"+file.Path, false, "managed file checksum drifted")
			continue
		}
		verification.add("managed-file:"+file.Path, true, "checksum and mode verified")
	}
	for _, block := range receipt.ManagedBlocks {
		body, readErr := readRepositoryRegularFile(root, block.Path)
		if readErr != nil {
			verification.add("managed-block:"+block.Path, false, readErr.Error())
			continue
		}
		current, blockErr := extractManagedBlock(body, block.BlockStart, block.BlockEnd)
		if blockErr != nil || bytesSHA256(current) != block.ManagedSHA256 {
			verification.add("managed-block:"+block.Path, false, "managed block markers or bytes drifted")
			continue
		}
		verification.add("managed-block:"+block.Path, true, "managed bytes verified; outside bytes remain user-owned")
	}
	for _, generated := range receipt.GeneratedArtifacts {
		body, readErr := readRepositoryRegularFile(root, generated.Path)
		if readErr != nil || bytesSHA256(body) != generated.SHA256 {
			detail := "generated artifact checksum drifted"
			if readErr != nil {
				detail = readErr.Error()
			}
			verification.add("generated:"+generated.Path, false, detail)
			continue
		}
		verification.add("generated:"+generated.Path, true, "generated checksum verified")
	}
	if len(receipt.PolicySources) > 0 {
		if err := reconruntime.ValidatePolicyLockfile(root); err != nil {
			verification.add("policy-lock", false, err.Error())
		} else {
			verification.add("policy-lock", true, "compiled policy is fresh")
		}
	}
	if len(receipt.Hooks) > 0 {
		statuses, inspectErr := hooks.InspectPlatforms(root)
		if inspectErr != nil {
			verification.add("hooks", false, inspectErr.Error())
		} else {
			statusByKind := map[string]hooks.PlatformStatus{}
			for _, status := range statuses {
				statusByKind[status.Kind] = status
			}
			for _, kind := range receipt.Hooks {
				status := statusByKind[kind]
				verification.add("hook:"+kind, status.State == hooks.StateConfigured, string(status.State)+": "+status.Detail)
			}
		}
	}
	sort.Slice(verification.Checks, func(i, j int) bool { return verification.Checks[i].Name < verification.Checks[j].Name })
	if verification.Valid {
		verification.NextAction = "Repository-owned Reconc artifacts are verified."
	} else {
		for _, check := range verification.Checks {
			if check.Status == "FAIL" {
				verification.NextAction = check.Detail
				break
			}
		}
	}
	return verification, nil
}

func (verification *SyncVerification) add(name string, pass bool, detail string) {
	status := "PASS"
	if !pass {
		status = "FAIL"
		verification.Valid = false
	}
	verification.Checks = append(verification.Checks, Check{Name: name, Status: status, Detail: detail})
}

func desiredSyncBytes(root string, action SyncAction, artifact desiredArtifact, artifactOK bool) ([]byte, error) {
	if action.Component == "policy-lock" {
		current, err := readRepositoryRegularFile(root, action.Path)
		if err != nil {
			return nil, err
		}
		migrated, _, err := migratePolicyLockBytes(current)
		return migrated, err
	}
	if !artifactOK {
		return nil, fmt.Errorf("repository sync target artifact no longer resolves: %s", action.Path)
	}
	if artifact.sourcePath == "" {
		return append([]byte{}, artifact.content...), nil
	}
	info, err := os.Lstat(artifact.sourcePath)
	if err != nil {
		return nil, fmt.Errorf("inspect repository sync binary source: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxBinaryBytes {
		return nil, fmt.Errorf("repository sync binary source is not a bounded real regular file")
	}
	body, err := os.ReadFile(artifact.sourcePath)
	if err != nil {
		return nil, fmt.Errorf("read repository sync binary source: %w", err)
	}
	return body, nil
}

func captureSyncBackup(target, expectedAfter string) (syncBackup, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return syncBackup{}, fmt.Errorf("capture repository sync rollback state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return syncBackup{}, fmt.Errorf("repository sync rollback target is not a real regular file: %s", target)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		return syncBackup{}, fmt.Errorf("read repository sync rollback state: %w", err)
	}
	return syncBackup{
		path: target, body: body, mode: info.Mode().Perm(), expectedAfter: expectedAfter,
	}, nil
}

func captureOptionalSyncBackup(target string) (syncBackup, error) {
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		return syncBackup{path: target, mode: 0o644, created: true}, nil
	} else if err != nil {
		return syncBackup{}, fmt.Errorf("inspect repository receipt rollback state: %w", err)
	}
	return captureSyncBackup(target, "")
}

func totalSyncBackupBytes(backups []syncBackup) int {
	total := 0
	for _, backup := range backups {
		total += len(backup.body)
	}
	return total
}

func rollbackSyncChanges(root string, backups []syncBackup, created []createdRecord, dirs []createdDirectory) ([]string, error) {
	rolledBack := []string{}
	var rollbackErr error
	for index := len(backups) - 1; index >= 0; index-- {
		backup := backups[index]
		relative, _ := filepath.Rel(root, backup.path)
		relative = filepath.ToSlash(relative)
		if backup.created {
			current, err := os.ReadFile(backup.path)
			if err == nil {
				if backup.expectedAfter == "" || bytesSHA256(current) != backup.expectedAfter {
					err = fmt.Errorf("refuse rollback after concurrent change: %s", relative)
				} else {
					err = os.Remove(backup.path)
				}
			}
			if err != nil && !os.IsNotExist(err) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove created sync artifact %s: %w", relative, err))
				continue
			}
			rolledBack = append(rolledBack, relative)
			continue
		}
		current, err := os.ReadFile(backup.path)
		if err != nil || (backup.expectedAfter != "" && bytesSHA256(current) != backup.expectedAfter) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("refuse rollback after concurrent change: %s", relative))
			continue
		}
		if _, err := atomicfile.WriteIfChanged(backup.path, backup.body, backup.mode); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore repository sync artifact %s: %w", relative, err))
			continue
		}
		rolledBack = append(rolledBack, relative)
	}
	removed, removeErr := rollbackCreated(root, created, dirs)
	rolledBack = append(rolledBack, removed...)
	sort.Strings(rolledBack)
	return rolledBack, errors.Join(rollbackErr, removeErr)
}

func advanceRepositoryReceipt(
	root string,
	current *RepositoryReceipt,
	plan *SyncPlan,
	artifacts []desiredArtifact,
) (*RepositoryReceipt, error) {
	next := &RepositoryReceipt{
		Schema: schema.Resolve(schema.RepositoryInstall), FormatVersion: RepositoryReceiptFormatVersion,
		ProductVersion: plan.TargetProductVersion, Profile: current.Profile,
		PolicyPacks:   append([]PolicyPackIdentity{}, plan.TargetPolicyPacks...),
		HarnessPacks:  append([]HarnessPackIdentity{}, plan.TargetHarnessPacks...),
		Hooks:         append([]string{}, current.Hooks...),
		PolicySources: append([]string{}, current.PolicySources...),
		ManagedFiles:  []ManagedFile{}, ManagedBlocks: []ManagedBlock{},
		GeneratedArtifacts: []GeneratedArtifact{},
		UserOwnedPaths:     append([]string{}, current.UserOwnedPaths...),
		PlanDigest:         plan.PlanDigest, Generation: current.Generation + 1,
	}
	currentBlocks := make(map[string]ManagedBlock, len(current.ManagedBlocks))
	for _, block := range current.ManagedBlocks {
		currentBlocks[block.Path] = block
	}
	for _, artifact := range artifacts {
		if block := currentBlocks[artifact.path]; block.Path != "" {
			body, err := readRepositoryRegularFile(root, artifact.path)
			if err != nil {
				return nil, err
			}
			managed, err := extractManagedBlock(body, block.BlockStart, block.BlockEnd)
			if err != nil {
				return nil, err
			}
			next.ManagedBlocks = append(next.ManagedBlocks, ManagedBlock{
				Path: block.Path, BlockStart: block.BlockStart, BlockEnd: block.BlockEnd,
				ManagedSHA256: bytesSHA256(managed), WholeFileSHA256: bytesSHA256(body),
				Component: artifact.component,
			})
			continue
		}
		if !syncOwnsComponent(artifact.component) {
			continue
		}
		body, err := readRepositoryRegularFile(root, artifact.path)
		if err != nil {
			return nil, err
		}
		next.ManagedFiles = append(next.ManagedFiles, ManagedFile{
			Path: artifact.path, Mode: artifact.mode, SHA256: bytesSHA256(body),
			Component: artifact.component, Ownership: "file",
		})
	}
	for _, generated := range current.GeneratedArtifacts {
		body, err := readRepositoryRegularFile(root, generated.Path)
		if err != nil {
			return nil, err
		}
		next.GeneratedArtifacts = append(next.GeneratedArtifacts, GeneratedArtifact{
			Path: generated.Path, Generator: generated.Generator,
			Version: plan.TargetProductVersion, SHA256: bytesSHA256(body),
		})
	}
	normalizeRepositoryReceipt(next)
	digest, err := computeRepositoryReceiptDigest(next)
	if err != nil {
		return nil, err
	}
	next.ReceiptDigest = digest
	if err := ValidateRepositoryReceipt(next); err != nil {
		return nil, err
	}
	return next, nil
}
