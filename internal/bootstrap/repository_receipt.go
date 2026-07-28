package bootstrap

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/harness"
	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/schema"
)

const maxRepositoryReceiptBytes = 4 << 20

func BuildRepositoryReceipt(plan *Plan, privateReceipt *InstallReceipt, generation uint64, appliedPlanDigest string) (*RepositoryReceipt, error) {
	if err := ValidatePlan(plan); err != nil {
		return nil, err
	}
	if privateReceipt == nil {
		return nil, fmt.Errorf("repository receipt requires exact private transaction ownership evidence")
	}
	if err := validateInstallReceipt(plan, privateReceipt); err != nil {
		return nil, err
	}
	if generation == 0 || !validSHA256(appliedPlanDigest) {
		return nil, fmt.Errorf("repository receipt generation or plan digest is invalid")
	}
	privateByPath := make(map[string]InstallReceiptEntry, len(privateReceipt.Entries))
	for _, entry := range privateReceipt.Entries {
		privateByPath[entry.Path] = entry
	}
	receipt := &RepositoryReceipt{
		Schema: schema.Resolve(schema.RepositoryInstall), FormatVersion: RepositoryReceiptFormatVersion,
		ProductVersion: plan.ProductVersion, Profile: plan.Selection.Profile,
		PolicyPacks: []PolicyPackIdentity{}, HarnessPacks: []HarnessPackIdentity{},
		Hooks: append([]string{}, plan.Selection.Hooks...), PolicySources: []string{},
		ManagedFiles: []ManagedFile{}, ManagedBlocks: []ManagedBlock{},
		GeneratedArtifacts: []GeneratedArtifact{}, UserOwnedPaths: []string{},
		PlanDigest: appliedPlanDigest, Generation: generation,
	}
	policyPacks, err := policyPackIdentities(plan.Selection.Packs)
	if err != nil {
		return nil, err
	}
	receipt.PolicyPacks = policyPacks
	harnessPacks, err := harnessPackIdentitiesForPlan(plan)
	if err != nil {
		return nil, err
	}
	receipt.HarnessPacks = harnessPacks
	profile, err := profileByName(plan.Selection.Profile)
	if err != nil {
		return nil, err
	}
	if profile.Policy {
		receipt.PolicySources = []string{".reconc.yml"}
	}
	for _, action := range plan.Actions {
		entry, owned := privateByPath[action.Path]
		start, end := managedMarkersForComponent(action.Component, action.Path)
		if owned && start != "" && entry.BlockStart == start && entry.BlockEnd == end {
			body, readErr := readRepositoryRegularFile(plan.RepoRoot, action.Path)
			if readErr != nil {
				return nil, readErr
			}
			block, blockErr := extractManagedBlock(body, start, end)
			if blockErr != nil {
				return nil, fmt.Errorf("capture managed block %s: %w", action.Path, blockErr)
			}
			receipt.ManagedBlocks = append(receipt.ManagedBlocks, ManagedBlock{
				Path: action.Path, BlockStart: start, BlockEnd: end,
				ManagedSHA256: bytesSHA256(block), WholeFileSHA256: bytesSHA256(body),
				Component: action.Component,
			})
			continue
		}
		if owned && entry.Ownership == "file" && syncOwnsComponent(action.Component) {
			body, readErr := readRepositoryRegularFile(plan.RepoRoot, action.Path)
			if readErr != nil {
				return nil, readErr
			}
			receipt.ManagedFiles = append(receipt.ManagedFiles, ManagedFile{
				Path: action.Path, Mode: action.Mode, SHA256: bytesSHA256(body),
				Component: action.Component, Ownership: "file",
			})
			continue
		}
		receipt.UserOwnedPaths = append(receipt.UserOwnedPaths, action.Path)
	}
	if entry, ok := privateByPath[".reconc/policy.lock.json"]; ok && entry.Ownership == "file" {
		body, readErr := readRepositoryRegularFile(plan.RepoRoot, ".reconc/policy.lock.json")
		if readErr != nil {
			return nil, readErr
		}
		receipt.GeneratedArtifacts = append(receipt.GeneratedArtifacts, GeneratedArtifact{
			Path: ".reconc/policy.lock.json", Generator: "reconc-policy-compiler",
			Version: plan.ProductVersion, SHA256: bytesSHA256(body),
		})
	}
	normalizeRepositoryReceipt(receipt)
	digest, err := computeRepositoryReceiptDigest(receipt)
	if err != nil {
		return nil, err
	}
	receipt.ReceiptDigest = digest
	if err := ValidateRepositoryReceipt(receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

func LoadRepositoryReceipt(repoRoot string) (*RepositoryReceipt, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	receiptPath := filepath.Join(root, filepath.FromSlash(RepositoryReceiptRelativePath))
	linkInfo, err := os.Lstat(receiptPath)
	if err != nil {
		return nil, fmt.Errorf("inspect repository receipt: %w", err)
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("repository receipt must be a real regular file")
	}
	file, err := os.Open(receiptPath)
	if err != nil {
		return nil, fmt.Errorf("open repository receipt: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		closeErr := file.Close()
		return nil, combineWriteFailure("stat repository receipt", err, closeErr, nil)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxRepositoryReceiptBytes {
		_ = file.Close()
		return nil, fmt.Errorf("repository receipt must be a bounded regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxRepositoryReceiptBytes+1))
	decoder.DisallowUnknownFields()
	var receipt RepositoryReceipt
	decodeErr := decoder.Decode(&receipt)
	var extra interface{}
	extraErr := decoder.Decode(&extra)
	closeErr := file.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("decode repository receipt: %w", decodeErr)
	}
	if extraErr != io.EOF {
		return nil, fmt.Errorf("repository receipt must contain exactly one JSON document")
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close repository receipt: %w", closeErr)
	}
	if err := ValidateRepositoryReceipt(&receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func ValidateRepositoryReceipt(receipt *RepositoryReceipt) error {
	if receipt == nil || !matchesSchema(receipt.Schema, schema.RepositoryInstallURL, schema.Resolve(schema.RepositoryInstall)) ||
		receipt.FormatVersion != RepositoryReceiptFormatVersion {
		return fmt.Errorf("unsupported repository receipt schema or format")
	}
	if strings.TrimSpace(receipt.ProductVersion) == "" {
		return fmt.Errorf("repository receipt product_version is required")
	}
	if _, err := profileByName(receipt.Profile); err != nil {
		return err
	}
	if receipt.Generation == 0 || !validSHA256(receipt.PlanDigest) || !validSHA256(receipt.ReceiptDigest) {
		return fmt.Errorf("repository receipt transaction identity is invalid")
	}
	if err := validatePolicyPackIdentities(receipt.PolicyPacks); err != nil {
		return err
	}
	if err := validateHarnessPackIdentities(receipt.HarnessPacks); err != nil {
		return err
	}
	if err := validateSortedStrings(receipt.Hooks, "hook"); err != nil {
		return err
	}
	if err := validateSortedPaths(receipt.PolicySources, "policy source"); err != nil {
		return err
	}
	claimed := map[string]string{}
	for index, file := range receipt.ManagedFiles {
		if !validRepositoryRelativePath(file.Path) || !validSHA256(file.SHA256) ||
			(file.Mode != 0o644 && file.Mode != 0o755) ||
			strings.TrimSpace(file.Component) == "" || file.Ownership != "file" {
			return fmt.Errorf("repository receipt managed file %d is invalid", index)
		}
		if index > 0 && receipt.ManagedFiles[index-1].Path >= file.Path {
			return fmt.Errorf("repository receipt managed files must be uniquely sorted")
		}
		claimed[file.Path] = "managed file"
	}
	for index, block := range receipt.ManagedBlocks {
		if !validRepositoryRelativePath(block.Path) || block.BlockStart == "" || block.BlockEnd == "" ||
			block.BlockStart == block.BlockEnd || !validSHA256(block.ManagedSHA256) ||
			!validSHA256(block.WholeFileSHA256) || strings.TrimSpace(block.Component) == "" {
			return fmt.Errorf("repository receipt managed block %d is invalid", index)
		}
		if index > 0 && receipt.ManagedBlocks[index-1].Path >= block.Path {
			return fmt.Errorf("repository receipt managed blocks must be uniquely sorted")
		}
		if previous := claimed[block.Path]; previous != "" {
			return fmt.Errorf("repository receipt path %s is claimed as %s and managed block", block.Path, previous)
		}
		claimed[block.Path] = "managed block"
	}
	for index, generated := range receipt.GeneratedArtifacts {
		if !validRepositoryRelativePath(generated.Path) || !validSHA256(generated.SHA256) ||
			generated.Generator == "" || generated.Version == "" {
			return fmt.Errorf("repository receipt generated artifact %d is invalid", index)
		}
		if index > 0 && receipt.GeneratedArtifacts[index-1].Path >= generated.Path {
			return fmt.Errorf("repository receipt generated artifacts must be uniquely sorted")
		}
		if previous := claimed[generated.Path]; previous != "" {
			return fmt.Errorf("repository receipt path %s is claimed as %s and generated artifact", generated.Path, previous)
		}
		claimed[generated.Path] = "generated artifact"
	}
	if err := validateSortedPaths(receipt.UserOwnedPaths, "user-owned path"); err != nil {
		return err
	}
	for _, userPath := range receipt.UserOwnedPaths {
		if previous := claimed[userPath]; previous != "" {
			return fmt.Errorf("repository receipt path %s is both %s and user-owned", userPath, previous)
		}
	}
	digest, err := computeRepositoryReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != digest {
		return fmt.Errorf("repository receipt digest mismatch")
	}
	return nil
}

func writeRepositoryReceiptCreate(plan *Plan, receipt *RepositoryReceipt) (createdRecord, []createdDirectory, error) {
	body, err := encodeRepositoryReceipt(receipt)
	if err != nil {
		return createdRecord{}, nil, err
	}
	artifact := desiredArtifact{
		component: "repository-receipt", path: RepositoryReceiptRelativePath,
		mode: 0o644, content: body,
	}
	return publishArtifact(plan.RepoRoot, artifact, artifact.path, bytesSHA256(body), plan.PlanDigest)
}

func writeRepositoryReceiptAtomic(root string, receipt *RepositoryReceipt) (bool, error) {
	body, err := encodeRepositoryReceipt(receipt)
	if err != nil {
		return false, err
	}
	target, err := safeBootstrapTarget(root, RepositoryReceiptRelativePath)
	if err != nil {
		return false, err
	}
	changed, err := atomicfile.WriteIfChanged(target, body, 0o644)
	if err != nil {
		return false, fmt.Errorf("publish repository receipt: %w", err)
	}
	return changed, nil
}

func encodeRepositoryReceipt(receipt *RepositoryReceipt) ([]byte, error) {
	if err := ValidateRepositoryReceipt(receipt); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode repository receipt: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxRepositoryReceiptBytes {
		return nil, fmt.Errorf("repository receipt exceeds %d bytes", maxRepositoryReceiptBytes)
	}
	return body, nil
}

func computeRepositoryReceiptDigest(receipt *RepositoryReceipt) (string, error) {
	if receipt == nil {
		return "", fmt.Errorf("repository receipt is nil")
	}
	copy := *receipt
	copy.ReceiptDigest = ""
	body, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("encode repository receipt digest: %w", err)
	}
	return bytesSHA256(body), nil
}

func policyPackIdentities(names []string) ([]PolicyPackIdentity, error) {
	identities := make([]PolicyPackIdentity, 0, len(names))
	for _, name := range names {
		content, err := presets.Load(name)
		if err != nil {
			return nil, err
		}
		identities = append(identities, PolicyPackIdentity{Name: name, Digest: bytesSHA256([]byte(content))})
	}
	return identities, nil
}

func harnessPackIdentities(selection Selection, productVersion string) ([]HarnessPackIdentity, error) {
	if len(selection.HarnessPacks) == 0 {
		return []HarnessPackIdentity{}, nil
	}
	pack, err := harness.Advanced(productVersion)
	if err != nil {
		return nil, err
	}
	return []HarnessPackIdentity{{
		Name: pack.Manifest.Name, Version: pack.Manifest.Version,
		MinimumProductVersion:   pack.Manifest.ProductCompatibility.Minimum,
		MaximumExclusiveVersion: pack.Manifest.ProductCompatibility.MaximumExclusive,
		Digest:                  pack.Manifest.Digest,
	}}, nil
}

func harnessPackIdentitiesForPlan(plan *Plan) ([]HarnessPackIdentity, error) {
	if len(plan.Selection.HarnessPacks) == 0 {
		return []HarnessPackIdentity{}, nil
	}
	if identities, err := harnessPackIdentities(plan.Selection, plan.ProductVersion); err == nil &&
		len(identities) == len(plan.Selection.HarnessPacks) &&
		identities[0].Digest == plan.Selection.HarnessPacks[0].Digest {
		return identities, nil
	}
	identities := make([]HarnessPackIdentity, len(plan.Selection.HarnessPacks))
	for index, selected := range plan.Selection.HarnessPacks {
		identities[index] = HarnessPackIdentity{
			Name: selected.Name, Version: selected.Version,
			MinimumProductVersion:   plan.ProductVersion,
			MaximumExclusiveVersion: "1.0.0", Digest: selected.Digest,
		}
	}
	return identities, nil
}

func validatePolicyPackIdentities(values []PolicyPackIdentity) error {
	seen := map[string]bool{}
	for index, value := range values {
		if strings.TrimSpace(value.Name) == "" || seen[value.Name] || !validSHA256(value.Digest) {
			return fmt.Errorf("repository receipt policy pack %d is invalid", index)
		}
		seen[value.Name] = true
	}
	return nil
}

func validateHarnessPackIdentities(values []HarnessPackIdentity) error {
	seen := map[string]bool{}
	for index, value := range values {
		if value.Name == "" || value.Version == "" || value.MinimumProductVersion == "" ||
			value.MaximumExclusiveVersion == "" || seen[value.Name] || !validSHA256(value.Digest) {
			return fmt.Errorf("repository receipt harness pack %d is invalid", index)
		}
		seen[value.Name] = true
	}
	return nil
}

func normalizeRepositoryReceipt(receipt *RepositoryReceipt) {
	sort.Strings(receipt.Hooks)
	sort.Strings(receipt.PolicySources)
	sort.Slice(receipt.ManagedFiles, func(i, j int) bool { return receipt.ManagedFiles[i].Path < receipt.ManagedFiles[j].Path })
	sort.Slice(receipt.ManagedBlocks, func(i, j int) bool { return receipt.ManagedBlocks[i].Path < receipt.ManagedBlocks[j].Path })
	sort.Slice(receipt.GeneratedArtifacts, func(i, j int) bool {
		return receipt.GeneratedArtifacts[i].Path < receipt.GeneratedArtifacts[j].Path
	})
	receipt.UserOwnedPaths = dedupePreservingOrder(receipt.UserOwnedPaths)
	sort.Strings(receipt.UserOwnedPaths)
}

func validateSortedStrings(values []string, label string) error {
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("repository receipt %s %d is empty", label, index)
		}
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("repository receipt %ss must be uniquely sorted", label)
		}
	}
	return nil
}

func validateSortedPaths(values []string, label string) error {
	for index, value := range values {
		if !validRepositoryRelativePath(value) {
			return fmt.Errorf("repository receipt %s %d is invalid", label, index)
		}
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("repository receipt %ss must be uniquely sorted", label)
		}
	}
	return nil
}

func validRepositoryRelativePath(value string) bool {
	return value != "" && len(value) <= 512 && !strings.Contains(value, `\`) &&
		!path.IsAbs(value) && path.Clean(value) == value && value != "." &&
		!strings.HasPrefix(value, "../")
}

func readRepositoryRegularFile(root, relative string) ([]byte, error) {
	target, err := safeBootstrapTarget(root, relative)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return nil, fmt.Errorf("inspect repository artifact %s: %w", relative, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxBinaryBytes {
		return nil, fmt.Errorf("repository artifact is not a bounded regular file: %s", relative)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("read repository artifact %s: %w", relative, err)
	}
	return body, nil
}

func extractManagedBlock(body []byte, start, end string) ([]byte, error) {
	text := string(body)
	if strings.Count(text, start) != 1 || strings.Count(text, end) != 1 {
		return nil, fmt.Errorf("managed block markers must occur exactly once")
	}
	startIndex := strings.Index(text, start)
	endIndex := strings.Index(text, end)
	if startIndex < 0 || endIndex < startIndex {
		return nil, fmt.Errorf("managed block markers are incomplete or out of order")
	}
	endIndex += len(end)
	if endIndex < len(text) && text[endIndex] == '\n' {
		endIndex++
	}
	return []byte(text[startIndex:endIndex]), nil
}

func managedMarkersForComponent(component, relative string) (string, string) {
	return managedMarkersForAction(Action{Component: component, Path: relative})
}

func syncOwnsComponent(component string) bool {
	return component == "hook-wrapper" || component == "binary" ||
		strings.HasPrefix(component, "binary@") ||
		strings.HasPrefix(component, "hook:") ||
		strings.HasPrefix(component, "harness-pack:")
}
