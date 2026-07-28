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
)

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
	if diagnostic.Channel != nil && *diagnostic.Channel != requestedChannel &&
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
		report.Status = LifecycleCurrent
		report.NextAction = "Global Reconc is already at the selected version."
		return report, nil
	}
	if comparison > 0 && !request.AllowDowngrade {
		report.Status = LifecycleRefused
		report.NextAction = "Rerun with `--allow-downgrade` to authorize the selected downgrade."
		return report, nil
	}
	report.Status = LifecycleUpdateAvailable
	report.Actions = []DiagnosticAction{{
		Kind: "direct-update", Command: updateApplyCommand(request),
		Detail: fmt.Sprintf("replace the receipt-owned direct binary with %s", target),
	}}
	report.NextAction = "Run `" + updateApplyCommand(request) + "` to apply the verified update."
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
		receipt, err := loadReceiptFile(paths.receipt)
		if err != nil {
			return err
		}
		if receipt.Manager != ManagerDirect {
			return errors.New("direct installation ownership changed before update")
		}
		currentDigest, err := fileSHA256(receipt.BinaryPath)
		if err != nil || currentDigest != receipt.ArtifactSHA256 {
			return errors.New("direct installation changed before update")
		}
		targetPath = receipt.BinaryPath
		parent := filepath.Dir(targetPath)
		candidate, err := os.CreateTemp(parent, ".reconc-update-*.candidate")
		if err != nil {
			return fmt.Errorf("create update candidate: %w", err)
		}
		candidatePath := candidate.Name()
		if err := candidate.Close(); err != nil {
			_ = os.Remove(candidatePath)
			return err
		}
		defer os.Remove(candidatePath)
		if err := materializeCandidate(ctx, release, candidatePath); err != nil {
			return err
		}
		state, err := verifyAttestation(ctx, candidatePath, release)
		if err != nil {
			return err
		}
		provenanceState = state
		if err := smokeCandidate(ctx, candidatePath, release.manifest.Version); err != nil {
			return err
		}
		body, err := readBoundedBinary(candidatePath)
		if err != nil {
			return err
		}
		backup, err := captureBinaryBackup(targetPath)
		if err != nil {
			return err
		}
		changed, err := atomicfile.WriteIfChanged(targetPath, body, 0o755)
		if err != nil {
			return rollbackInstall(targetPath, backup, changed, fmt.Errorf("publish update: %w", err))
		}
		updatedDigest, err := fileSHA256(targetPath)
		if err != nil || updatedDigest != release.asset.SHA256 {
			return rollbackInstall(targetPath, backup, changed, errors.New("published update checksum verification failed"))
		}
		provenance, err := buildprovenance.InspectBinary(targetPath)
		if err != nil {
			return rollbackInstall(targetPath, backup, changed, err)
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
			return rollbackInstall(targetPath, backup, changed, err)
		}
		if _, err := writeReceiptUnlocked(paths.receipt, updatedReceipt); err != nil {
			return rollbackInstall(targetPath, backup, changed, err)
		}
		return nil
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
	output, err := lifecycleCommand(ctx, candidate, "--version").CombinedOutput()
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
	required := strings.TrimSpace(os.Getenv("RECONC_REQUIRE_ATTESTATION")) == "1"
	path, err := exec.LookPath(tool)
	if err != nil {
		if required {
			return "", fmt.Errorf("attestation verification requires %q on PATH", tool)
		}
		return ProvenanceEmbeddedVerified, nil
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
			if required {
				return "", errors.New("offline attestation bundle is required but missing")
			}
			return ProvenanceEmbeddedVerified, nil
		}
		if _, rootErr := os.Stat(root); rootErr != nil {
			if required {
				return "", errors.New("offline trusted root is required but missing")
			}
			return ProvenanceEmbeddedVerified, nil
		}
		args = append(args, "--bundle", bundle, "--custom-trusted-root", root)
	}
	output, err := lifecycleCommand(ctx, path, args...).CombinedOutput()
	if err != nil {
		if required {
			return "", fmt.Errorf("attestation verification failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return ProvenanceEmbeddedVerified, nil
	}
	return ProvenanceGitHubVerified, nil
}

func updateApplyCommand(request UpdateRequest) string {
	command := "reconc update apply"
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
