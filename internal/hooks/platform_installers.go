package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/pathidentity"
)

func installDevinCLI(repoRoot string, force bool) (*InstallReport, error) {
	root, err := existingRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(root, DevinHooksPath)
	if err := requireManagedTargetWithin(root, target); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}
	artifact, err := Generate(KindDevinCLI)
	if err != nil {
		return nil, err
	}
	var generatedHooks map[string]interface{}
	if err := json.Unmarshal([]byte(artifact.Content), &generatedHooks); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "internal: generated Devin artifact is not valid JSON", Cause: err}
	}

	action := "created"
	snapshot, err := readManagedArtifactSnapshot(target)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	if snapshot.exists {
		action = "updated"
	}
	existing := snapshot.body
	mergedHooks := generatedHooks
	var mergeDiff MergeDiff
	backupPath := ""
	if !emptyJSONConfig(existing) {
		var existingHooks map[string]interface{}
		if err := json.Unmarshal(existing, &existingHooks); err != nil {
			if !force {
				return nil, &rerrors.PolicySourceError{Message: target + " is not valid JSON; pass --force to overwrite with a fresh reconc config", Cause: err}
			}
			backupPath, err = backupMalformedConfig(target, existing)
			if err != nil {
				return nil, err
			}
		} else {
			wrappedDest := map[string]interface{}{"hooks": existingHooks}
			wrappedGenerated := map[string]interface{}{"hooks": generatedHooks}
			mergeDiff = mergeReconcHooks(wrappedDest, wrappedGenerated, MergeOptions{Force: force, Kind: KindDevinCLI})
			if len(mergeDiff.Blocked) != 0 {
				return nil, &rerrors.PolicySourceError{
					Message: target + " has incompatible hook values: " + strings.Join(mergeDiff.Blocked, "; ") + "; pass --force to replace only those values",
				}
			}
			if hooksValue, ok := wrappedDest["hooks"].(map[string]interface{}); ok {
				mergedHooks = hooksValue
			}
		}
	}
	out, err := marshalHostJSON(mergedHooks)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "marshal merged Devin hooks", Cause: err}
	}
	if writeAction, err := writeGeneratedArtifact(target, string(append(out, '\n')), false, snapshot); err != nil {
		return nil, err
	} else if writeAction == "unchanged" {
		action = writeAction
	}
	return &InstallReport{
		Kind:             KindDevinCLI,
		RepoRoot:         root,
		TargetPath:       DevinHooksPath,
		Action:           action,
		NextAction:       "Restart Devin CLI in this repository or run `/hooks` to verify .devin/hooks.v1.json is loaded.",
		DroppedUserEdits: mergeDiff.Removed,
		BackupPath:       backupPath,
	}, nil
}

func installKilo(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindKilo,
		repoRoot,
		force,
		func(data []byte) bool { return managedPlatformArtifact(KindKilo, data) },
		true,
		"Restart Kilo Code in this repository so it reloads .kilo/plugin/reconc.js; KILO_PURE must be unset.",
	)
}

func installGitHubCopilot(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindGitHubCopilot,
		repoRoot,
		force,
		func(data []byte) bool { return managedPlatformArtifact(KindGitHubCopilot, data) },
		false,
		"Restart Copilot CLI in this repository or start a new Copilot cloud agent job so .github/hooks/reconc.json is loaded.",
	)
}

func installGrok(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindGrok,
		repoRoot,
		force,
		func(data []byte) bool { return managedPlatformArtifact(KindGrok, data) },
		true,
		"Restart Grok Build or reload /hooks, then run /hooks-trust once for this project so .grok/hooks/reconc.json can execute.",
	)
}

func installOMP(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindOMP,
		repoRoot,
		force,
		func(data []byte) bool { return managedPlatformArtifact(KindOMP, data) },
		false,
		"Restart OMP from this repository root so it loads .omp/extensions/reconc.ts.",
	)
}

func installDSH(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindDSH,
		repoRoot,
		force,
		func(data []byte) bool { return managedPlatformArtifact(KindDSH, data) },
		false,
		"Start DSH from this repository with `npx --yes @deepseek-ai/dsh@0.1.5-rc.2 --profile headless --patch .dsh/reconc.patch.yml`.",
	)
}

func installPi(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindPi,
		repoRoot,
		force,
		func(data []byte) bool { return managedPlatformArtifact(KindPi, data) },
		false,
		"Restart Pi from this repository root and trust the project when prompted so it loads .pi/extensions/reconc.ts; non-interactive runs may use --approve for that run.",
	)
}

func installManagedPlatformFile(kind, repoRoot string, force bool, managed func([]byte) bool, allowForceForeign bool, nextAction string) (*InstallReport, error) {
	root, err := existingRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	artifact, err := Generate(kind)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(root, artifact.TargetPath)
	if err := requireManagedTargetWithin(root, target); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}
	action := "created"
	snapshot, err := readManagedArtifactSnapshot(target)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	if snapshot.exists {
		action = "updated"
		if !managed(snapshot.body) {
			if !force || !allowForceForeign {
				message := artifact.TargetPath + " exists and is not reconc-managed"
				if allowForceForeign {
					message += "; pass --force to overwrite"
				} else {
					message += "; refusing to overwrite this user-owned hook file"
				}
				return nil, &rerrors.PolicySourceError{Message: message}
			}
		}
	}
	if writeAction, err := writeGeneratedArtifact(target, artifact.Content, artifact.Executable, snapshot); err != nil {
		return nil, err
	} else if writeAction == "unchanged" {
		action = writeAction
	}
	report := &InstallReport{Kind: kind, RepoRoot: root, TargetPath: artifact.TargetPath, Action: action, Executable: artifact.Executable, NextAction: nextAction}
	if snapshot.exists && action == "updated" && managed(snapshot.body) && !legacyGeneratedArtifact(kind, snapshot.body) {
		report.DroppedUserEdits = []string{artifact.TargetPath + ": managed content replaced; review local customizations"}
	}
	return report, nil
}

func isManagedGitHubCopilotConfig(data []byte) bool {
	if legacyGeneratedArtifact(KindGitHubCopilot, data) {
		return true
	}
	var topLevel map[string]json.RawMessage
	if json.Unmarshal(data, &topLevel) != nil || len(topLevel) != 2 {
		return false
	}
	if _, ok := topLevel["version"]; !ok {
		return false
	}
	if _, ok := topLevel["hooks"]; !ok {
		return false
	}
	var document struct {
		Version int                                 `json:"version"`
		Hooks   map[string][]map[string]interface{} `json:"hooks"`
	}
	if json.Unmarshal(data, &document) != nil || document.Version != 1 || len(document.Hooks) == 0 {
		return false
	}
	generated, err := Generate(KindGitHubCopilot)
	if err != nil {
		return false
	}
	var current struct {
		Hooks map[string][]map[string]interface{} `json:"hooks"`
	}
	if json.Unmarshal([]byte(generated.Content), &current) != nil {
		return false
	}
	for event, entries := range document.Hooks {
		canonical := current.Hooks[event]
		if len(entries) != 1 || len(canonical) != 1 || !managedCopilotEntry(entries[0], canonical[0]) {
			return false
		}
	}
	return true
}

func managedCopilotEntry(entry, canonical map[string]interface{}) bool {
	for key := range entry {
		switch key {
		case "type", "bash", "powershell", "cwd", "timeoutSec", "matcher":
		default:
			return false
		}
	}
	for _, key := range []string{"type", "bash", "powershell", "cwd"} {
		value, ok := entry[key].(string)
		if !ok || value != canonical[key] {
			return false
		}
	}
	return true
}

func existingRepoRoot(repoRoot string) (string, error) {
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return "", &rerrors.PolicySourceError{Message: "resolve repository filesystem identity", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root, Cause: err}
	}
	return root, nil
}
