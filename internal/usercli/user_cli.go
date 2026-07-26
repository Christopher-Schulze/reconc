// Package usercli installs and verifies the interactive Reconc command.
package usercli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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
	Status  *Status `json:"status"`
	Changed bool    `json:"changed"`
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
	if resolved, lookErr := exec.LookPath("reconc"); lookErr == nil {
		status.PathVisible = true
		if canonical, resolveErr := pathidentity.ResolveExisting(resolved); resolveErr == nil {
			status.ResolvedPath = canonical
			if digest, hashErr := fileSHA256(canonical); hashErr == nil {
				status.Ready = digest == expected
			}
		}
	}
	status.NextAction = nextAction(status, directory)
	return status, nil
}

func InstallCurrent(installDir string) (*InstallReport, error) {
	status, err := InspectCurrent(installDir)
	if err != nil {
		return nil, err
	}
	if err := ensureRealDirectory(filepath.Dir(status.TargetPath)); err != nil {
		return nil, err
	}
	body, err := readBoundedBinary(status.SourcePath)
	if err != nil {
		return nil, err
	}
	changed, err := atomicfile.WriteIfChanged(status.TargetPath, body, 0o755)
	if err != nil {
		return nil, fmt.Errorf("install user CLI: %w", err)
	}
	verified, err := InspectCurrent(installDir)
	if err != nil {
		return nil, err
	}
	if !verified.Installed || !verified.Executable || !verified.Current {
		return nil, fmt.Errorf("installed user CLI failed checksum or executable verification: %s", verified.TargetPath)
	}
	return &InstallReport{Status: verified, Changed: changed}, nil
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
