package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	reconruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/schema"
)

type syncResolutionOptions struct {
	failAfter      int
	interruptAfter int
}

func ResolveRepositorySync(
	request SyncResolutionRequest,
	productVersion string,
) (*SyncResolutionReport, error) {
	return resolveRepositorySync(request, productVersion, syncResolutionOptions{})
}

func resolveRepositorySync(
	request SyncResolutionRequest,
	productVersion string,
	options syncResolutionOptions,
) (*SyncResolutionReport, error) {
	report := &SyncResolutionReport{
		Schema: schema.Resolve(schema.RepositorySyncReport), FormatVersion: SyncResolutionFormatVersion,
		Status: SyncRefused, Changed: []string{}, RolledBack: []string{},
		Verification: []Check{}, Path: request.Path, Strategy: request.Strategy,
	}
	if err := ValidateSyncPlan(request.Plan); err != nil {
		report.NextAction = err.Error()
		return report, err
	}
	report.RepoRoot = request.Plan.RepoRoot
	report.PlanDigest = request.Plan.PlanDigest
	if request.ExactDigest != request.Plan.PlanDigest {
		err := fmt.Errorf("repository sync resolution digest mismatch")
		report.NextAction = err.Error()
		return report, err
	}
	if productVersion != request.Plan.TargetProductVersion {
		err := fmt.Errorf(
			"repository sync plan targets reconc %s, not the running %s",
			request.Plan.TargetProductVersion, productVersion,
		)
		report.NextAction = err.Error()
		return report, err
	}
	action, ok := syncActionByPath(request.Plan.Actions, request.Path)
	if !ok {
		err := fmt.Errorf("repository sync plan has no action for %s", request.Path)
		report.NextAction = err.Error()
		return report, err
	}
	if mutableSyncState(action.State) || action.State == SyncUnchanged {
		err := fmt.Errorf("repository sync action %s does not require explicit resolution", action.Path)
		report.NextAction = err.Error()
		return report, err
	}
	var resolveErr error
	lockErr := withRepositoryTransactionLock(request.Plan.RepoRoot, func() error {
		resolveErr = resolveRepositorySyncLocked(request, action, productVersion, options, report)
		return resolveErr
	})
	if lockErr != nil {
		if report.NextAction == "" {
			report.NextAction = lockErr.Error()
		}
		return report, lockErr
	}
	return report, nil
}

func resolveRepositorySyncLocked(
	request SyncResolutionRequest,
	action SyncAction,
	productVersion string,
	options syncResolutionOptions,
	report *SyncResolutionReport,
) error {
	root := request.Plan.RepoRoot
	if err := ensureNoPendingRepositorySync(root); err != nil {
		return err
	}
	currentPlan, err := BuildSyncPlan(root, productVersion)
	if err != nil {
		return err
	}
	if currentPlan.PlanDigest != request.Plan.PlanDigest {
		return fmt.Errorf("repository sync plan is stale; rebuild it and review the new digest")
	}
	currentReceipt, _, err := loadRepositoryOwnership(root)
	if err != nil {
		return err
	}
	targetReceipt := copyRepositoryReceipt(currentReceipt)
	mutations := []syncMutation{}
	switch request.Strategy {
	case SyncKeepCurrent:
		if request.Binary != nil {
			return fmt.Errorf("--binary inputs are valid only with --strategy use-binary")
		}
		if action.Component == "policy-lock" {
			if verifyErr := reconruntime.ValidatePolicyLockfile(root); verifyErr != nil {
				return fmt.Errorf("cannot keep an invalid generated policy lock; use --strategy use-target")
			}
		}
		releaseRepositoryOwnership(targetReceipt, action)
	case SyncUseTarget:
		if request.Binary != nil {
			return fmt.Errorf("--binary inputs are valid only with --strategy use-binary")
		}
		desired, desiredErr := resolvePlannedTargetBytes(root, currentReceipt, action, productVersion)
		if desiredErr != nil {
			return desiredErr
		}
		mutation, mutationErr := resolutionMutation(root, action.Path, action.Mode, desired)
		if mutationErr != nil {
			return mutationErr
		}
		mutations = append(mutations, mutation)
		if err := assignRepositoryOwnership(targetReceipt, action, desired, productVersion, false); err != nil {
			return err
		}
	case SyncUseBinary:
		if action.Component != "binary" || request.Binary == nil {
			return fmt.Errorf("--strategy use-binary requires a binary action and checksum-pinned binary inputs")
		}
		binary, err := BinarySelectionFor(
			request.Binary.SourcePath, request.Binary.SHA256,
			request.Binary.OS, request.Binary.Arch,
		)
		if err != nil {
			return fmt.Errorf("validate checksum-pinned repository binary: %w", err)
		}
		name, err := StableBinaryName(binary.OS, binary.Arch)
		if err != nil {
			return err
		}
		expectedPath := filepath.ToSlash(filepath.Join("tools", "reconc", "dist", name))
		if action.Path != expectedPath {
			return fmt.Errorf("binary platform %s/%s targets %s, not %s", binary.OS, binary.Arch, expectedPath, action.Path)
		}
		body, _, err := readRemovalFile(binary.SourcePath, maxBinaryBytes)
		if err != nil {
			return fmt.Errorf("read checksum-pinned repository binary: %w", err)
		}
		if bytesSHA256(body) != binary.SHA256 {
			return fmt.Errorf("checksum-pinned repository binary changed after validation")
		}
		mutation, err := resolutionMutation(root, action.Path, 0o755, body)
		if err != nil {
			return err
		}
		mutations = append(mutations, mutation)
		if err := assignRepositoryOwnership(targetReceipt, action, body, productVersion, true); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported repository sync resolution strategy %q", request.Strategy)
	}
	targetReceipt.ProductVersion = currentReceipt.ProductVersion
	targetReceipt.PlanDigest = request.Plan.PlanDigest
	targetReceipt.Generation = currentReceipt.Generation + 1
	normalizeRepositoryReceipt(targetReceipt)
	targetReceipt.ReceiptDigest = ""
	targetReceipt.ReceiptDigest, err = computeRepositoryReceiptDigest(targetReceipt)
	if err != nil {
		return err
	}
	if err := ValidateRepositoryReceipt(targetReceipt); err != nil {
		return err
	}
	receiptBody, err := encodeRepositoryReceipt(targetReceipt)
	if err != nil {
		return err
	}
	receiptTarget, err := safeRepositorySyncPath(root, RepositoryReceiptRelativePath)
	if err != nil {
		return err
	}
	_, lstatErr := os.Lstat(receiptTarget)
	receiptCreated := os.IsNotExist(lstatErr)
	if lstatErr != nil && !receiptCreated {
		return fmt.Errorf("inspect repository receipt before resolution: %w", lstatErr)
	}
	mutations = append(mutations, syncMutation{
		Path: RepositoryReceiptRelativePath, Mode: 0o644,
		After: receiptBody, Created: receiptCreated,
	})
	transaction, err := buildRepositorySyncTransaction(
		root, currentReceipt.ProductVersion, request.Plan.PlanDigest, mutations, false,
	)
	if err != nil {
		return err
	}
	if err := publishRepositorySyncTransaction(
		root, transaction, mutations, options.failAfter, options.interruptAfter,
	); err != nil {
		if errors.Is(err, errRepositorySyncInterrupted) {
			report.Status = SyncRefused
			report.NextAction = "reconc repo sync recover " + quoteBootstrapArgument(root)
			return err
		}
		rolledBack, rollbackErr := rollbackRepositorySyncTransaction(root, transaction)
		report.RolledBack = append(report.RolledBack, rolledBack...)
		report.Status = SyncRolledBack
		return errors.Join(err, rollbackErr)
	}
	for _, mutation := range mutations {
		report.Changed = append(report.Changed, mutation.Path)
	}
	verification, err := verifyRepository(root, "", true)
	if err != nil || !verification.Valid {
		if err == nil {
			err = fmt.Errorf("repository sync resolution verification failed: %s", verification.NextAction)
		}
		rolledBack, rollbackErr := rollbackRepositorySyncTransaction(root, transaction)
		report.RolledBack = append(report.RolledBack, rolledBack...)
		report.Status = SyncRolledBack
		return errors.Join(err, rollbackErr)
	}
	report.Verification = append(report.Verification, verification.Checks...)
	if err := removeRepositorySyncTransaction(root, transaction); err != nil {
		report.NextAction = "reconc repo sync recover " + quoteBootstrapArgument(root)
		return err
	}
	report.Status = SyncComplete
	sort.Strings(report.Changed)
	report.NextAction = "reconc repo sync plan " + quoteBootstrapArgument(root)
	return nil
}

func resolvePlannedTargetBytes(
	root string,
	receipt *RepositoryReceipt,
	action SyncAction,
	productVersion string,
) ([]byte, error) {
	if action.DesiredSHA256 == "" {
		return nil, fmt.Errorf("repository sync action %s has no materializable target; use keep-current or use-binary", action.Path)
	}
	selection, err := selectionFromRepositoryReceipt(receipt, productVersion)
	if err != nil {
		return nil, err
	}
	artifacts, err := buildDesiredArtifacts(root, selection, productVersion)
	if err != nil {
		return nil, err
	}
	var artifact desiredArtifact
	found := false
	for _, candidate := range artifacts {
		if candidate.path == action.Path {
			artifact = candidate
			found = true
			break
		}
	}
	body, err := desiredSyncBytes(root, action, artifact, found, productVersion)
	if err != nil {
		return nil, err
	}
	if bytesSHA256(body) != action.DesiredSHA256 {
		return nil, fmt.Errorf("repository sync target bytes drifted for %s", action.Path)
	}
	return body, nil
}

func resolutionMutation(root, relative string, mode uint32, after []byte) (syncMutation, error) {
	target, err := safeRepositorySyncPath(root, relative)
	if err != nil {
		return syncMutation{}, err
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return syncMutation{Path: relative, Mode: mode, After: after, Created: true}, nil
	}
	if err != nil {
		return syncMutation{}, fmt.Errorf("inspect repository sync resolution target %s: %w", relative, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return syncMutation{}, fmt.Errorf("repository sync resolution refuses to replace non-regular target %s; use keep-current", relative)
	}
	return syncMutation{Path: relative, Mode: mode, After: after}, nil
}

func releaseRepositoryOwnership(receipt *RepositoryReceipt, action SyncAction) {
	hookKind := ""
	switch {
	case strings.HasPrefix(action.Component, "hook:"):
		hookKind = strings.TrimPrefix(action.Component, "hook:")
	case strings.HasPrefix(action.Component, "hook-activation:"):
		hookKind = strings.TrimPrefix(action.Component, "hook-activation:")
	}
	match := func(component string) bool {
		if hookKind != "" {
			return component == "hook:"+hookKind || component == "hook-activation:"+hookKind
		}
		return component == action.Component
	}
	files := receipt.ManagedFiles[:0]
	for _, file := range receipt.ManagedFiles {
		if match(file.Component) || file.Path == action.Path {
			receipt.UserOwnedPaths = append(receipt.UserOwnedPaths, file.Path)
			continue
		}
		files = append(files, file)
	}
	receipt.ManagedFiles = files
	blocks := receipt.ManagedBlocks[:0]
	for _, block := range receipt.ManagedBlocks {
		if match(block.Component) || block.Path == action.Path {
			receipt.UserOwnedPaths = append(receipt.UserOwnedPaths, block.Path)
			continue
		}
		blocks = append(blocks, block)
	}
	receipt.ManagedBlocks = blocks
	generated := receipt.GeneratedArtifacts[:0]
	for _, artifact := range receipt.GeneratedArtifacts {
		if artifact.Path == action.Path {
			receipt.UserOwnedPaths = append(receipt.UserOwnedPaths, artifact.Path)
			continue
		}
		generated = append(generated, artifact)
	}
	receipt.GeneratedArtifacts = generated
	receipt.UserOwnedPaths = append(receipt.UserOwnedPaths, action.Path)
	if hookKind != "" {
		receipt.Hooks = removeString(receipt.Hooks, hookKind)
	}
	if strings.HasPrefix(action.Component, "harness-pack:") {
		name := strings.TrimPrefix(action.Component, "harness-pack:")
		if base, _, found := strings.Cut(name, "@"); found {
			name = base
		}
		harnessPacks := receipt.HarnessPacks[:0]
		for _, pack := range receipt.HarnessPacks {
			if pack.Name != name {
				harnessPacks = append(harnessPacks, pack)
			}
		}
		receipt.HarnessPacks = harnessPacks
	}
}

func assignRepositoryOwnership(
	receipt *RepositoryReceipt,
	action SyncAction,
	body []byte,
	productVersion string,
	binaryApproval bool,
) error {
	receipt.UserOwnedPaths = removeString(receipt.UserOwnedPaths, action.Path)
	receipt.ManagedFiles = removeManagedFile(receipt.ManagedFiles, action.Path)
	var existingBlock *ManagedBlock
	blocks := receipt.ManagedBlocks[:0]
	for index := range receipt.ManagedBlocks {
		block := receipt.ManagedBlocks[index]
		if block.Path == action.Path {
			copy := block
			existingBlock = &copy
			continue
		}
		blocks = append(blocks, block)
	}
	receipt.ManagedBlocks = blocks
	generated := receipt.GeneratedArtifacts[:0]
	for _, artifact := range receipt.GeneratedArtifacts {
		if artifact.Path != action.Path {
			generated = append(generated, artifact)
		}
	}
	receipt.GeneratedArtifacts = generated
	if action.Component == "policy-lock" {
		receipt.GeneratedArtifacts = append(receipt.GeneratedArtifacts, GeneratedArtifact{
			Path: action.Path, Generator: "reconc-policy-compiler",
			Version: productVersion, SHA256: bytesSHA256(body),
		})
		return nil
	}
	if existingBlock != nil {
		managed, err := extractManagedBlock(body, existingBlock.BlockStart, existingBlock.BlockEnd)
		if err != nil {
			return fmt.Errorf("capture resolved managed block %s: %w", action.Path, err)
		}
		receipt.ManagedBlocks = append(receipt.ManagedBlocks, ManagedBlock{
			Path: action.Path, BlockStart: existingBlock.BlockStart, BlockEnd: existingBlock.BlockEnd,
			ManagedSHA256: bytesSHA256(managed), WholeFileSHA256: bytesSHA256(body),
			Component: action.Component,
		})
		return nil
	}
	component := action.Component
	if binaryApproval {
		component = "binary@" + productVersion
	}
	receipt.ManagedFiles = append(receipt.ManagedFiles, ManagedFile{
		Path: action.Path, Mode: action.Mode, SHA256: bytesSHA256(body),
		Component: component, Ownership: "file",
	})
	return nil
}

func copyRepositoryReceipt(receipt *RepositoryReceipt) *RepositoryReceipt {
	copy := *receipt
	copy.PolicyPacks = append([]PolicyPackIdentity{}, receipt.PolicyPacks...)
	copy.HarnessPacks = append([]HarnessPackIdentity{}, receipt.HarnessPacks...)
	copy.Hooks = append([]string{}, receipt.Hooks...)
	copy.PolicySources = append([]string{}, receipt.PolicySources...)
	copy.ManagedFiles = append([]ManagedFile{}, receipt.ManagedFiles...)
	copy.ManagedBlocks = append([]ManagedBlock{}, receipt.ManagedBlocks...)
	copy.GeneratedArtifacts = append([]GeneratedArtifact{}, receipt.GeneratedArtifacts...)
	copy.UserOwnedPaths = append([]string{}, receipt.UserOwnedPaths...)
	return &copy
}

func removeManagedFile(files []ManagedFile, path string) []ManagedFile {
	result := files[:0]
	for _, file := range files {
		if file.Path != path {
			result = append(result, file)
		}
	}
	return result
}

func syncActionByPath(actions []SyncAction, path string) (SyncAction, bool) {
	index := sort.Search(len(actions), func(index int) bool { return actions[index].Path >= path })
	if index >= len(actions) || actions[index].Path != path {
		return SyncAction{}, false
	}
	return actions[index], true
}
