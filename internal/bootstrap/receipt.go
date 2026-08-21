package bootstrap

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/repositoryignore"
)

const (
	InstallReceiptFormatVersion = "reconc.bootstrap.install-receipt/v1"
	maxInstallReceiptBytes      = 4 << 20
)

type InstallReceipt struct {
	FormatVersion  string                 `json:"format_version"`
	ProductVersion string                 `json:"product_version"`
	RepoRoot       string                 `json:"repo_root"`
	PlanDigest     string                 `json:"plan_digest"`
	HarnessPacks   []HarnessPackSelection `json:"harness_packs,omitempty"`
	Entries        []InstallReceiptEntry  `json:"entries"`
	Digest         string                 `json:"digest"`
}

type InstallReceiptEntry struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Mode       uint32 `json:"mode"`
	Ownership  string `json:"ownership"` // "file" | "managed-block"
	BlockStart string `json:"block_start,omitempty"`
	BlockEnd   string `json:"block_end,omitempty"`
}

func installReceiptPath(planDigest string) string {
	return filepath.ToSlash(filepath.Join(".reconc", "bootstrap-install-"+planDigest[:12]+".json"))
}

func buildInstallReceipt(plan *Plan, productVersion, lockSHA, planRecordPath, recordedPlanSHA string) (*InstallReceipt, error) {
	entries := []InstallReceiptEntry{}
	hasCreatedArtifact := false
	for _, action := range plan.Actions {
		start, end := managedMarkersForAction(action)
		switch action.State {
		case ActionCreate:
			hasCreatedArtifact = true
			entries = append(entries, InstallReceiptEntry{
				Path: action.Path, SHA256: action.DesiredSHA256, Mode: action.Mode,
				Ownership: "file", BlockStart: start, BlockEnd: end,
			})
		case ActionUnchanged:
			if start != "" {
				entries = append(entries, InstallReceiptEntry{
					Path: action.Path, SHA256: action.DesiredSHA256, Mode: action.Mode,
					Ownership: "managed-block", BlockStart: start, BlockEnd: end,
				})
			}
		}
	}
	if plan.CompileRequired {
		if !validSHA256(lockSHA) {
			return nil, fmt.Errorf("compiled policy lock checksum is invalid")
		}
		hasCreatedArtifact = true
		entries = append(entries, InstallReceiptEntry{
			Path: policyLockfilePath, SHA256: lockSHA, Mode: 0o644, Ownership: "file",
		})
	}
	// An all-unchanged replay must not mint a competing ownership receipt.
	if !hasCreatedArtifact {
		return nil, nil
	}
	if planRecordPath == "" || !validSHA256(recordedPlanSHA) {
		return nil, fmt.Errorf("recorded bootstrap plan identity is invalid")
	}
	entries = append(entries, InstallReceiptEntry{
		Path: planRecordPath, SHA256: recordedPlanSHA, Mode: 0o600, Ownership: "file",
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	receipt := &InstallReceipt{
		FormatVersion: InstallReceiptFormatVersion, ProductVersion: productVersion,
		RepoRoot: plan.RepoRoot, PlanDigest: plan.PlanDigest,
		HarnessPacks: append([]HarnessPackSelection{}, plan.Selection.HarnessPacks...),
		Entries:      entries,
	}
	digest, err := computeInstallReceiptDigest(receipt)
	if err != nil {
		return nil, err
	}
	receipt.Digest = digest
	return receipt, nil
}

func managedMarkersForAction(action Action) (string, string) {
	switch action.Component {
	case "ignore-policy":
		return repositoryignore.BlockStart, repositoryignore.BlockEnd
	case "agent-doc":
		return agentBlockStart, agentBlockEnd
	case "documentation":
		if action.Path == "docs/documentation.md" {
			return docsBlockStart, docsBlockEnd
		}
	case "hook-activation:codex":
		return hooks.CodexActivationBlockStart, hooks.CodexActivationBlockEnd
	}
	return "", ""
}

func writeInstallReceipt(plan *Plan, receipt *InstallReceipt) (createdRecord, []createdDirectory, string, error) {
	if receipt == nil {
		return createdRecord{}, nil, "", nil
	}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return createdRecord{}, nil, "", fmt.Errorf("encode bootstrap install receipt: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxInstallReceiptBytes {
		return createdRecord{}, nil, "", fmt.Errorf("bootstrap install receipt exceeds %d bytes", maxInstallReceiptBytes)
	}
	relative := installReceiptPath(plan.PlanDigest)
	artifact := desiredArtifact{component: "install-receipt", path: relative, mode: 0o600, content: body}
	record, dirs, err := publishArtifact(plan.RepoRoot, artifact, relative, bytesSHA256(body), plan.PlanDigest)
	return record, dirs, relative, err
}

func loadInstallReceipt(plan *Plan) (*InstallReceipt, string, error) {
	relative := installReceiptPath(plan.PlanDigest)
	path := filepath.Join(plan.RepoRoot, filepath.FromSlash(relative))
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, relative, fmt.Errorf("inspect bootstrap install receipt %s: %w", relative, err)
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, relative, fmt.Errorf("bootstrap install receipt must be a real regular file: %s", relative)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, relative, fmt.Errorf("open bootstrap install receipt %s: %w", relative, err)
	}
	info, err := file.Stat()
	if err != nil {
		closeErr := file.Close()
		return nil, relative, combineWriteFailure("stat bootstrap install receipt", err, closeErr, nil)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxInstallReceiptBytes {
		_ = file.Close()
		return nil, relative, fmt.Errorf("bootstrap install receipt is not a bounded regular file: %s", relative)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxInstallReceiptBytes+1))
	decoder.DisallowUnknownFields()
	var receipt InstallReceipt
	decodeErr := decoder.Decode(&receipt)
	var extra interface{}
	extraErr := decoder.Decode(&extra)
	closeErr := file.Close()
	if decodeErr != nil {
		return nil, relative, fmt.Errorf("decode bootstrap install receipt: %w", decodeErr)
	}
	if extraErr != io.EOF {
		return nil, relative, fmt.Errorf("bootstrap install receipt must contain exactly one JSON document")
	}
	if closeErr != nil {
		return nil, relative, fmt.Errorf("close bootstrap install receipt: %w", closeErr)
	}
	if err := validateInstallReceipt(plan, &receipt); err != nil {
		return nil, relative, err
	}
	return &receipt, relative, nil
}

func validateInstallReceipt(plan *Plan, receipt *InstallReceipt) error {
	if receipt == nil || receipt.FormatVersion != InstallReceiptFormatVersion {
		return fmt.Errorf("unsupported bootstrap install receipt format")
	}
	if receipt.RepoRoot != plan.RepoRoot || receipt.PlanDigest != plan.PlanDigest || receipt.ProductVersion != plan.ProductVersion {
		return fmt.Errorf("bootstrap install receipt does not belong to the supplied plan")
	}
	if len(receipt.HarnessPacks) != len(plan.Selection.HarnessPacks) {
		return fmt.Errorf("bootstrap install receipt harness pack selection drifted")
	}
	for index := range receipt.HarnessPacks {
		if receipt.HarnessPacks[index] != plan.Selection.HarnessPacks[index] {
			return fmt.Errorf("bootstrap install receipt harness pack selection drifted")
		}
	}
	digest, err := computeInstallReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.Digest != digest {
		return fmt.Errorf("bootstrap install receipt digest mismatch")
	}
	actions := map[string]Action{}
	for _, action := range plan.Actions {
		actions[action.Path] = action
	}
	for index, entry := range receipt.Entries {
		if entry.Path == "" || !validSHA256(entry.SHA256) || (entry.Mode != 0o600 && entry.Mode != 0o644 && entry.Mode != 0o755) {
			return fmt.Errorf("bootstrap install receipt entry %d is invalid", index)
		}
		if _, err := safeBootstrapTarget(plan.RepoRoot, entry.Path); err != nil {
			return err
		}
		if index > 0 && receipt.Entries[index-1].Path >= entry.Path {
			return fmt.Errorf("bootstrap install receipt entries must be uniquely sorted")
		}
		if entry.Path == policyLockfilePath {
			if !plan.CompileRequired || entry.Ownership != "file" {
				return fmt.Errorf("bootstrap install receipt claims an unowned policy lock")
			}
			continue
		}
		if entry.Path == recordedPlanPath(plan) {
			if entry.Ownership != "file" || entry.Mode != 0o600 {
				return fmt.Errorf("bootstrap install receipt has invalid recorded plan ownership")
			}
			continue
		}
		action, ok := actions[entry.Path]
		if !ok || entry.SHA256 != action.DesiredSHA256 || entry.Mode != action.Mode {
			return fmt.Errorf("bootstrap install receipt entry does not map to plan action %s", entry.Path)
		}
		start, end := managedMarkersForAction(action)
		switch entry.Ownership {
		case "file":
			if action.State != ActionCreate {
				return fmt.Errorf("bootstrap install receipt claims pre-existing file %s", entry.Path)
			}
		case "managed-block":
			if action.State != ActionUnchanged || start == "" {
				return fmt.Errorf("bootstrap install receipt claims unmanaged block %s", entry.Path)
			}
		default:
			return fmt.Errorf("bootstrap install receipt entry %s has invalid ownership %q", entry.Path, entry.Ownership)
		}
		if entry.BlockStart != start || entry.BlockEnd != end {
			return fmt.Errorf("bootstrap install receipt markers drifted for %s", entry.Path)
		}
	}
	return nil
}

func computeInstallReceiptDigest(receipt *InstallReceipt) (string, error) {
	copy := *receipt
	copy.Digest = ""
	body, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("encode bootstrap install receipt digest: %w", err)
	}
	return bytesSHA256(body), nil
}
