package hooks

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"reconc.dev/reconc/internal/atomicfile"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/usercli"
)

const (
	KimiCodeManagedBlockStart = "# >>> reconc kimi-code hooks"
	KimiCodeManagedBlockEnd   = "# <<< reconc kimi-code hooks"
	kimiCodeLockName          = ".reconc-hooks.lock"
)

func installKimiCode(force bool) (*InstallReport, error) {
	if err := verifyKimiCodeCLIIdentity(); err != nil {
		return nil, err
	}
	configPath, home, err := kimiCodeConfigPath(true)
	if err != nil {
		return nil, err
	}
	var report *InstallReport
	err = withKimiCodeLock(home, func() error {
		existing, mode, exists, err := readKimiCodeConfig(configPath)
		if err != nil {
			return err
		}
		if exists {
			if err := validateKimiCodeTOML(existing); err != nil {
				return &rerrors.PolicySourceError{Message: "Kimi Code config is invalid TOML; refusing to modify it", Cause: err}
			}
		}
		artifact, err := generateKimiCode()
		if err != nil {
			return err
		}
		updated, action, replaced, err := mergeKimiCodeBlock(existing, artifact.Content, force)
		if err != nil {
			return err
		}
		if err := validateKimiCodeTOML(updated); err != nil {
			return &rerrors.PolicySourceError{Message: "generated Kimi Code config is invalid TOML", Cause: err}
		}
		backupPath := ""
		if replaced {
			backupPath, err = backupMalformedConfig(configPath, existing)
			if err != nil {
				return err
			}
		}
		if action != "unchanged" {
			if err := verifyKimiCodeCLIIdentity(); err != nil {
				return err
			}
			current, _, currentExists, readErr := readKimiCodeConfig(configPath)
			if readErr != nil {
				return readErr
			}
			if currentExists != exists || !bytes.Equal(current, existing) {
				return &rerrors.PolicySourceError{Message: "Kimi Code config changed after install preflight; retry"}
			}
			if _, err := atomicfile.WriteIfChanged(configPath, updated, mode); err != nil {
				return &rerrors.PolicySourceError{Message: "write Kimi Code config", Cause: err}
			}
		}
		report = &InstallReport{
			Kind: KindKimiCode, RepoRoot: "global", TargetPath: configPath,
			Action: action, NextAction: "Restart Kimi Code CLI so it reloads the global hook configuration.",
			BackupPath: backupPath,
		}
		return nil
	})
	return report, err
}

func uninstallKimiCode() (*UninstallReport, error) {
	configPath, home, err := kimiCodeConfigPath(false)
	if err != nil {
		return nil, err
	}
	report := &UninstallReport{
		Kind: KindKimiCode, RepoRoot: "global", TargetPath: configPath,
		Action: "absent", NextAction: "No Kimi Code hook restart is required.",
	}
	if home == "" {
		return report, nil
	}
	err = withKimiCodeLock(home, func() error {
		existing, mode, exists, err := readKimiCodeConfig(configPath)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if err := validateKimiCodeTOML(existing); err != nil {
			return &rerrors.PolicySourceError{Message: "Kimi Code config is invalid TOML; refusing removal", Cause: err}
		}
		artifact, err := generateKimiCode()
		if err != nil {
			return err
		}
		updated, removed, err := removeKimiCodeBlock(existing, artifact.Content)
		if err != nil {
			return err
		}
		if !removed {
			return nil
		}
		if err := validateKimiCodeTOML(updated); err != nil {
			return &rerrors.PolicySourceError{Message: "Kimi Code config would become invalid after removal", Cause: err}
		}
		current, _, currentExists, readErr := readKimiCodeConfig(configPath)
		if readErr != nil {
			return readErr
		}
		if !currentExists || !bytes.Equal(current, existing) {
			return &rerrors.PolicySourceError{Message: "Kimi Code config changed after uninstall preflight; retry"}
		}
		if _, err := atomicfile.WriteIfChanged(configPath, updated, mode); err != nil {
			return &rerrors.PolicySourceError{Message: "write Kimi Code config", Cause: err}
		}
		report.Action = "updated"
		report.RemovedEntries = kimiCodeHookCount()
		report.NextAction = "Restart Kimi Code CLI so it unloads the removed Reconc hooks."
		return nil
	})
	return report, err
}

func inspectKimiCodePlatform(platform Platform) PlatformStatus {
	report := PlatformStatus{
		Kind: platform.Kind, DisplayName: platform.DisplayName,
		TargetPath: platform.TargetPath, State: StateAbsent,
		Detail:         "global managed hook block not installed",
		ExpectedEvents: platformRuntimeEvents(platform),
	}
	artifact, generateErr := generateKimiCode()
	report.Generated = generateErr == nil
	configPath, _, err := kimiCodeConfigPath(false)
	if err != nil {
		report.State = StateDegraded
		report.Detail = err.Error()
		report.Remediation = "Set a valid KIMI_CODE_HOME, then rerun `reconc hook status`."
		return report
	}
	report.TargetPath = configPath
	data, _, exists, err := readKimiCodeConfig(configPath)
	if err != nil {
		report.State = StateDegraded
		report.Detail = "global config is unreadable: " + err.Error()
		report.Remediation = "Repair the Kimi Code config path, then rerun `reconc hook status`."
		return report
	}
	if !exists {
		report.Remediation = "Run `reconc hook install kimi-code`."
		return report
	}
	if err := validateKimiCodeTOML(data); err != nil {
		report.State = StateDegraded
		report.Detail = "global config is invalid TOML: " + err.Error()
		report.Remediation = "Repair the Kimi Code config manually; Reconc will not overwrite invalid global configuration."
		return report
	}
	block, present, blockErr := currentKimiCodeBlock(data)
	if blockErr != nil {
		report.State = StateDegraded
		report.Detail = blockErr.Error()
		report.Remediation = "Repair the managed marker pair manually, then rerun `reconc hook install kimi-code`."
		return report
	}
	if !present {
		report.Remediation = "Run `reconc hook install kimi-code`."
		return report
	}
	report.Installed = true
	if generateErr != nil || block != artifact.Content {
		report.State = StateDegraded
		report.Detail = "managed hook block differs from the current generator"
		report.Remediation = "Review the drift, then run `reconc hook install kimi-code --force`."
		return report
	}
	bareStatus, err := usercli.InspectRunningOnPATH()
	if err != nil {
		report.State = StateDegraded
		report.Detail = "managed hooks are installed but bare `reconc` identity cannot be verified: " + err.Error()
		report.Remediation = "Repair the Reconc user CLI, then rerun `reconc hook status`."
		return report
	}
	if !bareStatus.PathVisible {
		report.State = StateInstalled
		report.Detail = "managed hooks are installed but bare `reconc` is not visible on PATH"
		report.Remediation = "Install the Reconc user CLI on PATH, then restart Kimi Code CLI."
		return report
	}
	if !bareStatus.ChecksumCurrent {
		report.State = StateDegraded
		report.Executable = true
		report.Detail = "managed hooks are installed but bare `reconc` resolves to different bytes: " + bareStatus.ResolvedPath
		report.Remediation = "Run `" + bareStatus.RunningPath + " install-cli`, then restart Kimi Code CLI."
		return report
	}
	report.State = StateConfigured
	report.Configured = true
	report.Executable = true
	report.Detail = "global hook configuration is current and bare `reconc` is checksum-identical to the running build; live execution is reported separately"
	return report
}

func verifyKimiCodeCLIIdentity() error {
	status, err := usercli.InspectRunningOnPATH()
	if err != nil {
		return &rerrors.PolicySourceError{Message: "verify bare Reconc identity before Kimi Code hook installation", Cause: err}
	}
	if !status.PathVisible {
		return &rerrors.PolicySourceError{
			Message: "bare `reconc` is not visible on PATH; run `" + status.RunningPath + " install-cli` before installing global Kimi Code hooks",
		}
	}
	if !status.ChecksumCurrent {
		return &rerrors.PolicySourceError{
			Message: "bare `reconc` resolves to different bytes at " + status.ResolvedPath + "; run `" + status.RunningPath + " install-cli` before installing global Kimi Code hooks",
		}
	}
	return nil
}

func mergeKimiCodeBlock(existing []byte, generated string, force bool) ([]byte, string, bool, error) {
	current, present, err := currentKimiCodeBlock(existing)
	if err != nil {
		return nil, "", false, err
	}
	if present {
		if current == generated {
			return append([]byte(nil), existing...), "unchanged", false, nil
		}
		if !force {
			return nil, "", false, &rerrors.PolicySourceError{Message: "Kimi Code managed hook block differs from the current generator; review it and pass --force to replace only that marked block"}
		}
		return replaceKimiCodeBlock(existing, current, generated), "updated", true, nil
	}
	return append(append([]byte(nil), existing...), generated...), "created", false, nil
}

func removeKimiCodeBlock(existing []byte, generated string) ([]byte, bool, error) {
	current, present, err := currentKimiCodeBlock(existing)
	if err != nil {
		return nil, false, err
	}
	if !present {
		return append([]byte(nil), existing...), false, nil
	}
	if current != generated {
		return nil, false, &rerrors.PolicySourceError{Message: "Kimi Code managed hook block differs from the current generator; refusing to remove modified content"}
	}
	return replaceKimiCodeBlock(existing, current, ""), true, nil
}

func currentKimiCodeBlock(data []byte) (string, bool, error) {
	text := string(data)
	startCount := strings.Count(text, KimiCodeManagedBlockStart)
	endCount := strings.Count(text, KimiCodeManagedBlockEnd)
	if startCount == 0 && endCount == 0 {
		return "", false, nil
	}
	if startCount != 1 || endCount != 1 {
		return "", false, &rerrors.PolicySourceError{Message: "Kimi Code config has malformed or duplicate Reconc managed markers"}
	}
	start := strings.Index(text, KimiCodeManagedBlockStart)
	if start > 0 && text[start-1] == '\n' {
		start--
	}
	end := strings.Index(text, KimiCodeManagedBlockEnd)
	if end < start {
		return "", false, &rerrors.PolicySourceError{Message: "Kimi Code config closes the Reconc managed block before it opens"}
	}
	end += len(KimiCodeManagedBlockEnd)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return text[start:end], true, nil
}

func replaceKimiCodeBlock(existing []byte, current, replacement string) []byte {
	text := string(existing)
	start := strings.Index(text, current)
	if start < 0 {
		return append([]byte(nil), existing...)
	}
	end := start + len(current)
	return []byte(text[:start] + replacement + text[end:])
}

func validateKimiCodeTOML(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var document map[string]interface{}
	return toml.Unmarshal(data, &document)
}

func readKimiCodeConfig(path string) ([]byte, os.FileMode, bool, error) {
	data, err := readManagedArtifact(path)
	if os.IsNotExist(err) {
		return nil, 0o600, false, nil
	}
	if err != nil {
		return nil, 0, false, &rerrors.PolicySourceError{Message: "read Kimi Code config", Cause: err}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, false, &rerrors.PolicySourceError{Message: "inspect Kimi Code config", Cause: err}
	}
	return data, info.Mode().Perm(), true, nil
}

func kimiCodeConfigPath(createHome bool) (string, string, error) {
	home := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", "", &rerrors.PolicySourceError{Message: "resolve user home for Kimi Code config", Cause: err}
		}
		home = filepath.Join(userHome, ".kimi-code")
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return "", "", &rerrors.PolicySourceError{Message: "resolve Kimi Code home", Cause: err}
	}
	resolved, err := pathidentity.ResolveProspective(absolute)
	if err != nil {
		return "", "", &rerrors.PolicySourceError{Message: "resolve Kimi Code home identity", Cause: err}
	}
	info, statErr := os.Lstat(resolved)
	if os.IsNotExist(statErr) && createHome {
		if err := os.MkdirAll(resolved, 0o700); err != nil {
			return "", "", &rerrors.PolicySourceError{Message: "create Kimi Code home", Cause: err}
		}
		info, statErr = os.Lstat(resolved)
	}
	if os.IsNotExist(statErr) {
		return filepath.Join(resolved, "config.toml"), "", nil
	}
	if statErr != nil {
		return "", "", &rerrors.PolicySourceError{Message: "inspect Kimi Code home", Cause: statErr}
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", &rerrors.PolicySourceError{Message: "Kimi Code home is not a real directory: " + resolved}
	}
	return filepath.Join(resolved, "config.toml"), resolved, nil
}

func withKimiCodeLock(home string, fn func() error) error {
	if home == "" {
		return errors.New("missing Kimi Code home")
	}
	lockPath := filepath.Join(home, kimiCodeLockName)
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return &rerrors.PolicySourceError{Message: "open Kimi Code hook lock", Cause: err}
	}
	defer lock.Close()
	unlock, err := filelock.Lock(lock)
	if err != nil {
		return &rerrors.PolicySourceError{Message: "lock Kimi Code hook config", Cause: err}
	}
	defer unlock()
	return fn()
}

func kimiCodeHookCount() int {
	platform, ok := PlatformForKind(KindKimiCode)
	if !ok {
		return 0
	}
	count := 0
	for _, capability := range platform.Capabilities {
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent != "" && !binding.Compatibility {
				count++
			}
		}
	}
	return count
}
