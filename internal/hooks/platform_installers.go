package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
)

func installDevinCLI(repoRoot string, force bool) (*InstallReport, error) {
	root, err := existingRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(root, DevinHooksPath)
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
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	mergedHooks := generatedHooks
	var mergeDiff MergeDiff
	if len(existing) != 0 && strings.TrimSpace(string(existing)) != "{}" {
		action = "updated"
		var existingHooks map[string]interface{}
		if err := json.Unmarshal(existing, &existingHooks); err != nil {
			if !force {
				return nil, &rerrors.PolicySourceError{Message: target + " is not valid JSON; pass --force to overwrite with a fresh reconc config", Cause: err}
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
		"Restart Kilo Code in this repository so it reloads .kilo/plugin/reconc.js; KILO_PURE must be unset.",
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
		"Restart Grok Build or reload /hooks, then run /hooks-trust once for this project so .grok/hooks/reconc.json can execute.",
	)
}

func installManagedPlatformFile(kind, repoRoot string, force bool, managed func([]byte) bool, nextAction string) (*InstallReport, error) {
	root, err := existingRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	artifact, err := Generate(kind)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(root, artifact.TargetPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}
	action := "created"
	if existing, err := os.ReadFile(target); err == nil {
		action = "updated"
		if !force && !managed(existing) {
			return nil, &rerrors.PolicySourceError{Message: artifact.TargetPath + " exists and is not reconc-managed; pass --force to overwrite"}
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

func existingRepoRoot(repoRoot string) (string, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", &rerrors.PolicySourceError{Message: "resolve repo path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root, Cause: err}
	}
	return root, nil
}
