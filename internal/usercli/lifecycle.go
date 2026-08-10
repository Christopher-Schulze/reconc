package usercli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/schema"
)

const LifecycleFormatVersion = "reconc.global-lifecycle/v1"
const maxInstallationDirectoryEntries = 32

type LifecycleStatus string

const (
	LifecycleCurrent         LifecycleStatus = "current"
	LifecycleUpdateAvailable LifecycleStatus = "update-available"
	LifecycleUpdated         LifecycleStatus = "updated"
	LifecycleUninstalled     LifecycleStatus = "uninstalled"
	LifecycleRefused         LifecycleStatus = "refused"
	LifecycleFailed          LifecycleStatus = "failed"
)

type LifecycleReport struct {
	Schema         string             `json:"$schema"`
	FormatVersion  string             `json:"format_version"`
	Operation      string             `json:"operation"`
	Status         LifecycleStatus    `json:"status"`
	Changed        bool               `json:"changed"`
	Owner          *Manager           `json:"owner"`
	CurrentVersion string             `json:"current_version"`
	TargetVersion  *string            `json:"target_version"`
	Channel        *Channel           `json:"channel"`
	BinaryPath     *string            `json:"binary_path"`
	ReceiptPath    *string            `json:"receipt_path"`
	PlanDigest     *string            `json:"plan_digest"`
	Checks         []DiagnosticCheck  `json:"checks"`
	Actions        []DiagnosticAction `json:"actions"`
	NextAction     string             `json:"next_action"`
}

type UpdateRequest struct {
	Version        string
	Channel        Channel
	FromDir        string
	AllowDowngrade bool
}

type UninstallRequest struct {
	PurgeState bool
}

var lifecycleCommand = exec.CommandContext

func CheckUpdate(ctx context.Context, currentVersion string, request UpdateRequest) (*LifecycleReport, error) {
	return update(ctx, currentVersion, request, false)
}

func ApplyUpdate(ctx context.Context, currentVersion string, request UpdateRequest) (*LifecycleReport, error) {
	return update(ctx, currentVersion, request, true)
}

func Uninstall(ctx context.Context, currentVersion string, request UninstallRequest) (*LifecycleReport, error) {
	diagnostic, err := DiagnoseGlobal(currentVersion)
	if err != nil {
		return nil, err
	}
	report := lifecycleFromDiagnostic("uninstall", diagnostic)
	if diagnostic.Blocking() || diagnostic.Owner == nil {
		report.Status = LifecycleRefused
		report.NextAction = diagnostic.NextAction
		return report, nil
	}
	switch *diagnostic.Owner {
	case ManagerDirect, ManagerSource:
		return uninstallOwned(report, request)
	default:
		report.Status = LifecycleRefused
		report.NextAction = "Resolve installation ownership with `reconc doctor --global`."
		return report, nil
	}
}

func lifecycleFromDiagnostic(operation string, diagnostic *GlobalDiagnostic) *LifecycleReport {
	report := &LifecycleReport{
		Schema: schema.Resolve(schema.GlobalLifecycle), FormatVersion: LifecycleFormatVersion,
		Operation: operation, Status: LifecycleFailed, Changed: false,
		CurrentVersion: diagnostic.CurrentVersion, Owner: diagnostic.Owner,
		Channel: diagnostic.Channel, BinaryPath: diagnostic.BinaryPath,
		ReceiptPath: diagnostic.ReceiptPath, Checks: append([]DiagnosticCheck(nil), diagnostic.Checks...),
		Actions: []DiagnosticAction{}, NextAction: diagnostic.NextAction,
	}
	return report
}

func uninstallOwned(report *LifecycleReport, request UninstallRequest) (*LifecycleReport, error) {
	paths, err := resolveReceiptPaths()
	if err != nil {
		return nil, err
	}
	var removedPath string
	err = withReceiptLock(paths, func() error {
		if request.PurgeState {
			if err := validatePurgeInventory(paths, true); err != nil {
				return err
			}
		}
		receipt, err := loadReceiptFile(paths.receipt)
		if err != nil {
			return err
		}
		if report.Owner == nil || receipt.Manager != *report.Owner {
			return errors.New("installation ownership changed before uninstall")
		}
		info, err := os.Lstat(receipt.BinaryPath)
		if err != nil {
			return fmt.Errorf("inspect owned binary: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("owned binary is not a regular file")
		}
		digest, err := fileSHA256(receipt.BinaryPath)
		if err != nil {
			return err
		}
		if digest != receipt.ArtifactSHA256 {
			return errors.New("owned binary checksum no longer matches the installation receipt")
		}
		backup, err := captureBinaryBackup(receipt.BinaryPath)
		if err != nil {
			return err
		}
		if err := os.Remove(receipt.BinaryPath); err != nil {
			return fmt.Errorf("remove owned binary: %w", err)
		}
		if err := os.Remove(paths.receipt); err != nil {
			if backup.exists {
				_, restoreErr := atomicfile.WriteIfChanged(receipt.BinaryPath, backup.body, backup.mode)
				return errors.Join(fmt.Errorf("remove installation receipt: %w", err), restoreErr)
			}
			return fmt.Errorf("remove installation receipt: %w", err)
		}
		removedPath = receipt.BinaryPath
		return nil
	})
	if err != nil {
		report.Status = LifecycleRefused
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "owned-removal", Status: "fail", Detail: err.Error(),
		})
		report.NextAction = "Repair ownership with `reconc doctor --global` before uninstalling."
		return report, nil
	}
	if request.PurgeState {
		if err := purgeInstallationState(paths); err != nil {
			report.Status = LifecycleFailed
			report.Changed = true
			report.Checks = append(report.Checks, DiagnosticCheck{
				Name: "state-purge", Status: "fail", Detail: err.Error(),
			})
			report.NextAction = "Inspect the retained installation state before removing it manually."
			return report, nil
		}
	}
	report.Status = LifecycleUninstalled
	report.Changed = true
	report.BinaryPath = stringPointer(removedPath)
	report.Checks = append(report.Checks, DiagnosticCheck{
		Name: "owned-removal", Status: "pass", Detail: "removed receipt-owned binary and installation receipt",
	})
	report.Actions = []DiagnosticAction{}
	report.NextAction = "Global Reconc is uninstalled; repository policy and runtime evidence were preserved."
	return report, nil
}

func purgeInstallationState(paths receiptPaths) error {
	if err := validatePurgeInventory(paths, false); err != nil {
		return err
	}
	if err := os.Remove(paths.lock); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove installation lock: %w", err)
	}
	if err := os.Remove(paths.directory); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove empty installation state directory: %w", err)
	}
	return nil
}

func validatePurgeInventory(paths receiptPaths, receiptPresent bool) error {
	entries, err := boundedio.ReadDirNoSymlink(paths.directory, maxInstallationDirectoryEntries)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inventory installation state: %w", err)
	}
	allowed := map[string]bool{receiptLockFileName: true}
	if receiptPresent {
		allowed[receiptFileName] = true
	}
	unknown := make([]string, 0)
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			unknown = append(unknown, entry.Name())
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return fmt.Errorf("unknown installation state retained: %s", strings.Join(unknown, ", "))
	}
	return nil
}

func EncodeLifecycle(report *LifecycleReport) ([]byte, error) {
	if report == nil {
		return nil, errors.New("lifecycle report is required")
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode lifecycle report: %w", err)
	}
	return append(body, '\n'), nil
}

func supportedDirectTarget() bool {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64":
		return true
	default:
		return false
	}
}

func targetArtifact(version string) string {
	name := fmt.Sprintf("reconc-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func sameFileIdentity(path string, expected string) bool {
	left, leftErr := filepath.EvalSymlinks(path)
	right, rightErr := filepath.EvalSymlinks(expected)
	return leftErr == nil && rightErr == nil && samePath(left, right)
}
