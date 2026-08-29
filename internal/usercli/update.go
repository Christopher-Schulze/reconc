package usercli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/buildprovenance"
	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedexec"
)

const maxLifecycleCommandOutput = 1 << 20

var beforeDirectUpdatePhase = func(string) error { return nil }

func update(ctx context.Context, currentVersion string, request UpdateRequest, apply bool) (*LifecycleReport, error) {
	operation := "update.check"
	if apply {
		operation = "update.apply"
	}
	diagnostic, err := DiagnoseGlobal(currentVersion)
	if err != nil {
		return nil, err
	}
	report := lifecycleFromDiagnostic(operation, diagnostic)
	if diagnostic.Blocking() || diagnostic.Owner == nil {
		report.Status = LifecycleRefused
		report.NextAction = diagnostic.NextAction
		return report, nil
	}
	if *diagnostic.Owner == ManagerSource {
		report.Status = LifecycleRefused
		report.NextAction = "Build the desired source revision, then run its path-qualified `reconc install-cli`."
		return report, nil
	}
	if *diagnostic.Owner != ManagerDirect || !diagnostic.ReceiptValid {
		report.Status = LifecycleRefused
		report.NextAction = "A valid direct-install receipt is required; run `reconc doctor --global`."
		return report, nil
	}
	requestedChannel, channelErr := selectedChannel(request)
	if channelErr != nil {
		report.Status = LifecycleRefused
		report.NextAction = channelErr.Error()
		return report, nil
	}
	// An exact receipt records a one-time version selection, not a persistent
	// update channel. Preview installations still require an explicit switch
	// back to stable so a bare update cannot silently leave preview.
	if diagnostic.Channel != nil && *diagnostic.Channel == ChannelPreview &&
		*diagnostic.Channel != requestedChannel &&
		request.Channel == "" && strings.TrimSpace(request.Version) == "" {
		report.Status = LifecycleRefused
		report.NextAction = "Select the channel explicitly with `--channel " + string(requestedChannel) + "`."
		return report, nil
	}
	release, err := selectRelease(ctx, request)
	if err != nil {
		report.Status = LifecycleFailed
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "release-selection", Status: "fail", Detail: err.Error(),
		})
		report.NextAction = "Correct the release selection or trust failure, then rerun the update."
		return report, nil
	}
	target := release.manifest.Version
	report.TargetVersion = &target
	report.Channel = &requestedChannel
	comparison, err := compareVersionStrings(currentVersion, target)
	if err != nil {
		report.Status = LifecycleFailed
		report.NextAction = err.Error()
		return report, nil
	}
	if comparison == 0 {
		paths, receiptErr := resolveReceiptPaths()
		if receiptErr != nil {
			report.Status = LifecycleFailed
			report.NextAction = receiptErr.Error()
			return report, nil
		}
		receipt, receiptErr := loadReceiptFile(paths.receipt)
		if receiptErr != nil {
			report.Status = LifecycleFailed
			report.NextAction = "Read the direct-install receipt before comparing artifact identity: " + receiptErr.Error()
			return report, nil
		}
		if receipt.ArtifactSHA256 == release.asset.SHA256 {
			report.Status = LifecycleCurrent
			report.NextAction = "Global Reconc already matches the selected release artifact."
			return report, nil
		}
	}
	if comparison > 0 && !request.AllowDowngrade {
		report.Status = LifecycleRefused
		report.NextAction = "Rerun with `--allow-downgrade` to authorize the selected downgrade."
		return report, nil
	}
	report.Status = LifecycleUpdateAvailable
	report.Actions = []DiagnosticAction{{
		Kind: "direct-update", Command: updateCommand(request),
		Detail: fmt.Sprintf("replace the receipt-owned direct binary with %s", target),
	}}
	report.NextAction = "Run `" + updateCommand(request) + "` to apply the verified update."
	if !apply {
		return report, nil
	}
	return applyDirectUpdate(ctx, report, release)
}

func applyDirectUpdate(ctx context.Context, report *LifecycleReport, release selectedRelease) (*LifecycleReport, error) {
	paths, err := resolveReceiptPaths()
	if err != nil {
		return nil, err
	}
	targetPath := ""
	provenanceState := ProvenanceEmbeddedVerified
	err = withReceiptLock(paths, func() error {
		receiptSnapshot, err := loadReceiptSnapshot(paths.receipt)
		if err != nil {
			return err
		}
		receipt := receiptSnapshot.receipt
		if receipt.Manager != ManagerDirect {
			return errors.New("direct installation ownership changed before update")
		}
		backup, err := captureBinaryBackup(receipt.BinaryPath)
		if err != nil {
			return err
		}
		return withCapturedBinaryBackup(backup, func(backup *binaryBackup) error {
			if !backup.exists || backup.digest != receipt.ArtifactSHA256 {
				return errors.New("direct installation changed before update")
			}
			if err := validateReceiptSnapshot(paths.receipt, receiptSnapshot); err != nil {
				return err
			}
			targetPath = receipt.BinaryPath
			parent := filepath.Dir(targetPath)
			return withPrivateTemporaryBinary(parent, ".reconc-update-*.candidate", func(candidatePath string) error {
				if err := beforeDirectUpdatePhase("materialize"); err != nil {
					return err
				}
				if err := materializeCandidate(ctx, release, candidatePath); err != nil {
					return err
				}
				if err := beforeDirectUpdatePhase("attestation"); err != nil {
					return err
				}
				state, err := verifyAttestation(ctx, candidatePath, release)
				if err != nil {
					return err
				}
				provenanceState = state
				if err := beforeDirectUpdatePhase("smoke"); err != nil {
					return err
				}
				if err := smokeCandidate(ctx, candidatePath, release.manifest.Version); err != nil {
					return err
				}
				if err := validateReceiptSnapshot(paths.receipt, receiptSnapshot); err != nil {
					return err
				}
				if err := beforeDirectUpdatePhase("publication"); err != nil {
					return err
				}
				if err := publishBinaryFromFileIfCurrent(targetPath, candidatePath, 0o755, backup); err != nil {
					if errors.Is(err, atomicfile.ErrCurrentChanged) {
						return err
					}
					return rollbackInstall(targetPath, backup, true, fmt.Errorf("publish update: %w", err))
				}
				updatedDigest, err := fileSHA256(targetPath)
				if err != nil || updatedDigest != release.asset.SHA256 {
					return rollbackInstall(targetPath, backup, true, errors.New("published update checksum verification failed"))
				}
				provenance, err := buildprovenance.InspectBinary(targetPath)
				if err != nil {
					return rollbackInstall(targetPath, backup, true, err)
				}
				input := ReceiptInput{
					Manager: ManagerDirect, Channel: channelForRelease(release),
					Version: release.manifest.Version, SourceRepository: releaseRepository,
					ReleaseTag: &release.manifest.Tag, ArtifactName: release.asset.Name,
					ArtifactSHA256: release.asset.SHA256, BinaryPath: receipt.BinaryPath,
					GOOS: provenance.GOOS, GOARCH: provenance.GOARCH,
					SourceDigest: provenance.SourceDigest, ProvenanceState: provenanceState,
					InstalledAt: time.Now().UTC(),
				}
				updatedReceipt, err := NewReceipt(input)
				if err != nil {
					return rollbackInstall(targetPath, backup, true, err)
				}
				if err := validateReceiptSnapshot(paths.receipt, receiptSnapshot); err != nil {
					return rollbackInstall(targetPath, backup, true, err)
				}
				if _, err := writeReceiptUnlocked(paths.receipt, updatedReceipt); err != nil {
					return rollbackInstall(targetPath, backup, true, err)
				}
				return nil
			})
		})
	})
	if err != nil {
		report.Status = LifecycleFailed
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "direct-update", Status: "fail", Detail: err.Error(),
		})
		report.NextAction = "The previous binary was retained or restored; resolve the failure and rerun the update."
		return report, nil
	}
	report.Status = LifecycleUpdated
	report.Changed = true
	report.BinaryPath = &targetPath
	report.Checks = append(report.Checks, DiagnosticCheck{
		Name: "direct-update", Status: "pass",
		Detail: fmt.Sprintf("published %s with %s provenance", release.manifest.Version, provenanceState),
	})
	report.NextAction = "Run `reconc doctor --global` to verify the updated installation."
	return report, nil
}

func smokeCandidate(ctx context.Context, candidate string, version string) error {
	output, err := boundedexec.CombinedOutput(lifecycleCommand(ctx, candidate, "--version"), maxLifecycleCommandOutput)
	if err != nil {
		return fmt.Errorf("candidate smoke test failed: %w", err)
	}
	if strings.TrimSpace(string(output)) != "reconc "+version {
		return fmt.Errorf("candidate smoke test returned unexpected version %q", strings.TrimSpace(string(output)))
	}
	return nil
}

func verifyAttestation(ctx context.Context, candidate string, release selectedRelease) (ProvenanceState, error) {
	tool := strings.TrimSpace(os.Getenv("RECONC_ATTESTATION_TOOL"))
	if tool == "" {
		tool = "gh"
	}
	path, err := exec.LookPath(tool)
	if err != nil {
		return "", fmt.Errorf("attestation verification requires %q on PATH", tool)
	}
	args := []string{
		"attestation", "verify", candidate, "--repo", releaseRepository,
		"--signer-workflow", releaseRepository + "/.github/workflows/reconc-release.yml",
		"--source-ref", "refs/tags/" + release.manifest.Tag,
		"--deny-self-hosted-runners",
	}
	if release.localDir != "" {
		bundle := filepath.Join(release.localDir, release.asset.Name+".sigstore.jsonl")
		root := filepath.Join(release.localDir, "trusted_root.jsonl")
		if _, bundleErr := os.Stat(bundle); bundleErr != nil {
			return "", errors.New("offline attestation bundle is required but missing")
		}
		if _, rootErr := os.Stat(root); rootErr != nil {
			return "", errors.New("offline trusted root is required but missing")
		}
		args = append(args, "--bundle", bundle, "--custom-trusted-root", root)
	}
	output, err := boundedexec.CombinedOutput(lifecycleCommand(ctx, path, args...), maxLifecycleCommandOutput)
	if err != nil {
		return "", fmt.Errorf("attestation verification failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return ProvenanceGitHubVerified, nil
}

func updateCommand(request UpdateRequest) string {
	command := "reconc update"
	if strings.TrimSpace(request.Version) != "" {
		command += " --version " + request.Version
	} else if request.Channel == ChannelPreview {
		command += " --channel preview"
	}
	if strings.TrimSpace(request.FromDir) != "" {
		command += " --from-dir " + quote(request.FromDir)
	}
	if request.AllowDowngrade {
		command += " --allow-downgrade"
	}
	return command
}

func compareVersionStrings(current string, target string) (int, error) {
	left, err := parseSemanticVersion(current)
	if err != nil {
		return 0, fmt.Errorf("running version cannot participate in update ordering: %w", err)
	}
	right, err := parseSemanticVersion(target)
	if err != nil {
		return 0, err
	}
	return compareSemanticVersions(left, right), nil
}

func channelForRelease(release selectedRelease) Channel {
	if release.channel != "" {
		return release.channel
	}
	if release.manifest.Prerelease {
		return ChannelPreview
	}
	return ChannelStable
}
