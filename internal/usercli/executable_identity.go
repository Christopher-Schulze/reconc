package usercli

import (
	"fmt"
	"runtime"

	"reconc.dev/reconc/internal/pathidentity"
)

// ExecutableReceiptIdentity is the point-in-time identity of an executable
// validated against the installation receipt that owns it.
type ExecutableReceiptIdentity struct {
	ExecutablePath string
	ReceiptPath    string
	ArtifactSHA256 string
}

// VerifyPATHReceiptIdentity verifies that bare `reconc` resolves to the
// regular executable owned by the current installation receipt.
func VerifyPATHReceiptIdentity() (*ExecutableReceiptIdentity, error) {
	return verifyExecutableReceiptIdentity(func() (string, error) {
		candidates, err := pathCandidates()
		if err != nil {
			return "", fmt.Errorf("resolve bare Reconc on PATH: %w", err)
		}
		if len(candidates) == 0 {
			return "", fmt.Errorf("bare `reconc` is not visible on PATH")
		}
		return candidates[0], nil
	})
}

// VerifyRunningReceiptIdentity verifies that the current process executable
// is the regular executable owned by the current installation receipt.
func VerifyRunningReceiptIdentity() (*ExecutableReceiptIdentity, error) {
	return verifyExecutableReceiptIdentity(currentExecutable)
}

func verifyExecutableReceiptIdentity(resolveExecutable func() (string, error)) (*ExecutableReceiptIdentity, error) {
	if resolveExecutable == nil {
		return nil, fmt.Errorf("executable identity resolver is required")
	}
	paths, err := resolveReceiptPaths()
	if err != nil {
		return nil, err
	}
	var identity *ExecutableReceiptIdentity
	err = withReceiptReadLock(paths, func() error {
		receipt, err := loadReceiptFile(paths.receipt)
		if err != nil {
			return fmt.Errorf("load installation receipt %s: %w", paths.receipt, err)
		}
		if receipt.GOOS != runtime.GOOS || receipt.GOARCH != runtime.GOARCH {
			return fmt.Errorf("installation receipt targets %s/%s, not this %s/%s host", receipt.GOOS, receipt.GOARCH, runtime.GOOS, runtime.GOARCH)
		}
		receiptPath, err := pathidentity.ResolveExisting(receipt.BinaryPath)
		if err != nil {
			return fmt.Errorf("resolve receipt-owned executable: %w", err)
		}
		executablePath, err := resolveExecutable()
		if err != nil {
			return err
		}
		if !samePath(executablePath, receiptPath) {
			return fmt.Errorf("executable %s is not the receipt-owned binary %s", executablePath, receiptPath)
		}
		digest, info, err := fileSHA256Snapshot(receipt.BinaryPath)
		if err != nil {
			return err
		}
		if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			return fmt.Errorf("receipt-owned executable is not executable: %s", receipt.BinaryPath)
		}
		if digest != receipt.ArtifactSHA256 {
			return fmt.Errorf("receipt-owned executable checksum changed at %s", receipt.BinaryPath)
		}
		identity = &ExecutableReceiptIdentity{
			ExecutablePath: receiptPath,
			ReceiptPath:    paths.receipt,
			ArtifactSHA256: digest,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if identity == nil {
		return nil, fmt.Errorf("installation receipt verification produced no executable identity")
	}
	return identity, nil
}
