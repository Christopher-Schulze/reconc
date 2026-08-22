package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/hooks"
	reconruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/schema"
)

const maxSyncRollbackBytes = 64 << 20

type syncApplyOptions struct {
	failAfter      int
	interruptAfter int
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
	if err := ensureNoPendingRepositorySync(plan.RepoRoot); err != nil {
		report.Status = SyncRefused
		return err
	}
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
	desiredByPath := map[string][]byte{}
	mutations := []syncMutation{}
	for _, action := range plan.Actions {
		if action.State == SyncUnchanged {
			report.Unchanged = append(report.Unchanged, action.Path)
			continue
		}
		if !mutableSyncState(action.State) {
			report.Status = SyncRefused
			return fmt.Errorf("repository sync action became non-mutable: %s", action.Path)
		}
		artifact, artifactOK := artifactByPath[action.Path]
		desired, desiredErr := desiredSyncBytes(plan.RepoRoot, action, artifact, artifactOK, productVersion)
		if desiredErr != nil {
			report.Status = SyncRefused
			return desiredErr
		}
		if bytesSHA256(desired) != action.DesiredSHA256 {
			report.Status = SyncRefused
			return fmt.Errorf("repository sync desired bytes drifted for %s", action.Path)
		}
		desiredByPath[action.Path] = desired
		mutations = append(mutations, syncMutation{
			Path: action.Path, Mode: action.Mode, After: desired,
			Created: action.State == SyncCreateOwned,
		})
	}
	receiptNeedsAdvance := repositoryReceiptNeedsAdvance(currentReceipt, plan, len(mutations) > 0)
	var targetReceipt *RepositoryReceipt
	if receiptNeedsAdvance {
		targetReceipt, err = advanceRepositoryReceipt(
			plan.RepoRoot, currentReceipt, plan, artifacts, desiredByPath,
		)
		if err != nil {
			report.Status = SyncRefused
			return err
		}
		receiptBody, encodeErr := encodeRepositoryReceipt(targetReceipt)
		if encodeErr != nil {
			report.Status = SyncRefused
			return encodeErr
		}
		receiptPath, pathErr := safeRepositorySyncPath(plan.RepoRoot, RepositoryReceiptRelativePath)
		if pathErr != nil {
			report.Status = SyncRefused
			return pathErr
		}
		_, lstatErr := os.Lstat(receiptPath)
		receiptCreated := os.IsNotExist(lstatErr)
		if lstatErr != nil && !receiptCreated {
			report.Status = SyncRefused
			return fmt.Errorf("inspect repository receipt before sync: %w", lstatErr)
		}
		mutations = append(mutations, syncMutation{
			Path: RepositoryReceiptRelativePath, Mode: 0o644,
			After: receiptBody, Created: receiptCreated,
		})
	}
	if len(mutations) == 0 {
		report.ReceiptTo = currentReceipt.ReceiptDigest
		verification, err := VerifyRepository(plan.RepoRoot, productVersion)
		if err != nil {
			report.Status = SyncRefused
			return err
		}
		report.Verification = append(report.Verification, verification.Checks...)
		if !verification.Valid {
			report.Status = SyncRefused
			return fmt.Errorf("repository sync verification failed: %s", verification.NextAction)
		}
		report.Status = SyncComplete
		report.NextAction = "Repository-owned Reconc artifacts already match the running product."
		sort.Strings(report.Unchanged)
		return nil
	}
	transaction, err := buildRepositorySyncTransaction(
		plan.RepoRoot, productVersion, plan.PlanDigest, mutations, true,
	)
	if err != nil {
		report.Status = SyncRefused
		return err
	}
	publishErr := publishRepositorySyncTransaction(
		plan.RepoRoot, transaction, mutations, options.failAfter, options.interruptAfter,
	)
	if publishErr != nil {
		if errors.Is(publishErr, errRepositorySyncInterrupted) {
			report.Status = SyncRefused
			report.NextAction = "reconc repo sync recover " + quoteBootstrapArgument(plan.RepoRoot)
			return publishErr
		}
		rolledBack, rollbackErr := rollbackRepositorySyncTransaction(plan.RepoRoot, transaction)
		report.RolledBack = append(report.RolledBack, rolledBack...)
		report.Status = SyncRolledBack
		return errors.Join(publishErr, rollbackErr)
	}
	for _, mutation := range mutations {
		report.Changed = append(report.Changed, mutation.Path)
	}
	if targetReceipt == nil {
		report.ReceiptTo = currentReceipt.ReceiptDigest
	} else {
		report.ReceiptTo = targetReceipt.ReceiptDigest
	}
	verification, err := verifyRepository(plan.RepoRoot, productVersion, true)
	if err != nil {
		rolledBack, rollbackErr := rollbackRepositorySyncTransaction(plan.RepoRoot, transaction)
		report.RolledBack = append(report.RolledBack, rolledBack...)
		report.Status = SyncRolledBack
		return errors.Join(err, rollbackErr)
	}
	report.Verification = append(report.Verification, verification.Checks...)
	if !verification.Valid {
		primary := fmt.Errorf("repository sync verification failed: %s", verification.NextAction)
		rolledBack, rollbackErr := rollbackRepositorySyncTransaction(plan.RepoRoot, transaction)
		report.RolledBack = append(report.RolledBack, rolledBack...)
		report.Status = SyncRolledBack
		return errors.Join(primary, rollbackErr)
	}
	if err := removeRepositorySyncTransaction(plan.RepoRoot); err != nil {
		report.Status = SyncRefused
		report.NextAction = "reconc repo sync recover " + quoteBootstrapArgument(plan.RepoRoot)
		return err
	}
	pruneObsoleteBootstrapReceipts(plan.RepoRoot, plan.PlanDigest)
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
	return verifyRepository(repoRoot, expectedProductVersion, false)
}

func verifyRepository(repoRoot, expectedProductVersion string, allowPendingTransaction bool) (*SyncVerification, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	verification := &SyncVerification{
		Schema: schema.Resolve(schema.RepositorySyncReport), FormatVersion: SyncVerifyFormatVersion,
		RepoRoot: root, Valid: true, Checks: []Check{},
	}
	if !allowPendingTransaction {
		if pendingErr := ensureNoPendingRepositorySync(root); pendingErr != nil {
			verification.add("repository-sync-transaction", false, pendingErr.Error())
			verification.NextAction = pendingErr.Error()
			return verification, nil
		}
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
	if expectedProductVersion == "" {
		verification.add("policy-packs", true, fmt.Sprintf("%d receipt pack identities structurally verified", len(receipt.PolicyPacks)))
		verification.add("harness-packs", true, fmt.Sprintf("%d receipt pack identities structurally verified", len(receipt.HarnessPacks)))
	} else {
		expectedPolicyPacks, packErr := policyPackIdentities(packNames(receipt.PolicyPacks))
		if packErr != nil {
			verification.add("policy-packs", false, packErr.Error())
		} else if !equalPolicyPackIdentities(receipt.PolicyPacks, expectedPolicyPacks) {
			verification.add("policy-packs", false, "receipt policy-pack identities differ from the packs embedded in the running product")
		} else {
			verification.add("policy-packs", true, fmt.Sprintf("%d embedded pack identities verified", len(expectedPolicyPacks)))
		}
		harnessSelection := Selection{HarnessPacks: make([]HarnessPackSelection, len(receipt.HarnessPacks))}
		expectedHarnessPacks, harnessErr := harnessPackIdentities(harnessSelection, expectedProductVersion)
		if harnessErr != nil {
			verification.add("harness-packs", false, harnessErr.Error())
		} else if !equalHarnessPackIdentities(receipt.HarnessPacks, expectedHarnessPacks) {
			verification.add("harness-packs", false, "receipt harness-pack identities differ from the packs embedded in the running product")
		} else {
			verification.add("harness-packs", true, fmt.Sprintf("%d embedded pack identities verified", len(expectedHarnessPacks)))
		}
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
	binary, binaryOS, binaryArch, binaryErr := repositoryBinaryOwnership(receipt)
	switch {
	case binaryErr != nil:
		verification.add("repository-binary", false, binaryErr.Error())
	case binary == nil:
		verification.add("repository-binary", true, "repository receipt owns no binary")
	case expectedProductVersion != "" && binaryOS == runtime.GOOS && binaryArch == runtime.GOARCH:
		running, selectionErr := CurrentBinarySelection()
		if selectionErr != nil {
			verification.add("repository-binary", false, selectionErr.Error())
		} else if running.SHA256 != binary.SHA256 {
			verification.add("repository-binary", false, "receipt-owned binary differs from the exact running product binary")
		} else {
			verification.add("repository-binary", true, "receipt-owned binary matches the exact running product binary")
		}
	default:
		verification.add(
			"repository-binary", true,
			fmt.Sprintf("receipt-owned %s/%s binary identity verified; running platform is %s/%s", binaryOS, binaryArch, runtime.GOOS, runtime.GOARCH),
		)
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
	if expectedProductVersion == "" {
		verification.add("policy-lock", true, "runtime freshness check not requested for receipt-only verification")
	} else if len(receipt.PolicySources) > 0 {
		if err := reconruntime.ValidatePolicyLockfile(root); err != nil {
			verification.add("policy-lock", false, err.Error())
		} else {
			verification.add("policy-lock", true, "compiled policy is fresh")
		}
	}
	if expectedProductVersion == "" {
		verification.add("hooks", true, "runtime hook check not requested for receipt-only verification")
	} else if len(receipt.Hooks) > 0 {
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

func equalPolicyPackIdentities(left, right []PolicyPackIdentity) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalHarnessPackIdentities(left, right []HarnessPackIdentity) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (verification *SyncVerification) add(name string, pass bool, detail string) {
	status := "PASS"
	if !pass {
		status = "FAIL"
		verification.Valid = false
	}
	verification.Checks = append(verification.Checks, Check{Name: name, Status: status, Detail: detail})
}

func desiredSyncBytes(root string, action SyncAction, artifact desiredArtifact, artifactOK bool, productVersion string) ([]byte, error) {
	if action.Component == "policy-lock" {
		_, body, err := compiler.RenderRepoPolicy(root, productVersion)
		if err != nil {
			return nil, fmt.Errorf("compile repository sync policy lock in memory: %w", err)
		}
		return body, nil
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
	body, err := boundedio.ReadRegularFile(artifact.sourcePath, maxBinaryBytes)
	if err != nil {
		return nil, fmt.Errorf("read repository sync binary source: %w", err)
	}
	return body, nil
}

func advanceRepositoryReceipt(
	root string,
	current *RepositoryReceipt,
	plan *SyncPlan,
	artifacts []desiredArtifact,
	desiredByPath map[string][]byte,
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
	artifactPaths := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		artifactPaths[artifact.path] = true
		body := append([]byte(nil), desiredByPath[artifact.path]...)
		if len(body) == 0 {
			var err error
			body, err = readRepositoryRegularFile(root, artifact.path)
			if err != nil {
				return nil, err
			}
		}
		if block := currentBlocks[artifact.path]; block.Path != "" {
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
		next.ManagedFiles = append(next.ManagedFiles, ManagedFile{
			Path: artifact.path, Mode: artifact.mode, SHA256: bytesSHA256(body),
			Component: artifact.component, Ownership: "file",
		})
	}
	for _, file := range current.ManagedFiles {
		if artifactPaths[file.Path] || !strings.HasPrefix(file.Component, "binary@") {
			continue
		}
		action, ok := syncActionByPath(plan.Actions, file.Path)
		if !ok || action.State != SyncUnchanged ||
			!binaryApprovedForProduct(file, plan.TargetProductVersion) {
			continue
		}
		body, err := readRepositoryRegularFile(root, file.Path)
		if err != nil {
			return nil, err
		}
		if bytesSHA256(body) != file.SHA256 {
			return nil, fmt.Errorf("unchanged approved binary no longer matches receipt checksum: %s", file.Path)
		}
		next.ManagedFiles = append(next.ManagedFiles, file)
	}
	for _, generated := range current.GeneratedArtifacts {
		body := append([]byte(nil), desiredByPath[generated.Path]...)
		if len(body) == 0 {
			var err error
			body, err = readRepositoryRegularFile(root, generated.Path)
			if err != nil {
				return nil, err
			}
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
