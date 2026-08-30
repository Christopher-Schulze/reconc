package hooks

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	tomlunstable "github.com/pelletier/go-toml/v2/unstable"

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

type kimiCodeManagedBlock struct {
	start int
	end   int
}

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
		snapshot, err := readManagedArtifactSnapshot(configPath)
		if err != nil {
			return err
		}
		existing, exists := snapshot.body, snapshot.exists
		mode := os.FileMode(0o600)
		if exists {
			mode = snapshot.info.Mode().Perm()
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
			if _, err := publishManagedArtifact(configPath, updated, mode, snapshot); err != nil {
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
		snapshot, err := readManagedArtifactSnapshot(configPath)
		if err != nil {
			return err
		}
		existing, exists := snapshot.body, snapshot.exists
		if !exists {
			return nil
		}
		mode := snapshot.info.Mode().Perm()
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
		if _, err := publishManagedArtifact(configPath, updated, mode, snapshot); err != nil {
			return &rerrors.PolicySourceError{Message: "write Kimi Code config", Cause: err}
		}
		report.Action = "updated"
		report.RemovedEntries = kimiCodeHookCount()
		report.NextAction = "Restart Kimi Code CLI so it unloads the removed Reconc hooks."
		return nil
	})
	return report, err
}

func inspectKimiCodePlatform(platform Platform) (report PlatformStatus) {
	report = PlatformStatus{
		Kind: platform.Kind, DisplayName: platform.DisplayName,
		TargetPath: platform.TargetPath, State: StateAbsent,
		Detail:         "global managed hook block not installed",
		ExpectedEvents: platformRuntimeEvents(platform),
		remediation:    hookInstallRemediation(platform.Kind, "", false),
	}
	defer func() { applyRemediation(&report, report.remediation) }()
	artifact, generateErr := generateKimiCode()
	report.Generated = generateErr == nil
	configPath, _, err := kimiCodeConfigPath(false)
	if err != nil {
		report.State = StateDegraded
		report.Detail = err.Error()
		report.remediation = hostRemediation("Set a valid KIMI_CODE_HOME, then rerun hook status.", remediationCommand{})
		return report
	}
	report.TargetPath = configPath
	data, _, exists, err := readKimiCodeConfig(configPath)
	if err != nil {
		report.State = StateDegraded
		report.Detail = "global config is unreadable: " + err.Error()
		report.remediation = manualRemediation("Repair the Kimi Code config path, then rerun hook status; Reconc will not overwrite an unreadable global configuration.")
		return report
	}
	if !exists {
		return report
	}
	if err := validateKimiCodeTOML(data); err != nil {
		report.State = StateDegraded
		report.Detail = "global config is invalid TOML: " + err.Error()
		report.remediation = manualRemediation("Repair the Kimi Code config manually; Reconc will not overwrite invalid global configuration.")
		return report
	}
	block, present, blockErr := currentKimiCodeBlock(data)
	if blockErr != nil {
		report.State = StateDegraded
		report.Detail = blockErr.Error()
		report.remediation = manualRemediation("Repair the managed marker pair manually before reinstalling; Reconc cannot identify a safe replacement boundary.")
		return report
	}
	if !present {
		return report
	}
	report.Installed = true
	if generateErr != nil || !bytes.Equal(data[block.start:block.end], []byte(artifact.Content)) {
		report.State = StateDegraded
		report.Detail = "managed hook block differs from the current generator"
		report.remediation = hookInstallRemediation(platform.Kind, "", true)
		return report
	}
	bareStatus, err := usercli.InspectRunningOnPATH()
	if err != nil {
		report.State = StateDegraded
		report.Detail = "managed hooks are installed but bare `reconc` identity cannot be verified: " + err.Error()
		report.remediation = hostRemediation("Repair the Reconc user CLI, then rerun hook status.", remediationCommand{})
		return report
	}
	if !bareStatus.PathVisible {
		report.State = StateInstalled
		report.Detail = "managed hooks are installed but bare `reconc` is not visible on PATH"
		report.remediation = hostRemediation("Install the Reconc user CLI on PATH, then restart Kimi Code CLI.", remediationCommand{})
		return report
	}
	if !bareStatus.ChecksumCurrent {
		report.State = StateDegraded
		report.Executable = true
		report.Detail = "managed hooks are installed but bare `reconc` resolves to different bytes: " + bareStatus.ResolvedPath
		report.remediation = hostRemediation("Repair the user CLI, then restart Kimi Code CLI:", remediationCommand{Program: bareStatus.RunningPath, Args: []string{"install-cli"}})
		return report
	}
	report.State = StateConfigured
	report.Configured = true
	report.Executable = true
	report.Detail = "global hook configuration is current and bare `reconc` is checksum-identical to the running build; live execution is reported separately"
	report.remediation = noRemediation()
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
		if bytes.Equal(existing[current.start:current.end], []byte(generated)) {
			return append([]byte(nil), existing...), "unchanged", false, nil
		}
		if !force {
			return nil, "", false, &rerrors.PolicySourceError{Message: "Kimi Code managed hook block differs from the current generator; review it and pass --force to replace only that marked block"}
		}
		updated, err := replaceKimiCodeBlock(existing, current, generated)
		return updated, "updated", true, err
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
	if !bytes.Equal(existing[current.start:current.end], []byte(generated)) {
		return nil, false, &rerrors.PolicySourceError{Message: "Kimi Code managed hook block differs from the current generator; refusing to remove modified content"}
	}
	updated, err := replaceKimiCodeBlock(existing, current, "")
	return updated, err == nil, err
}

func currentKimiCodeBlock(data []byte) (kimiCodeManagedBlock, bool, error) {
	parser := tomlunstable.Parser{KeepComments: true}
	parser.Reset(data)
	start, end := -1, -1
	startCount, endCount := 0, 0
	for parser.NextExpression() {
		node := parser.Expression()
		if node == nil || node.Kind != tomlunstable.Comment {
			continue
		}
		offset := int(node.Raw.Offset)
		if offset > 0 && data[offset-1] != '\n' {
			continue
		}
		switch {
		case bytes.Equal(node.Data, []byte(KimiCodeManagedBlockStart)):
			startCount++
			start = int(node.Raw.Offset)
		case bytes.Equal(node.Data, []byte(KimiCodeManagedBlockEnd)):
			endCount++
			end = int(node.Raw.Offset + node.Raw.Length)
		}
	}
	if err := parser.Error(); err != nil {
		return kimiCodeManagedBlock{}, false, &rerrors.PolicySourceError{Message: "parse Kimi Code config while locating the Reconc managed block", Cause: err}
	}
	if startCount == 0 && endCount == 0 {
		return kimiCodeManagedBlock{}, false, nil
	}
	if startCount != 1 || endCount != 1 {
		return kimiCodeManagedBlock{}, false, &rerrors.PolicySourceError{Message: "Kimi Code config has malformed or duplicate Reconc managed markers"}
	}
	if end < start {
		return kimiCodeManagedBlock{}, false, &rerrors.PolicySourceError{Message: "Kimi Code config closes the Reconc managed block before it opens"}
	}
	if start > 0 && data[start-1] == '\n' {
		start--
		if start > 0 && data[start-1] == '\r' {
			start--
		}
	}
	if end < len(data) && data[end] == '\r' && end+1 < len(data) && data[end+1] == '\n' {
		end += 2
	} else if end < len(data) && data[end] == '\n' {
		end++
	}
	return kimiCodeManagedBlock{start: start, end: end}, true, nil
}

func replaceKimiCodeBlock(existing []byte, current kimiCodeManagedBlock, replacement string) ([]byte, error) {
	if current.start < 0 || current.end < current.start || current.end > len(existing) {
		return nil, &rerrors.PolicySourceError{Message: "Kimi Code managed hook block boundary is invalid"}
	}
	updated := make([]byte, 0, len(existing)-(current.end-current.start)+len(replacement))
	updated = append(updated, existing[:current.start]...)
	updated = append(updated, replacement...)
	updated = append(updated, existing[current.end:]...)
	return updated, nil
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
	unlock, err := filelock.LockContext(context.Background(), lock, filelock.DefaultTimeout)
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
