package bootstrap

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/schema"
)

const maxSyncPlanBytes = 8 << 20

func BuildSyncPlan(repoRoot, productVersion string) (*SyncPlan, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	if err := ensureNoPendingRepositorySync(root); err != nil {
		return nil, err
	}
	if strings.TrimSpace(productVersion) == "" {
		return nil, fmt.Errorf("repository sync target product version is required")
	}
	receipt, legacy, err := loadRepositoryOwnership(root)
	if err != nil {
		return nil, err
	}
	selection, err := selectionFromRepositoryReceipt(receipt, productVersion)
	if err != nil {
		return nil, err
	}
	artifacts, err := buildDesiredArtifacts(root, selection, productVersion)
	if err != nil {
		return nil, err
	}
	plan := &SyncPlan{
		Schema: schema.Resolve(schema.RepositorySyncPlan), FormatVersion: SyncPlanFormatVersion,
		RepoRoot: root, CurrentProductVersion: receipt.ProductVersion,
		TargetProductVersion: productVersion, CurrentReceiptDigest: receipt.ReceiptDigest,
		LegacyReceiptImport: legacy,
		CurrentPolicyPacks:  append([]PolicyPackIdentity{}, receipt.PolicyPacks...),
		CurrentHarnessPacks: append([]HarnessPackIdentity{}, receipt.HarnessPacks...),
		TargetPolicyPacks:   []PolicyPackIdentity{},
		TargetHarnessPacks:  []HarnessPackIdentity{}, Actions: []SyncAction{},
		Migrations: []SyncMigration{}, Candidates: []string{}, BlockingIssues: []string{},
	}
	plan.TargetPolicyPacks, err = policyPackIdentities(packNames(receipt.PolicyPacks))
	if err != nil {
		return nil, err
	}
	plan.TargetHarnessPacks, err = harnessPackIdentities(selection, productVersion)
	if err != nil {
		return nil, err
	}
	plan.GitSnapshot, err = captureReadOnlyGitSnapshot(root)
	if err != nil {
		return nil, err
	}

	managedFiles := make(map[string]ManagedFile, len(receipt.ManagedFiles))
	for _, file := range receipt.ManagedFiles {
		managedFiles[file.Path] = file
	}
	managedBlocks := make(map[string]ManagedBlock, len(receipt.ManagedBlocks))
	for _, block := range receipt.ManagedBlocks {
		managedBlocks[block.Path] = block
	}
	targetPaths := make(map[string]bool, len(artifacts))
	userOwnedPaths := make(map[string]bool, len(receipt.UserOwnedPaths))
	for _, path := range receipt.UserOwnedPaths {
		userOwnedPaths[path] = true
	}
	binary, binaryOS, binaryArch, err := repositoryBinaryOwnership(receipt)
	if err != nil {
		return nil, err
	}
	if binary != nil && (binaryOS != runtime.GOOS || binaryArch != runtime.GOARCH) {
		targetPaths[binary.Path] = true
		var action SyncAction
		var actionErr error
		if binaryApprovedForProduct(*binary, productVersion) {
			action, actionErr = planApprovedCrossPlatformBinary(root, *binary, productVersion)
		} else {
			action, actionErr = planCrossPlatformBinary(root, *binary, binaryOS, binaryArch)
		}
		if actionErr != nil {
			return nil, actionErr
		}
		plan.Actions = append(plan.Actions, action)
	}
	for _, artifact := range artifacts {
		if userOwnedPaths[artifact.path] {
			targetPaths[artifact.path] = true
			continue
		}
		if !syncOwnsComponent(artifact.component) && managedBlocks[artifact.path].Path == "" {
			continue
		}
		targetPaths[artifact.path] = true
		action, actionErr := planSyncArtifact(root, artifact, managedFiles[artifact.path], managedBlocks[artifact.path])
		if actionErr != nil {
			return nil, actionErr
		}
		plan.Actions = append(plan.Actions, action)
	}
	for _, file := range receipt.ManagedFiles {
		if targetPaths[file.Path] {
			continue
		}
		plan.Actions = append(plan.Actions, SyncAction{
			Component: file.Component, Path: file.Path, Mode: file.Mode,
			State: SyncOrphanedLegacy, ReceiptSHA256: file.SHA256,
			Reason: "receipt-owned artifact is not part of the target product; preserve it for explicit review",
		})
	}
	for _, block := range receipt.ManagedBlocks {
		if targetPaths[block.Path] {
			continue
		}
		plan.Actions = append(plan.Actions, SyncAction{
			Component: block.Component, Path: block.Path, Mode: 0o644,
			State: SyncOrphanedLegacy, ReceiptSHA256: block.ManagedSHA256,
			Reason: "receipt-owned managed block is not part of the target product; preserve it for explicit review",
		})
	}
	policyAction, migrations, err := planPolicyLockMigration(root, receipt, productVersion)
	if err != nil {
		return nil, err
	}
	if policyAction != nil {
		plan.Actions = append(plan.Actions, *policyAction)
	}
	plan.Migrations = append(plan.Migrations, migrations...)
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].Path < plan.Actions[j].Path })
	sort.Slice(plan.Migrations, func(i, j int) bool {
		left := plan.Migrations[i]
		right := plan.Migrations[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.From != right.From {
			return left.From < right.From
		}
		return left.To < right.To
	})
	for _, action := range plan.Actions {
		if !mutableSyncState(action.State) && action.State != SyncUnchanged {
			plan.BlockingIssues = append(plan.BlockingIssues, action.Path+": "+action.Reason)
			if action.CandidatePath != "" {
				plan.Candidates = append(plan.Candidates, action.CandidatePath)
			}
		}
	}
	sort.Strings(plan.BlockingIssues)
	sort.Strings(plan.Candidates)
	digest, err := computeSyncPlanDigest(plan)
	if err != nil {
		return nil, err
	}
	plan.PlanDigest = digest
	if err := ValidateSyncPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func WriteSyncPlan(outputPath string, plan *SyncPlan) (string, error) {
	body, err := encodeSyncPlan(plan)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(outputPath)
	if err != nil {
		return "", fmt.Errorf("resolve repository sync plan output: %w", err)
	}
	info, inspectErr := os.Lstat(absolute)
	if inspectErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("repository sync plan output is not a real regular file: %s", absolute)
		}
		current, readErr := boundedio.ReadRegularFile(absolute, maxSyncPlanBytes)
		if readErr != nil {
			return "", fmt.Errorf("read repository sync plan output: %w", readErr)
		}
		if bytes.Equal(current, body) {
			return "unchanged", nil
		}
		return "", fmt.Errorf("repository sync plan output already exists with different content: %s", absolute)
	}
	if !os.IsNotExist(inspectErr) {
		return "", fmt.Errorf("inspect repository sync plan output: %w", inspectErr)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return "", fmt.Errorf("create repository sync plan parent: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("create repository sync plan output: %w", err)
	}
	if _, err := file.Write(body); err != nil {
		closeErr := file.Close()
		removeErr := os.Remove(absolute)
		return "", combineWriteFailure("write repository sync plan output", err, closeErr, removeErr)
	}
	if err := file.Sync(); err != nil {
		closeErr := file.Close()
		removeErr := os.Remove(absolute)
		return "", combineWriteFailure("sync repository sync plan output", err, closeErr, removeErr)
	}
	if err := file.Close(); err != nil {
		removeErr := os.Remove(absolute)
		return "", combineWriteFailure("close repository sync plan output", err, nil, removeErr)
	}
	return "created", nil
}

func ReplaceSyncPlan(outputPath string, plan *SyncPlan) (string, error) {
	body, err := encodeSyncPlan(plan)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(outputPath)
	if err != nil {
		return "", fmt.Errorf("resolve repository sync plan output: %w", err)
	}
	info, err := os.Lstat(absolute)
	if os.IsNotExist(err) {
		return WriteSyncPlan(absolute, plan)
	}
	if err != nil {
		return "", fmt.Errorf("inspect repository sync plan output: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("repository sync plan output is not a real regular file: %s", absolute)
	}
	current, err := LoadSyncPlan(absolute)
	if err != nil {
		return "", fmt.Errorf("refuse to replace non-sync-plan output %s: %w", absolute, err)
	}
	if current.RepoRoot != plan.RepoRoot {
		return "", fmt.Errorf("refuse to replace sync plan for %s with a plan for %s", current.RepoRoot, plan.RepoRoot)
	}
	changed, err := atomicfile.WriteIfChanged(absolute, body, 0o644)
	if err != nil {
		return "", fmt.Errorf("replace repository sync plan output: %w", err)
	}
	if !changed {
		return "unchanged", nil
	}
	return "replaced", nil
}

func LoadSyncPlan(planPath string) (*SyncPlan, error) {
	linkInfo, err := os.Lstat(planPath)
	if err != nil {
		return nil, fmt.Errorf("inspect repository sync plan: %w", err)
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("repository sync plan must be a real regular file")
	}
	file, err := os.Open(planPath)
	if err != nil {
		return nil, fmt.Errorf("open repository sync plan: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		closeErr := file.Close()
		return nil, combineWriteFailure("stat repository sync plan", err, closeErr, nil)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxSyncPlanBytes {
		_ = file.Close()
		return nil, fmt.Errorf("repository sync plan must be a bounded regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxSyncPlanBytes+1))
	decoder.DisallowUnknownFields()
	var plan SyncPlan
	decodeErr := decoder.Decode(&plan)
	var extra interface{}
	extraErr := decoder.Decode(&extra)
	closeErr := file.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("decode repository sync plan: %w", decodeErr)
	}
	if extraErr != io.EOF {
		return nil, fmt.Errorf("repository sync plan must contain exactly one JSON document")
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close repository sync plan: %w", closeErr)
	}
	if err := ValidateSyncPlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func ValidateSyncPlan(plan *SyncPlan) error {
	if plan == nil || !schema.AcceptsFormat(
		schema.RepositorySyncPlan, plan.Schema, plan.FormatVersion,
	) {
		return fmt.Errorf("unsupported repository sync plan schema or format")
	}
	root, err := canonicalRepoRoot(plan.RepoRoot)
	if err != nil {
		return err
	}
	if root != plan.RepoRoot || plan.CurrentProductVersion == "" || plan.TargetProductVersion == "" ||
		!validSHA256(plan.CurrentReceiptDigest) || !validSHA256(plan.PlanDigest) {
		return fmt.Errorf("repository sync plan identity is invalid")
	}
	if plan.CurrentPolicyPacks == nil || plan.CurrentHarnessPacks == nil ||
		plan.TargetPolicyPacks == nil || plan.TargetHarnessPacks == nil ||
		plan.Actions == nil || plan.Migrations == nil || plan.Candidates == nil ||
		plan.BlockingIssues == nil {
		return fmt.Errorf("repository sync plan collections must be arrays")
	}
	if plan.GitSnapshot != nil {
		if plan.GitSnapshot.RepoRoot != plan.RepoRoot {
			return fmt.Errorf("repository sync plan Git snapshot belongs to a different repository")
		}
		if (plan.GitSnapshot.Head != "UNBORN" && !validGitObjectID(plan.GitSnapshot.Head)) ||
			!validGitObjectID(plan.GitSnapshot.IndexTree) {
			return fmt.Errorf("repository sync plan Git snapshot identity is invalid")
		}
	}
	if err := validatePolicyPackIdentities(plan.TargetPolicyPacks); err != nil {
		return err
	}
	if err := validateHarnessPackIdentities(plan.TargetHarnessPacks); err != nil {
		return err
	}
	if err := validatePolicyPackIdentities(plan.CurrentPolicyPacks); err != nil {
		return err
	}
	if err := validateHarnessPackIdentities(plan.CurrentHarnessPacks); err != nil {
		return err
	}
	for index, action := range plan.Actions {
		if !validRepositoryRelativePath(action.Path) || action.Component == "" ||
			(action.Mode != 0o644 && action.Mode != 0o755) || !validSyncState(action.State) ||
			strings.TrimSpace(action.Reason) == "" || strings.TrimSpace(action.Reason) != action.Reason ||
			action.CurrentMode > 0o777 {
			return fmt.Errorf("repository sync action %d is invalid", index)
		}
		if index > 0 && plan.Actions[index-1].Path >= action.Path {
			return fmt.Errorf("repository sync actions must be uniquely sorted")
		}
		for _, digest := range []string{action.CurrentSHA256, action.ReceiptSHA256, action.DesiredSHA256} {
			if digest != "" && !validSHA256(digest) {
				return fmt.Errorf("repository sync action %s contains an invalid digest", action.Path)
			}
		}
		if action.CandidatePath != "" && !validRepositoryRelativePath(action.CandidatePath) {
			return fmt.Errorf("repository sync action %s has an invalid candidate path", action.Path)
		}
		if err := validateSyncActionContract(action); err != nil {
			return err
		}
	}
	for index, migration := range plan.Migrations {
		if strings.TrimSpace(migration.Kind) == "" || strings.TrimSpace(migration.From) == "" ||
			strings.TrimSpace(migration.To) == "" || !validRepositoryRelativePath(migration.Path) {
			return fmt.Errorf("repository sync migration %d is invalid", index)
		}
		if index > 0 && !syncMigrationLess(plan.Migrations[index-1], migration) {
			return fmt.Errorf("repository sync migrations must be uniquely sorted")
		}
		if !syncActionPathExists(plan.Actions, migration.Path) {
			return fmt.Errorf("repository sync migration references unknown action %s", migration.Path)
		}
	}
	if err := validateSortedStrings(plan.Candidates, "sync candidate"); err != nil {
		return err
	}
	if err := validateSortedStrings(plan.BlockingIssues, "sync blocking issue"); err != nil {
		return err
	}
	expectedCandidates := []string{}
	expectedIssues := []string{}
	for _, action := range plan.Actions {
		if action.CandidatePath != "" {
			expectedCandidates = append(expectedCandidates, action.CandidatePath)
		}
		if !mutableSyncState(action.State) && action.State != SyncUnchanged {
			expectedIssues = append(expectedIssues, action.Path+": "+action.Reason)
		}
	}
	sort.Strings(expectedCandidates)
	sort.Strings(expectedIssues)
	if !equalStrings(plan.Candidates, expectedCandidates) {
		return fmt.Errorf("repository sync candidates do not match action candidates")
	}
	if !equalStrings(plan.BlockingIssues, expectedIssues) {
		return fmt.Errorf("repository sync blocking issues do not match non-mutable actions")
	}
	digest, err := computeSyncPlanDigest(plan)
	if err != nil {
		return err
	}
	if plan.PlanDigest != digest {
		return fmt.Errorf("repository sync plan digest mismatch")
	}
	return nil
}

func validateSyncActionContract(action SyncAction) error {
	require := func(value, field string) error {
		if value == "" {
			return fmt.Errorf("repository sync action %s requires %s", action.Path, field)
		}
		return nil
	}
	forbidCandidate := func() error {
		if action.CandidatePath != "" {
			return fmt.Errorf("repository sync action %s must not contain a candidate", action.Path)
		}
		return nil
	}
	switch action.State {
	case SyncUnchanged:
		if err := require(action.DesiredSHA256, "desired_sha256"); err != nil {
			return err
		}
		return forbidCandidate()
	case SyncReplaceOwned, SyncUpdateManagedBlock:
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "current_sha256", value: action.CurrentSHA256},
			{name: "receipt_sha256", value: action.ReceiptSHA256},
			{name: "desired_sha256", value: action.DesiredSHA256},
		} {
			if err := require(field.value, field.name); err != nil {
				return err
			}
		}
		return forbidCandidate()
	case SyncCreateOwned:
		if action.CurrentSHA256 != "" {
			return fmt.Errorf("repository sync create action %s must not contain current_sha256", action.Path)
		}
		if err := require(action.DesiredSHA256, "desired_sha256"); err != nil {
			return err
		}
		return forbidCandidate()
	case SyncUserDrift, SyncManualReview:
		if err := require(action.CandidatePath, "candidate_path"); err != nil {
			return err
		}
		if action.CandidatePath != syncCandidatePath(action) {
			return fmt.Errorf("repository sync action %s candidate identity is invalid", action.Path)
		}
	case SyncOrphanedLegacy:
		if err := require(action.ReceiptSHA256, "receipt_sha256"); err != nil {
			return err
		}
		if action.DesiredSHA256 != "" {
			return fmt.Errorf("repository sync orphan action %s must not contain desired_sha256", action.Path)
		}
		return forbidCandidate()
	case SyncIncompatible:
		return forbidCandidate()
	}
	return nil
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	if value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func syncMigrationLess(left, right SyncMigration) bool {
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	if left.From != right.From {
		return left.From < right.From
	}
	return left.To < right.To
}

func syncActionPathExists(actions []SyncAction, path string) bool {
	index := sort.Search(len(actions), func(index int) bool { return actions[index].Path >= path })
	return index < len(actions) && actions[index].Path == path
}

func equalStrings(left, right []string) bool {
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

func encodeSyncPlan(plan *SyncPlan) ([]byte, error) {
	if err := ValidateSyncPlan(plan); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode repository sync plan: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxSyncPlanBytes {
		return nil, fmt.Errorf("repository sync plan exceeds %d bytes", maxSyncPlanBytes)
	}
	return body, nil
}

func computeSyncPlanDigest(plan *SyncPlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("repository sync plan is nil")
	}
	copy := *plan
	copy.PlanDigest = ""
	body, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("encode repository sync plan digest: %w", err)
	}
	return bytesSHA256(body), nil
}

func loadRepositoryOwnership(root string) (*RepositoryReceipt, bool, error) {
	receipt, err := LoadRepositoryReceipt(root)
	if err == nil {
		return receipt, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	legacyPlan, err := recordedInitPlan(root)
	if err != nil {
		return nil, false, err
	}
	if legacyPlan == nil {
		return nil, false, fmt.Errorf("repository has no portable receipt or exactly one valid legacy bootstrap receipt; run reconc init explicitly")
	}
	privateReceipt, _, err := loadInstallReceipt(legacyPlan)
	if err != nil {
		return nil, false, err
	}
	receipt, err = BuildRepositoryReceipt(legacyPlan, privateReceipt, 1, legacyPlan.PlanDigest)
	if err != nil {
		return nil, false, fmt.Errorf("import legacy bootstrap ownership: %w", err)
	}
	return receipt, true, nil
}

func selectionFromRepositoryReceipt(receipt *RepositoryReceipt, productVersion string) (Selection, error) {
	selection := Selection{
		Profile: receipt.Profile, Packs: packNames(receipt.PolicyPacks),
		Hooks: append([]string{}, receipt.Hooks...),
	}
	binary, targetOS, targetArch, err := repositoryBinaryOwnership(receipt)
	if err != nil {
		return Selection{}, err
	}
	if binary != nil && targetOS == runtime.GOOS && targetArch == runtime.GOARCH {
		selection.Binary, err = CurrentBinarySelection()
		if err != nil {
			return Selection{}, fmt.Errorf("select running binary for repository sync: %w", err)
		}
	}
	if len(receipt.HarnessPacks) == 0 {
		return selection, nil
	}
	return attachHarnessPacks(selection, productVersion)
}

func packNames(identities []PolicyPackIdentity) []string {
	names := make([]string, len(identities))
	for index, identity := range identities {
		names[index] = identity.Name
	}
	return names
}

func planSyncArtifact(root string, artifact desiredArtifact, owned ManagedFile, block ManagedBlock) (SyncAction, error) {
	desiredSHA, err := artifactSHA256(artifact)
	if err != nil {
		return SyncAction{}, err
	}
	action := SyncAction{
		Component: artifact.component, Path: artifact.path, Mode: artifact.mode,
		DesiredSHA256: desiredSHA,
	}
	target, err := safeBootstrapTarget(root, artifact.path)
	if err != nil {
		return SyncAction{}, err
	}
	info, lstatErr := os.Lstat(target)
	if os.IsNotExist(lstatErr) {
		if owned.Path != "" || block.Path != "" || syncOwnsComponent(artifact.component) {
			action.State = SyncCreateOwned
			action.Reason = "receipt-owned product artifact is absent and can be created from the embedded target pack"
			return action, nil
		}
	}
	if lstatErr != nil {
		return SyncAction{}, fmt.Errorf("inspect repository sync target %s: %w", artifact.path, lstatErr)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		action.State = SyncUserDrift
		action.Reason = "target is not a real regular file"
		action.CandidatePath = syncCandidatePath(action)
		return action, nil
	}
	current, err := readRepositoryRegularFile(root, artifact.path)
	if err != nil {
		return SyncAction{}, err
	}
	action.CurrentSHA256 = bytesSHA256(current)
	action.CurrentMode = uint32(info.Mode().Perm())
	if block.Path != "" {
		action.ReceiptSHA256 = block.ManagedSHA256
		currentBlock, blockErr := extractManagedBlock(current, block.BlockStart, block.BlockEnd)
		if blockErr != nil {
			action.State = SyncManualReview
			action.Reason = "managed block markers are missing, duplicated, or malformed"
			action.CandidatePath = syncCandidatePath(action)
			return action, nil
		}
		if bytesSHA256(currentBlock) != block.ManagedSHA256 {
			action.State = SyncUserDrift
			action.Reason = "managed block bytes differ from the portable receipt"
			action.CandidatePath = syncCandidatePath(action)
			return action, nil
		}
		if action.CurrentSHA256 == desiredSHA && modeSatisfies(info.Mode(), artifact.mode) {
			action.State = SyncUnchanged
			action.Reason = "managed block and preserved outside bytes already match the target product"
			return action, nil
		}
		action.State = SyncUpdateManagedBlock
		action.Reason = "only the receipt-owned marker-delimited block will be replaced"
		return action, nil
	}
	if owned.Path == "" {
		action.State = SyncManualReview
		action.Reason = "target path exists without portable ownership evidence"
		action.CandidatePath = syncCandidatePath(action)
		return action, nil
	}
	action.ReceiptSHA256 = owned.SHA256
	if action.CurrentSHA256 != owned.SHA256 || !modeSatisfies(info.Mode(), owned.Mode) {
		action.State = SyncUserDrift
		action.Reason = "receipt-owned file changed since the recorded transaction"
		action.CandidatePath = syncCandidatePath(action)
		return action, nil
	}
	if action.CurrentSHA256 == desiredSHA && modeSatisfies(info.Mode(), artifact.mode) {
		action.State = SyncUnchanged
		action.Reason = "receipt-owned file already matches the target product"
		return action, nil
	}
	action.State = SyncReplaceOwned
	action.Reason = "receipt-owned bytes match their precondition and can be atomically replaced"
	return action, nil
}

func repositoryBinaryOwnership(receipt *RepositoryReceipt) (*ManagedFile, string, string, error) {
	var owned *ManagedFile
	for index := range receipt.ManagedFiles {
		file := &receipt.ManagedFiles[index]
		if file.Component != "binary" && !strings.HasPrefix(file.Component, "binary@") {
			continue
		}
		if owned != nil {
			return nil, "", "", fmt.Errorf("repository receipt owns more than one Reconc binary")
		}
		copy := *file
		owned = &copy
	}
	if owned == nil {
		return nil, "", "", nil
	}
	for _, platform := range []struct {
		os   string
		arch string
	}{
		{os: "darwin", arch: "amd64"},
		{os: "darwin", arch: "arm64"},
		{os: "linux", arch: "amd64"},
		{os: "linux", arch: "arm64"},
		{os: "windows", arch: "amd64"},
	} {
		name, err := StableBinaryName(platform.os, platform.arch)
		if err != nil {
			return nil, "", "", err
		}
		if owned.Path == filepath.ToSlash(filepath.Join("tools", "reconc", "dist", name)) {
			return owned, platform.os, platform.arch, nil
		}
	}
	return nil, "", "", fmt.Errorf("repository receipt binary path is not a supported stable artifact: %s", owned.Path)
}

func binaryApprovedForProduct(file ManagedFile, productVersion string) bool {
	return file.Component == "binary@"+productVersion
}

func planApprovedCrossPlatformBinary(root string, owned ManagedFile, productVersion string) (SyncAction, error) {
	action := SyncAction{
		Component: "binary", Path: owned.Path, Mode: owned.Mode,
		ReceiptSHA256: owned.SHA256, DesiredSHA256: owned.SHA256,
		Reason: "checksum-pinned cross-platform binary was explicitly approved for reconc " + productVersion,
	}
	target, err := safeBootstrapTarget(root, owned.Path)
	if err != nil {
		return SyncAction{}, err
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		action.State = SyncIncompatible
		action.DesiredSHA256 = ""
		action.Reason = "approved cross-platform binary is missing; repeat the checksum-pinned use-binary resolution"
		return action, nil
	}
	if err != nil {
		return SyncAction{}, fmt.Errorf("inspect approved cross-platform binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		action.State = SyncIncompatible
		action.DesiredSHA256 = ""
		action.Reason = "approved cross-platform binary is not a real regular file; repeat the checksum-pinned use-binary resolution"
		return action, nil
	}
	body, err := readRepositoryRegularFile(root, owned.Path)
	if err != nil {
		return SyncAction{}, err
	}
	action.CurrentSHA256 = bytesSHA256(body)
	action.CurrentMode = uint32(info.Mode().Perm())
	if action.CurrentSHA256 != owned.SHA256 || !modeSatisfies(info.Mode(), owned.Mode) {
		action.State = SyncIncompatible
		action.DesiredSHA256 = ""
		action.Reason = "approved cross-platform binary has drifted; repeat the checksum-pinned use-binary resolution"
		return action, nil
	}
	action.State = SyncUnchanged
	return action, nil
}

func planCrossPlatformBinary(root string, owned ManagedFile, targetOS, targetArch string) (SyncAction, error) {
	action := SyncAction{
		Component: "binary", Path: owned.Path, Mode: owned.Mode,
		State: SyncIncompatible, ReceiptSHA256: owned.SHA256,
		Reason: fmt.Sprintf(
			"running on %s/%s cannot supply the receipt-owned %s/%s binary; use a checksum-pinned artifact with `reconc repo sync resolve --plan PLAN --digest DIGEST --path %s --strategy use-binary --binary PATH --checksum SHA256 --platform %s/%s`",
			runtime.GOOS, runtime.GOARCH, targetOS, targetArch, quoteBootstrapArgument(owned.Path), targetOS, targetArch,
		),
	}
	target, err := safeBootstrapTarget(root, owned.Path)
	if err != nil {
		return SyncAction{}, err
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		action.Reason = "receipt-owned cross-platform binary is missing; " + action.Reason
		return action, nil
	}
	if err != nil {
		return SyncAction{}, fmt.Errorf("inspect receipt-owned cross-platform binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		action.Reason = "receipt-owned cross-platform binary is not a real regular file; " + action.Reason
		return action, nil
	}
	body, err := readRepositoryRegularFile(root, owned.Path)
	if err != nil {
		return SyncAction{}, err
	}
	action.CurrentSHA256 = bytesSHA256(body)
	action.CurrentMode = uint32(info.Mode().Perm())
	if action.CurrentSHA256 != owned.SHA256 || !modeSatisfies(info.Mode(), owned.Mode) {
		action.Reason = "receipt-owned cross-platform binary has drifted; " + action.Reason
	}
	return action, nil
}

func planPolicyLockMigration(root string, receipt *RepositoryReceipt, productVersion string) (*SyncAction, []SyncMigration, error) {
	var generated GeneratedArtifact
	for _, artifact := range receipt.GeneratedArtifacts {
		if artifact.Path == policyLockfilePath {
			generated = artifact
			break
		}
	}
	if generated.Path == "" {
		return nil, []SyncMigration{}, nil
	}
	action := &SyncAction{
		Component: "policy-lock", Path: generated.Path, Mode: 0o644,
		ReceiptSHA256: generated.SHA256,
	}
	_, desired, err := compiler.RenderRepoPolicy(root, productVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("compile receipt-owned generated policy lock in memory: %w", err)
	}
	action.DesiredSHA256 = bytesSHA256(desired)
	target, err := safeBootstrapTarget(root, generated.Path)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		action.State = SyncCreateOwned
		action.Reason = "receipt-owned generated policy lock is missing and can be rebuilt from the current registered policy sources"
		return action, []SyncMigration{}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect generated policy lock: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		action.State = SyncUserDrift
		action.Reason = "receipt-owned generated policy lock is not a real regular file"
		action.CandidatePath = syncCandidatePath(*action)
		return action, []SyncMigration{}, nil
	}
	body, err := readRepositoryRegularFile(root, generated.Path)
	if err != nil {
		return nil, nil, err
	}
	action.CurrentSHA256 = bytesSHA256(body)
	if action.CurrentSHA256 != generated.SHA256 {
		action.State = SyncUserDrift
		action.Reason = "generated policy lock differs from the portable receipt"
		action.CandidatePath = syncCandidatePath(*action)
		return action, []SyncMigration{}, nil
	}
	if action.CurrentSHA256 == action.DesiredSHA256 && modeSatisfies(info.Mode(), 0o644) {
		action.State = SyncUnchanged
		action.Reason = "generated policy lock matches the current registered policy sources and compiler"
		return action, []SyncMigration{}, nil
	}
	planned := []SyncMigration{}
	if migratedBody, migrations, migrationErr := migratePolicyLockBytes(body); migrationErr == nil &&
		bytes.Equal(migratedBody, desired) {
		planned = make([]SyncMigration, len(migrations))
		for index, migration := range migrations {
			planned[index] = SyncMigration{
				Kind: "policy-lock", From: migration.FromVersion,
				To: migration.ToVersion, Path: generated.Path,
			}
		}
	}
	action.State = SyncReplaceOwned
	action.Reason = "receipt-owned generated policy can be deterministically rebuilt from the current registered sources"
	return action, planned, nil
}

func migratePolicyLockBytes(body []byte) ([]byte, []compiler.Migration, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]interface{}
	if err := decoder.Decode(&payload); err != nil {
		return nil, nil, fmt.Errorf("decode generated policy lock for migration: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, nil, fmt.Errorf("generated policy lock must contain exactly one JSON document")
	}
	migrated, migrations, err := compiler.MigrateLockfile(payload)
	if err != nil {
		return nil, migrations, err
	}
	encoded, err := json.MarshalIndent(migrated, "", "  ")
	if err != nil {
		return nil, migrations, fmt.Errorf("encode migrated policy lock: %w", err)
	}
	return append(encoded, '\n'), migrations, nil
}

func syncCandidatePath(action SyncAction) string {
	digest := action.DesiredSHA256
	if digest == "" {
		digest = action.ReceiptSHA256
	}
	if len(digest) < 12 {
		digest = strings.Repeat("0", 12)
	}
	return action.Path + ".reconc-sync-candidate-" + digest[:12]
}

func mutableSyncState(state SyncActionState) bool {
	return state == SyncReplaceOwned || state == SyncUpdateManagedBlock || state == SyncCreateOwned
}

func validSyncState(state SyncActionState) bool {
	switch state {
	case SyncUnchanged, SyncReplaceOwned, SyncUpdateManagedBlock, SyncCreateOwned,
		SyncUserDrift, SyncOrphanedLegacy, SyncIncompatible, SyncManualReview:
		return true
	default:
		return false
	}
}
