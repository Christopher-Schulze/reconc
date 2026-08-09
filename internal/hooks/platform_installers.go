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
	existing, err := readManagedArtifact(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	mergedHooks := generatedHooks
	var mergeDiff MergeDiff
	backupPath := ""
	if len(existing) != 0 && strings.TrimSpace(string(existing)) != "{}" {
		action = "updated"
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
			mergeDiff = mergeReconcHooks(wrappedDest, wrappedGenerated, MergeOptions{KeepUserEdits: false})
			if hooksValue, ok := wrappedDest["hooks"].(map[string]interface{}); ok {
				mergedHooks = hooksValue
			}
		}
	}
	out, err := json.MarshalIndent(mergedHooks, "", "  ")
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "marshal merged Devin hooks", Cause: err}
	}
	if writeAction, err := writeGeneratedArtifact(target, string(append(out, '\n')), false); err != nil {
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
		func(data []byte) bool {
			text := string(data)
			return strings.Contains(text, "Managed by reconc") && strings.Contains(text, "kilo-pre-tool-use")
		},
		true,
		"Restart Kilo Code in this repository so it reloads .kilo/plugin/reconc.js; KILO_PURE must be unset.",
	)
}

func installGitHubCopilot(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindGitHubCopilot,
		repoRoot,
		force,
		isManagedGitHubCopilotConfig,
		false,
		"Restart Copilot CLI in this repository or start a new Copilot cloud agent job so .github/hooks/reconc.json is loaded.",
	)
}

func installGrok(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindGrok,
		repoRoot,
		force,
		func(data []byte) bool {
			text := string(data)
			return strings.Contains(text, `"reconcManaged": true`) &&
				strings.Contains(text, "grok-pre-tool-use")
		},
		true,
		"Restart Grok Build or reload /hooks, then run /hooks-trust once for this project so .grok/hooks/reconc.json can execute.",
	)
}

func installOMP(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindOMP,
		repoRoot,
		force,
		func(data []byte) bool {
			text := string(data)
			return strings.Contains(text, "Managed by reconc. Project-local Oh My Pi policy extension.") &&
				strings.Contains(text, "omp-pre-tool-use") &&
				strings.Contains(text, "omp-stop")
		},
		false,
		"Restart OMP from this repository root so it loads .omp/extensions/reconc.ts.",
	)
}

func installPi(repoRoot string, force bool) (*InstallReport, error) {
	return installManagedPlatformFile(
		KindPi,
		repoRoot,
		force,
		func(data []byte) bool {
			text := string(data)
			return strings.Contains(text, "Managed by reconc. Project-local Pi policy extension.") &&
				strings.Contains(text, "pi-pre-tool-use") &&
				strings.Contains(text, "pi-stop")
		},
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
	if existing, err := readManagedArtifact(target); err == nil {
		action = "updated"
		if !managed(existing) {
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
	} else if !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	if writeAction, err := writeGeneratedArtifact(target, artifact.Content, artifact.Executable); err != nil {
		return nil, err
	} else if writeAction == "unchanged" {
		action = writeAction
	}
	return &InstallReport{Kind: kind, RepoRoot: root, TargetPath: artifact.TargetPath, Action: action, Executable: artifact.Executable, NextAction: nextAction}, nil
}

func isManagedGitHubCopilotConfig(data []byte) bool {
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
	expectedRoutes, ok := githubCopilotExpectedRoutes()
	if !ok {
		return false
	}
	if len(document.Hooks) != len(expectedRoutes) {
		return false
	}
	for event, route := range expectedRoutes {
		entries := document.Hooks[event]
		if len(entries) != 1 {
			return false
		}
		entry := entries[0]
		bashCommand, _ := entry["bash"].(string)
		powershellCommand, _ := entry["powershell"].(string)
		entryType, _ := entry["type"].(string)
		cwd, _ := entry["cwd"].(string)
		if entryType != "command" || cwd != "." ||
			!githubCopilotCommandHasRoute(bashCommand, route) ||
			!githubCopilotCommandHasRoute(powershellCommand, route) {
			return false
		}
	}
	return true
}

func githubCopilotExpectedRoutes() (map[string]string, bool) {
	platform, ok := PlatformForKind(KindGitHubCopilot)
	if !ok {
		return nil, false
	}
	routes := make(map[string]string, len(platform.Capabilities))
	for _, capability := range platform.Capabilities {
		for _, binding := range capability.Bindings {
			if binding.Compatibility || binding.NativeEvent == "" || binding.RuntimeEvent == "" {
				continue
			}
			if _, duplicate := routes[binding.NativeEvent]; duplicate {
				return nil, false
			}
			routes[binding.NativeEvent] = binding.RuntimeEvent
		}
	}
	return routes, len(routes) != 0
}

func githubCopilotCommandHasRoute(command, route string) bool {
	for _, field := range strings.Fields(command) {
		if strings.Trim(field, `"';`) == route {
			return true
		}
	}
	return false
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
