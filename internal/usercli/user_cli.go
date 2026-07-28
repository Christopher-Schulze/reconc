// Package usercli installs and verifies the interactive Reconc command.
package usercli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	FormatVersion  = "reconc.user-cli/v1"
	maxBinaryBytes = 256 << 20
)

type Status struct {
	FormatVersion  string `json:"format_version"`
	SourcePath     string `json:"source_path"`
	TargetPath     string `json:"target_path"`
	ResolvedPath   string `json:"resolved_path,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256"`
	Installed      bool   `json:"installed"`
	Executable     bool   `json:"executable"`
	Current        bool   `json:"current"`
	PathVisible    bool   `json:"path_visible"`
	Ready          bool   `json:"ready"`
	NextAction     string `json:"next_action"`
}

type InstallReport struct {
	Status      *Status  `json:"status"`
	Receipt     *Receipt `json:"receipt,omitempty"`
	ReceiptPath string   `json:"receipt_path,omitempty"`
	Changed     bool     `json:"changed"`
}

type InstallOptions struct {
	Version         string
	Manager         Manager
	Channel         Channel
	ArtifactName    string
	ReleaseTag      string
	ProvenanceState ProvenanceState
	InstalledAt     time.Time
}

func InspectCurrent(installDir string) (*Status, error) {
	source, err := currentExecutable()
	if err != nil {
		return nil, err
	}
	expected, err := fileSHA256(source)
	if err != nil {
		return nil, err
	}
	directory, err := resolveInstallDirectory(installDir)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(directory, executableName())
	status := &Status{
		FormatVersion: FormatVersion, SourcePath: source, TargetPath: target,
		ExpectedSHA256: expected,
	}
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() {
			status.Installed = true
			status.Executable = runtime.GOOS == "windows" || info.Mode()&0o111 != 0
			if digest, hashErr := fileSHA256(target); hashErr == nil {
				status.Current = digest == expected
			}
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("inspect user CLI target: %w", statErr)
	}
	candidates, err := pathCandidates()
	if err != nil {
		return nil, fmt.Errorf("inspect user CLI PATH: %w", err)
	}
	if len(candidates) > 0 {
		resolved := candidates[0]
		status.PathVisible = true
		status.ResolvedPath = resolved
		if targetIdentity, targetErr := pathidentity.ResolveExisting(target); targetErr == nil &&
			samePath(resolved, targetIdentity) {
			if digest, hashErr := fileSHA256(resolved); hashErr == nil {
				status.Ready = digest == expected
			}
		}
	}
	status.NextAction = nextAction(status, directory)
	return status, nil
}

func InstallCurrent(installDir string) (*InstallReport, error) {
	return installCurrent(installDir, InstallOptions{}, false)
}

func InstallCurrentWithReceipt(installDir string, options InstallOptions) (*InstallReport, error) {
	return installCurrent(installDir, options, true)
}

func installCurrent(installDir string, options InstallOptions, publishReceipt bool) (*InstallReport, error) {
	paths, err := resolveReceiptPaths()
	if err != nil {
		return nil, err
	}
	var report *InstallReport
	err = withReceiptLock(paths, func() error {
		status, inspectErr := InspectCurrent(installDir)
		if inspectErr != nil {
			return inspectErr
		}
		if err := ensureRealDirectory(filepath.Dir(status.TargetPath)); err != nil {
			return err
		}
		backup, err := captureBinaryBackup(status.TargetPath)
		if err != nil {
			return err
		}
		body, err := readBoundedBinary(status.SourcePath)
		if err != nil {
			return err
		}
		changed, err := atomicfile.WriteIfChanged(status.TargetPath, body, 0o755)
		if err != nil {
			return fmt.Errorf("install user CLI: %w", err)
		}
		verified, err := InspectCurrent(installDir)
		if err != nil {
			return rollbackInstall(status.TargetPath, backup, changed, err)
		}
		if !verified.Installed || !verified.Executable || !verified.Current {
			verificationErr := fmt.Errorf("installed user CLI failed checksum or executable verification: %s", verified.TargetPath)
			return rollbackInstall(status.TargetPath, backup, changed, verificationErr)
		}
		report = &InstallReport{Status: verified, Changed: changed}
		if !publishReceipt || !verified.Ready {
			return nil
		}
		input, err := installReceiptInput(verified, options)
		if err != nil {
			return rollbackInstall(status.TargetPath, backup, changed, err)
		}
		receipt, err := NewReceipt(input)
		if err != nil {
			return rollbackInstall(status.TargetPath, backup, changed, err)
		}
		receiptChanged, err := writeReceiptUnlocked(paths.receipt, receipt)
		if err != nil {
			return rollbackInstall(status.TargetPath, backup, changed, err)
		}
		report.Receipt = receipt
		report.ReceiptPath = paths.receipt
		report.Changed = report.Changed || receiptChanged
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

type binaryBackup struct {
	exists bool
	body   []byte
	mode   os.FileMode
}

func captureBinaryBackup(path string) (binaryBackup, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return binaryBackup{}, nil
	}
	if err != nil {
		return binaryBackup{}, fmt.Errorf("inspect previous user CLI: %w", err)
	}
	if !info.Mode().IsRegular() {
		return binaryBackup{}, fmt.Errorf("previous user CLI is not a regular file: %s", path)
	}
	body, err := readBoundedBinary(path)
	if err != nil {
		return binaryBackup{}, fmt.Errorf("capture previous user CLI: %w", err)
	}
	return binaryBackup{exists: true, body: body, mode: info.Mode().Perm()}, nil
}

func rollbackInstall(path string, backup binaryBackup, changed bool, cause error) error {
	if !changed {
		return cause
	}
	if backup.exists {
		if _, err := atomicfile.WriteIfChanged(path, backup.body, backup.mode); err != nil {
			return errors.Join(cause, fmt.Errorf("restore previous user CLI: %w", err))
		}
		return cause
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return errors.Join(cause, fmt.Errorf("remove failed user CLI publication: %w", err))
	}
	return cause
}

func installReceiptInput(status *Status, options InstallOptions) (ReceiptInput, error) {
	version := strings.TrimSpace(options.Version)
	if version == "" {
		return ReceiptInput{}, fmt.Errorf("installation version is required for receipt publication")
	}
	manager := options.Manager
	if manager == "" {
		manager = ManagerSource
	}
	switch manager {
	case ManagerSource:
		return sourceReceiptInput(status, version, options.InstalledAt)
	case ManagerDirect:
		channel := options.Channel
		if channel == "" {
			channel = ChannelExact
		}
		artifactName := strings.TrimSpace(options.ArtifactName)
		if artifactName == "" {
			artifactName = fmt.Sprintf("reconc-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				artifactName += ".exe"
			}
		}
		releaseTag := strings.TrimSpace(options.ReleaseTag)
		if releaseTag == "" {
			releaseTag = "reconc-v" + version
		}
		provenanceState := options.ProvenanceState
		if provenanceState == "" {
			provenanceState = ProvenanceEmbeddedVerified
		}
		return directReceiptInput(status, version, channel, artifactName, releaseTag, provenanceState, options.InstalledAt)
	default:
		return ReceiptInput{}, fmt.Errorf("unsupported installation owner %q", manager)
	}
}

func currentExecutable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve running reconc executable: %w", err)
	}
	resolved, err := pathidentity.ResolveExisting(path)
	if err != nil {
		return "", fmt.Errorf("resolve running reconc executable identity: %w", err)
	}
	return resolved, nil
}

func resolveInstallDirectory(explicit string) (string, error) {
	directory := strings.TrimSpace(explicit)
	if directory == "" {
		directory = strings.TrimSpace(os.Getenv("RECONC_INSTALL_DIR"))
	}
	if directory == "" {
		if runtime.GOOS == "windows" {
			localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
			if localAppData == "" {
				return "", fmt.Errorf("LOCALAPPDATA is unavailable; set RECONC_INSTALL_DIR or --install-dir")
			}
			directory = filepath.Join(localAppData, "Programs", "Reconc", "bin")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve user home for CLI install: %w", err)
			}
			directory = filepath.Join(home, ".local", "bin")
		}
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve user CLI install directory: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "reconc.exe"
	}
	return "reconc"
}

func nextAction(status *Status, directory string) string {
	install := quote(status.SourcePath) + " install-cli"
	if directory != defaultInstallDirectory() {
		install += " --install-dir " + quote(directory)
	}
	if !status.Installed || !status.Executable || !status.Current {
		return install
	}
	if !status.PathVisible {
		return pathRemediation(directory)
	}
	if !status.Ready {
		return "Move " + quote(directory) + " before " + quote(filepath.Dir(status.ResolvedPath)) + " on PATH, then open a new terminal."
	}
	return "User CLI is current and directly callable as `reconc`."
}

func defaultInstallDirectory() string {
	directory, err := resolveInstallDirectory("")
	if err != nil {
		return ""
	}
	return directory
}

func pathRemediation(directory string) string {
	if runtime.GOOS == "windows" {
		escaped := strings.ReplaceAll(directory, "'", "''")
		return "Put the install directory first on the user PATH, then open a new terminal: " +
			"$userPath = [Environment]::GetEnvironmentVariable('Path', 'User'); " +
			"[Environment]::SetEnvironmentVariable('Path', (('" + escaped + ";' + $userPath).TrimEnd(';')), 'User')"
	}
	return "Add this line to your shell profile, then open a new terminal: export PATH=" + quote(directory) + ":$PATH"
}

func quote(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `'\''`) + `'`
}

func readBoundedBinary(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open running Reconc binary: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxBinaryBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read running Reconc binary: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close running Reconc binary: %w", closeErr)
	}
	if len(body) > maxBinaryBytes {
		return nil, fmt.Errorf("running Reconc binary exceeds %d bytes", maxBinaryBytes)
	}
	return body, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for checksum: %w", path, err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, maxBinaryBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("hash %s: %w", path, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close %s after checksum: %w", path, closeErr)
	}
	if written > maxBinaryBytes {
		return "", fmt.Errorf("binary exceeds %d bytes: %s", maxBinaryBytes, path)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ensureRealDirectory(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve user CLI directory: %w", err)
	}
	if info, statErr := os.Lstat(absolute); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("user CLI install path is not a real directory: %s", absolute)
		}
		return nil
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect user CLI directory: %w", statErr)
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return fmt.Errorf("create user CLI directory: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return fmt.Errorf("verify user CLI directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("user CLI install path is not a real directory: %s", absolute)
	}
	return nil
}
