package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"reconc.dev/reconc/internal/atomicfile"
	rerrors "reconc.dev/reconc/internal/errors"
)

// UninstallReport is the deterministic result of removing one platform hook.
// Shared wrappers remain because another platform may use them; bootstrap
// removal owns wrapper deletion through its install receipt.
type UninstallReport struct {
	Kind             string `json:"kind"`
	RepoRoot         string `json:"repo_root"`
	TargetPath       string `json:"target_path"`
	Action           string `json:"action"` // "removed" | "updated" | "unchanged" | "absent"
	RemovedEntries   int    `json:"removed_entries"`
	ActivationAction string `json:"activation_action,omitempty"`
	WrapperPath      string `json:"wrapper_path,omitempty"`
	WrapperAction    string `json:"wrapper_action,omitempty"`
	NextAction       string `json:"next_action"`
}

type uninstallMutation struct {
	path    string
	display string
	before  []byte
	after   []byte
	mode    os.FileMode
	remove  bool
}

// Uninstall removes only generator-exact managed artifacts or canonical
// Reconc entries. Modified Reconc-looking entries fail closed, and every
// mutation is preflighted before the first write.
func Uninstall(kind, repoRoot string) (*UninstallReport, error) {
	definition, ok := lookupPlatformDefinition(kind)
	if !ok {
		return nil, &rerrors.PolicySourceError{
			Message: fmt.Sprintf("unknown uninstallable hook kind: %q (supported: %v)", kind, InstallableKinds()),
		}
	}
	if definition.Kind == KindKimiCode {
		return uninstallKimiCode()
	}
	root, err := existingRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	mutations, targetPath, action, removedEntries, err := planPlatformUninstall(root, definition)
	if err != nil {
		return nil, err
	}
	activationAction := ""
	if kind == KindCodex {
		activationMutation, state, activationErr := planCodexActivationRemoval(root)
		if activationErr != nil {
			return nil, activationErr
		}
		activationAction = state
		if activationMutation != nil {
			mutations = append(mutations, *activationMutation)
			if action == "absent" || action == "unchanged" {
				action = "updated"
			}
		}
	}
	if err := applyUninstallMutations(mutations); err != nil {
		return nil, err
	}
	report := &UninstallReport{
		Kind: kind, RepoRoot: root, TargetPath: targetPath, Action: action,
		RemovedEntries: removedEntries, ActivationAction: activationAction,
		NextAction: "Run `reconc hook status " + quoteStatusArgument(root) + "` to verify the platform is absent while unrelated hooks remain.",
	}
	if definition.Activation.RequiresWrapper {
		report.WrapperPath = WrapperPath
		report.WrapperAction = "preserved-shared"
	}
	return report, nil
}

func planPlatformUninstall(root string, definition platformDefinition) ([]uninstallMutation, string, string, int, error) {
	target, display, err := uninstallTarget(root, definition)
	if err != nil {
		return nil, definition.TargetPath, "", 0, err
	}
	data, err := readManagedArtifact(target)
	if os.IsNotExist(err) {
		return nil, display, "absent", 0, nil
	}
	if err != nil {
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: "read " + display, Cause: err}
	}
	info, err := os.Lstat(target)
	if err != nil {
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: "inspect " + display, Cause: err}
	}

	switch definition.InstallMode {
	case InstallExecutable, InstallPlugin, InstallManagedJSON:
		artifact, generateErr := Generate(definition.Kind)
		if generateErr != nil {
			return nil, display, "", 0, generateErr
		}
		if string(data) != artifact.Content {
			return nil, display, "", 0, &rerrors.PolicySourceError{Message: display + " differs from the current Reconc generator; refusing to delete drifted content"}
		}
		mutation := uninstallMutation{path: target, display: display, before: data, mode: info.Mode().Perm(), remove: true}
		return []uninstallMutation{mutation}, display, "removed", 1, nil
	case InstallNestedJSON:
		return planNestedJSONUninstall(target, display, data, info.Mode().Perm(), definition.Kind)
	case InstallFlatJSON:
		return planFlatJSONUninstall(target, display, data, info.Mode().Perm(), definition.Kind)
	case InstallOwnedJSON:
		return planOwnedJSONUninstall(target, display, data, info.Mode().Perm(), definition.Kind)
	default:
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: "unsupported uninstall mode for " + definition.Kind}
	}
}

func uninstallTarget(root string, definition platformDefinition) (string, string, error) {
	if definition.Activation.Mode == ActivationGitPath {
		target, display, err := activeGitPreCommitPath(root)
		if err != nil {
			return "", definition.TargetPath, &rerrors.PolicySourceError{Message: "cannot resolve the active Git hooks path", Cause: err}
		}
		owned, err := gitHookTargetIsRepositoryOwned(root, target)
		if err != nil {
			return "", display, &rerrors.PolicySourceError{Message: "validate active Git hooks path", Cause: err}
		}
		if !owned {
			return "", display, &rerrors.PolicySourceError{Message: "active Git hooks path is outside the repository and its Git common directory; refusing removal"}
		}
		return target, display, nil
	}
	target := filepath.Join(root, filepath.FromSlash(definition.TargetPath))
	if err := requireManagedTargetWithin(root, target); err != nil {
		return "", definition.TargetPath, err
	}
	return target, definition.TargetPath, nil
}

func planNestedJSONUninstall(target, display string, data []byte, mode os.FileMode, kind string) ([]uninstallMutation, string, string, int, error) {
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: display + " is not valid JSON; refusing removal", Cause: err}
	}
	generated, err := generatedJSONDocument(kind)
	if err != nil {
		return nil, display, "", 0, err
	}
	removed, err := removeCanonicalReconcHooks(document, generated)
	if err != nil {
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: display + ": " + err.Error()}
	}
	return jsonUninstallMutation(target, display, data, mode, document, removed)
}

func planFlatJSONUninstall(target, display string, data []byte, mode os.FileMode, kind string) ([]uninstallMutation, string, string, int, error) {
	var hooksDocument map[string]interface{}
	if err := json.Unmarshal(data, &hooksDocument); err != nil {
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: display + " is not valid JSON; refusing removal", Cause: err}
	}
	generatedHooks, err := generatedJSONDocument(kind)
	if err != nil {
		return nil, display, "", 0, err
	}
	document := map[string]interface{}{"hooks": hooksDocument}
	generated := map[string]interface{}{"hooks": generatedHooks}
	removed, err := removeCanonicalReconcHooks(document, generated)
	if err != nil {
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: display + ": " + err.Error()}
	}
	remaining, _ := document["hooks"].(map[string]interface{})
	if remaining == nil {
		remaining = map[string]interface{}{}
	}
	return jsonUninstallMutation(target, display, data, mode, remaining, removed)
}

func planOwnedJSONUninstall(target, display string, data []byte, mode os.FileMode, kind string) ([]uninstallMutation, string, string, int, error) {
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: display + " is not valid JSON; refusing removal", Cause: err}
	}
	value, exists := document["reconc"]
	if !exists {
		return nil, display, "unchanged", 0, nil
	}
	generated, err := generatedJSONDocument(kind)
	if err != nil {
		return nil, display, "", 0, err
	}
	if !reflect.DeepEqual(value, generated["reconc"]) {
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: display + " has a modified or foreign top-level reconc entry; refusing removal"}
	}
	delete(document, "reconc")
	return jsonUninstallMutation(target, display, data, mode, document, 1)
}

func generatedJSONDocument(kind string) (map[string]interface{}, error) {
	artifact, err := Generate(kind)
	if err != nil {
		return nil, err
	}
	var document map[string]interface{}
	if err := json.Unmarshal([]byte(artifact.Content), &document); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "internal generated hook is not valid JSON", Cause: err}
	}
	return document, nil
}

func jsonUninstallMutation(target, display string, before []byte, mode os.FileMode, document interface{}, removed int) ([]uninstallMutation, string, string, int, error) {
	if removed == 0 {
		return nil, display, "unchanged", 0, nil
	}
	after, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, display, "", 0, &rerrors.PolicySourceError{Message: "marshal preserved hook config", Cause: err}
	}
	after = append(after, '\n')
	mutation := uninstallMutation{path: target, display: display, before: before, after: after, mode: mode}
	return []uninstallMutation{mutation}, display, "updated", removed, nil
}

func planCodexActivationRemoval(root string) (*uninstallMutation, string, error) {
	const relative = ".codex/config.toml"
	path := filepath.Join(root, filepath.FromSlash(relative))
	data, err := readManagedArtifact(path)
	if os.IsNotExist(err) {
		return nil, "absent", nil
	}
	if err != nil {
		return nil, "", &rerrors.PolicySourceError{Message: "read " + relative, Cause: err}
	}
	updated, removed, err := RemoveCodexActivation(string(data))
	if err != nil {
		return nil, "", &rerrors.PolicySourceError{Message: relative + ": " + err.Error()}
	}
	if !removed {
		return nil, "preserved-unmanaged", nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "", err
	}
	mutation := &uninstallMutation{path: path, display: relative, before: data, after: []byte(updated), mode: info.Mode().Perm()}
	return mutation, "removed-managed-block", nil
}

func applyUninstallMutations(mutations []uninstallMutation) error {
	for _, mutation := range mutations {
		current, err := readManagedArtifact(mutation.path)
		if err != nil {
			return &rerrors.PolicySourceError{Message: "revalidate " + mutation.display, Cause: err}
		}
		if !bytes.Equal(current, mutation.before) {
			return &rerrors.PolicySourceError{Message: mutation.display + " changed after uninstall preflight; retry"}
		}
	}
	applied := []uninstallMutation{}
	for _, mutation := range mutations {
		var err error
		if mutation.remove {
			err = os.Remove(mutation.path)
			if err == nil {
				err = syncManagedArtifactParent(mutation.path)
			}
		} else {
			_, err = atomicfile.WriteIfChanged(mutation.path, mutation.after, mutation.mode)
		}
		if err == nil {
			applied = append(applied, mutation)
			continue
		}
		rollbackErr := rollbackUninstallMutations(applied)
		return &rerrors.PolicySourceError{Message: "apply hook uninstall mutation " + mutation.display, Cause: errors.Join(err, rollbackErr)}
	}
	return nil
}

func rollbackUninstallMutations(applied []uninstallMutation) error {
	var rollbackErr error
	for index := len(applied) - 1; index >= 0; index-- {
		mutation := applied[index]
		if mutation.remove {
			if _, err := os.Lstat(mutation.path); err == nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("refuse to overwrite path that appeared during hook uninstall rollback: %s", mutation.display))
				continue
			} else if !os.IsNotExist(err) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("inspect hook uninstall rollback target %s: %w", mutation.display, err))
				continue
			}
		} else {
			current, err := readManagedArtifact(mutation.path)
			if err != nil || !bytes.Equal(current, mutation.after) {
				if err == nil {
					err = fmt.Errorf("content changed after hook uninstall mutation")
				}
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("refuse to overwrite hook uninstall rollback target %s: %w", mutation.display, err))
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(mutation.path), 0o755); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		if _, err := atomicfile.WriteIfChanged(mutation.path, mutation.before, mutation.mode); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	return rollbackErr
}
