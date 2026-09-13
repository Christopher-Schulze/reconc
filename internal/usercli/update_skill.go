package usercli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/schema"
	skillbundle "reconc.dev/reconc/skills/reconc"
)

const maxSelectedSkillAssetBytes = 1 << 20

func selectedSkillAssets(release selectedRelease) (ReleaseAsset, ReleaseAsset, bool, error) {
	base := "reconc-skill-" + release.manifest.Version
	manifest, hasManifest := releaseAssetByName(release.manifest, base+".json")
	archive, hasArchive := releaseAssetByName(release.manifest, base+".zip")
	if hasManifest != hasArchive {
		return ReleaseAsset{}, ReleaseAsset{}, false, errors.New("selected release has an incomplete portable skill bundle")
	}
	return manifest, archive, hasManifest, nil
}

func readSelectedSkillAsset(ctx context.Context, release selectedRelease, asset ReleaseAsset) ([]byte, error) {
	if asset.Size <= 0 || asset.Size > maxSelectedSkillAssetBytes {
		return nil, fmt.Errorf("selected skill asset %s exceeds the bounded size", asset.Name)
	}
	var body []byte
	var err error
	if release.localDir != "" {
		body, _, err = boundedio.ReadRegularFileSnapshot(filepath.Join(release.localDir, asset.Name), maxSelectedSkillAssetBytes)
	} else {
		endpoint := releaseDownloadBase + "/" + release.manifest.Tag + "/" + asset.Name
		body, err = downloadBounded(ctx, endpoint, maxSelectedSkillAssetBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("read selected skill asset %s: %w", asset.Name, err)
	}
	digest := sha256.Sum256(body)
	if int64(len(body)) != asset.Size || hex.EncodeToString(digest[:]) != asset.SHA256 {
		return nil, fmt.Errorf("selected skill asset %s differs from the release manifest", asset.Name)
	}
	return body, nil
}

func loadSelectedSkillManifest(ctx context.Context, release selectedRelease, path string) (*SkillReceipt, ReleaseAsset, error) {
	manifestAsset, archiveAsset, available, err := selectedSkillAssets(release)
	if err != nil || !available {
		return nil, ReleaseAsset{}, err
	}
	body, err := readSelectedSkillAsset(ctx, release, manifestAsset)
	if err != nil {
		return nil, ReleaseAsset{}, err
	}
	receipt, err := decodeSkillManifest(body, path)
	if err != nil {
		return nil, ReleaseAsset{}, fmt.Errorf("decode selected skill manifest: %w", err)
	}
	return receipt, archiveAsset, nil
}

func decodeSkillManifest(body []byte, path string) (*SkillReceipt, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest skillbundle.Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if manifest.FormatVersion != "1" || manifest.Name != skillbundle.Name {
		return nil, errors.New("skill manifest has an unsupported identity")
	}
	receipt := &SkillReceipt{Path: path, ManifestDigest: manifest.Digest, Files: make([]SkillFileReceipt, 0, len(manifest.Files))}
	for _, file := range manifest.Files {
		receipt.Files = append(receipt.Files, SkillFileReceipt{Path: file.Path, Size: file.Size, SHA256: file.SHA256})
	}
	if err := validateSkillReceipt(receipt); err != nil {
		return nil, fmt.Errorf("invalid skill manifest: %w", err)
	}
	return receipt, nil
}

func verifyCandidateSkillManifest(ctx context.Context, candidate string, target *SkillReceipt) error {
	output, err := boundedexec.CombinedOutput(lifecycleCommand(ctx, candidate, "skill-manifest", "--json"), maxSelectedSkillAssetBytes)
	if err != nil {
		return fmt.Errorf("candidate skill manifest probe failed: %w", err)
	}
	actual, err := decodeSkillManifest(output, target.Path)
	if err != nil {
		return fmt.Errorf("candidate skill manifest is invalid: %w", err)
	}
	if !sameSkillReceipt(actual, target) {
		return errors.New("selected release skill bundle differs from the candidate binary's embedded skill")
	}
	return nil
}

func loadSelectedSkillFiles(ctx context.Context, release selectedRelease, receipt *SkillReceipt, asset ReleaseAsset) ([]skillbundle.File, error) {
	body, err := readSelectedSkillAsset(ctx, release, asset)
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("decode selected skill archive: %w", err)
	}
	if len(reader.File) != len(receipt.Files) {
		return nil, errors.New("selected skill archive has an unexpected file count")
	}
	files := make([]skillbundle.File, 0, len(receipt.Files))
	for index, expected := range receipt.Files {
		entry := reader.File[index]
		if entry.Name != skillbundle.Name+"/"+expected.Path || !entry.Mode().IsRegular() ||
			entry.UncompressedSize64 != uint64(expected.Size) || entry.Mode().Perm() != 0o644 {
			return nil, fmt.Errorf("selected skill archive entry differs from manifest: %s", entry.Name)
		}
		opened, err := entry.Open()
		if err != nil {
			return nil, err
		}
		content, readErr := io.ReadAll(io.LimitReader(opened, int64(expected.Size+1)))
		closeErr := opened.Close()
		if readErr != nil || closeErr != nil {
			return nil, errors.Join(readErr, closeErr)
		}
		digest := sha256.Sum256(content)
		if len(content) != expected.Size || hex.EncodeToString(digest[:]) != expected.SHA256 {
			return nil, fmt.Errorf("selected skill archive content differs for %s", expected.Path)
		}
		files = append(files, skillbundle.File{Path: expected.Path, Size: expected.Size, SHA256: expected.SHA256, Data: content})
	}
	return files, nil
}

func inspectSelectedSkill(ctx context.Context, release selectedRelease, previous *Receipt) (*SkillUpdateReport, *SkillReceipt, ReleaseAsset, error) {
	path := ""
	if previous != nil && previous.Skill != nil {
		path = previous.Skill.Path
	}
	var err error
	path, err = resolveSkillDirectory(path)
	if err != nil {
		return nil, nil, ReleaseAsset{}, err
	}
	report := &SkillUpdateReport{Path: path, Discovery: skillDiscoveryDetail(path)}
	if previous != nil && previous.Skill != nil {
		report.InstalledManifestDigest = stringPointer(previous.Skill.ManifestDigest)
	}
	info, statErr := os.Lstat(path)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		report.State = SkillMissing
	case statErr != nil:
		return nil, nil, ReleaseAsset{}, fmt.Errorf("inspect installed skill: %w", statErr)
	case previous == nil || previous.Skill == nil:
		report.State = SkillUnmanaged
	case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
		report.State = SkillModifiedOwned
	case verifySkillTree(path, previous.Skill.Files) != nil:
		report.State = SkillModifiedOwned
	default:
	}
	target, archive, err := loadSelectedSkillManifest(ctx, release, path)
	if err != nil {
		return nil, nil, ReleaseAsset{}, err
	}
	if target == nil {
		if report.State == "" {
			report.State = SkillUnavailable
		}
		return report, nil, ReleaseAsset{}, nil
	}
	report.TargetManifestDigest = stringPointer(target.ManifestDigest)
	if report.State == "" {
		if sameSkillReceipt(previous.Skill, target) {
			report.State = SkillCurrent
		} else {
			report.State = SkillStaleOwned
		}
	}
	if report.State == SkillMissing {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, nil, ReleaseAsset{}, fmt.Errorf("resolve skill installation action cwd: %w", err)
		}
		report.Action = &SkillUpdateAction{
			Kind: "install-skill", Argv: []string{"reconc", "install-cli", "--skill-only"},
			Cwd: cwd, Authorization: "explicit-user-action",
		}
	}
	return report, target, archive, nil
}

func applyOwnedSkillUpdate(ctx context.Context, report *LifecycleReport, release selectedRelease, target *SkillReceipt, archive ReleaseAsset) (*LifecycleReport, error) {
	if target == nil || report.Skill == nil || report.Skill.State != SkillStaleOwned ||
		report.Skill.InstalledManifestDigest == nil {
		return nil, errors.New("owned skill update requires a verified stale target")
	}
	files, err := loadSelectedSkillFiles(ctx, release, target, archive)
	if err != nil {
		report.Status = LifecycleFailed
		report.Checks = append(report.Checks, DiagnosticCheck{Name: "selected-skill", Status: "fail", Detail: err.Error()})
		report.NextAction = "Verify the selected skill archive and rerun the update."
		return report, nil
	}
	paths, err := resolveReceiptPaths()
	if err != nil {
		return nil, err
	}
	mutationCommitted := false
	rollbackFailed := false
	err = withReceiptLock(paths, func() (resultErr error) {
		snapshot, err := loadReceiptSnapshot(paths.receipt)
		if err != nil {
			return err
		}
		previous := snapshot.receipt
		if previous.Manager != ManagerDirect || previous.Skill == nil ||
			previous.ArtifactSHA256 != release.asset.SHA256 ||
			!samePath(previous.Skill.Path, target.Path) ||
			previous.Skill.ManifestDigest != *report.Skill.InstalledManifestDigest {
			return errors.New("direct binary or owned skill changed before skill-only update")
		}
		backup, err := captureBinaryBackup(previous.BinaryPath)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, backup.cleanup()) }()
		if !backup.exists || backup.digest != previous.ArtifactSHA256 {
			return errors.New("direct binary changed before skill-only update")
		}
		skill, err := prepareSkillInstallationPayload(target.Path, previous, target, files)
		if skill != nil {
			defer func() { resultErr = errors.Join(resultErr, skill.cleanup()) }()
		}
		if err != nil {
			return err
		}
		if !skill.changed {
			return errors.New("owned skill changed before skill-only update")
		}
		rollback := func(cause error) error {
			cause = skill.rollback(cause)
			if err := verifySkillTree(skill.target, skill.previous.Files); err != nil {
				rollbackFailed = true
			}
			return cause
		}
		updated := *previous
		updated.Schema = schema.Resolve(schema.InstallationReceipt)
		updated.FormatVersion = ReceiptFormatVersion
		updated.Skill = target
		updated.InstalledAt = time.Now().UTC().Format(time.RFC3339)
		updated.ReceiptDigest, err = computeReceiptDigest(&updated)
		if err != nil {
			return err
		}
		if err := validateReceiptSnapshot(paths.receipt, snapshot); err != nil {
			return err
		}
		if err := validateBinaryBackupSnapshot(previous.BinaryPath, &backup); err != nil {
			return err
		}
		if err := skill.publish(); err != nil {
			return rollback(err)
		}
		if err := beforeSkillReceiptPublish(paths.receipt); err != nil {
			return rollback(err)
		}
		if err := validateReceiptSnapshot(paths.receipt, snapshot); err != nil {
			return rollback(err)
		}
		if err := validateBinaryBackupSnapshot(previous.BinaryPath, &backup); err != nil {
			return rollback(err)
		}
		if _, err := writeReceiptUnlocked(paths.receipt, &updated); err != nil {
			return rollback(err)
		}
		skill.committed = true
		mutationCommitted = true
		return nil
	})
	if err != nil {
		report.Status = LifecycleFailed
		report.Changed = mutationCommitted || rollbackFailed
		report.Checks = append(report.Checks, DiagnosticCheck{Name: "skill-update", Status: "fail", Detail: err.Error()})
		report.NextAction = "Inspect the owned skill, receipt, and any retained backup before retrying."
		return report, nil
	}
	report.Status = LifecycleUpdated
	report.Changed = true
	report.Skill.State = SkillCurrent
	report.Skill.InstalledManifestDigest = stringPointer(target.ManifestDigest)
	report.Checks = append(report.Checks, DiagnosticCheck{Name: "skill-update", Status: "pass", Detail: "installed selected release skill bundle without changing the binary"})
	report.NextAction = "Run `reconc doctor --global` to verify the updated skill."
	return report, nil
}
