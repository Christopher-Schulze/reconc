// Package usercli installs and verifies the interactive Reconc command.
package usercli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	FormatVersion  = "reconc.user-cli/v1"
	maxBinaryBytes = 256 << 20
)

type Status struct {
	FormatVersion  string            `json:"format_version"`
	SourcePath     string            `json:"source_path"`
	TargetPath     string            `json:"target_path"`
	ResolvedPath   string            `json:"resolved_path,omitempty"`
	ExpectedSHA256 string            `json:"expected_sha256"`
	Installed      bool              `json:"installed"`
	Executable     bool              `json:"executable"`
	Current        bool              `json:"current"`
	PathVisible    bool              `json:"path_visible"`
	Ready          bool              `json:"ready"`
	NextAction     string            `json:"next_action"`
	Diagnostics    []DiagnosticCheck `json:"diagnostics"`
}

// BareStatus reports whether the command resolved by bare `reconc` has the
// exact bytes of the currently running executable. Global host integrations
// need this narrower invariant because they cannot use a repository-local
// wrapper path.
type BareStatus struct {
	RunningPath     string `json:"running_path"`
	ResolvedPath    string `json:"resolved_path,omitempty"`
	ExpectedSHA256  string `json:"expected_sha256"`
	ResolvedSHA256  string `json:"resolved_sha256,omitempty"`
	PathVisible     bool   `json:"path_visible"`
	ChecksumCurrent bool   `json:"checksum_current"`
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

func InspectRunningOnPATH() (*BareStatus, error) {
	source, err := currentExecutable()
	if err != nil {
		return nil, err
	}
	expected, err := fileSHA256(source)
	if err != nil {
		return nil, err
	}
	status := &BareStatus{RunningPath: source, ExpectedSHA256: expected}
	candidates, err := pathCandidates()
	if err != nil {
		return nil, fmt.Errorf("inspect bare Reconc PATH identity: %w", err)
	}
	if len(candidates) == 0 {
		return status, nil
	}
	status.PathVisible = true
	status.ResolvedPath = candidates[0]
	status.ResolvedSHA256, err = fileSHA256(status.ResolvedPath)
	if err != nil {
		return nil, fmt.Errorf("inspect bare Reconc checksum identity: %w", err)
	}
	status.ChecksumCurrent = status.ResolvedSHA256 == status.ExpectedSHA256
	return status, nil
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
		ExpectedSHA256: expected, Diagnostics: []DiagnosticCheck{},
	}
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() {
			status.Installed = true
			status.Executable = runtime.GOOS == "windows" || info.Mode()&0o111 != 0
			if digest, hashErr := fileSHA256(target); hashErr == nil {
				status.Current = digest == expected
			} else {
				status.Diagnostics = append(status.Diagnostics, DiagnosticCheck{
					Name: "target-checksum", Status: "fail", Detail: hashErr.Error(),
				})
			}
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("inspect user CLI target: %w", statErr)
	}
	candidates, pathDiagnostics, err := pathCandidatesDetailed()
	if err != nil {
		return nil, fmt.Errorf("inspect user CLI PATH: %w", err)
	}
	status.Diagnostics = append(status.Diagnostics, pathDiagnostics...)
	if len(candidates) > 0 {
		resolved := candidates[0]
		status.PathVisible = true
		status.ResolvedPath = resolved
		if targetIdentity, targetErr := pathidentity.ResolveExisting(target); targetErr == nil &&
			samePath(resolved, targetIdentity) {
			if digest, hashErr := fileSHA256(resolved); hashErr == nil {
				status.Ready = digest == expected
			} else {
				status.Diagnostics = append(status.Diagnostics, DiagnosticCheck{
					Name: "resolved-checksum", Status: "fail", Detail: hashErr.Error(),
				})
			}
		} else if targetErr != nil && status.Installed {
			status.Diagnostics = append(status.Diagnostics, DiagnosticCheck{
				Name: "target-identity", Status: "fail", Detail: targetErr.Error(),
			})
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
		changed := !status.Installed || !status.Executable || !status.Current
		install := func(backup *binaryBackup) error {
			if changed {
				if err := publishBinaryFromFile(status.TargetPath, status.SourcePath, 0o755); err != nil {
					return rollbackInstall(status.TargetPath, backup, true, fmt.Errorf("install user CLI: %w", err))
				}
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
		}
		if !changed {
			return install(&binaryBackup{})
		}
		return withBinaryBackup(status.TargetPath, install)
	})
	if err != nil {
		return nil, err
	}
	return report, nil
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
	for _, diagnostic := range status.Diagnostics {
		if diagnostic.Status == "fail" {
			return "Resolve user CLI diagnostic `" + diagnostic.Name + "` before reinstalling: " + diagnostic.Detail
		}
	}
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

func fileSHA256(path string) (string, error) {
	digest, _, err := fileSHA256Snapshot(path)
	return digest, err
}

func fileSHA256Snapshot(path string) (string, os.FileInfo, error) {
	hash := sha256.New()
	var opened os.FileInfo
	err := boundedio.WithRegularFileSnapshot(path, maxBinaryBytes, func(file *os.File, info os.FileInfo) error {
		opened = info
		written, copyErr := io.CopyBuffer(hash, file, make([]byte, binaryCopyBufferBytes))
		if copyErr != nil {
			return copyErr
		}
		if written != info.Size() {
			return fmt.Errorf("hashed %d of %d bytes", written, info.Size())
		}
		return nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("open or hash %s for checksum: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), opened, nil
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
