package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/presets"
	reconruntime "reconc.dev/reconc/internal/runtime"
)

const maxPlanBytes int64 = 4 << 20

func BuildPlan(request Request, productVersion string) (*Plan, error) {
	root, err := canonicalRepoRoot(request.RepoRoot)
	if err != nil {
		return nil, err
	}
	inspection, err := Inspect(root)
	if err != nil {
		return nil, err
	}
	selection, err := normalizeSelection(request, inspection)
	if err != nil {
		return nil, err
	}
	profile, err := profileByName(selection.Profile)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(productVersion) == "" {
		return nil, fmt.Errorf("bootstrap product version must be non-empty")
	}
	selection, err = attachHarnessPacks(selection, productVersion)
	if err != nil {
		return nil, err
	}
	artifacts, err := buildDesiredArtifacts(root, selection, productVersion)
	if err != nil {
		return nil, err
	}
	actions := make([]Action, 0, len(artifacts))
	for _, artifact := range artifacts {
		action, err := planAction(root, artifact)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Path < actions[j].Path })
	hasConflict := false
	for _, action := range actions {
		if action.State == ActionConflict {
			hasConflict = true
			break
		}
	}
	lockPath := filepath.Join(root, ".reconc", "policy.lock.json")
	_, lockErr := os.Lstat(lockPath)
	lockExists := lockErr == nil
	if lockErr != nil && !os.IsNotExist(lockErr) {
		return nil, fmt.Errorf("inspect existing policy lockfile: %w", lockErr)
	}
	issues := []string{}
	compileRequired := profile.Policy && !hasConflict && !lockExists
	if !profile.Policy && !lockExists {
		issues = append(issues, "existing bootstrap profile requires an already compiled fresh policy lockfile")
	} else if lockExists && (!hasConflict || !profile.Policy) {
		if err := reconruntime.ValidatePolicyLockfile(root); err != nil {
			issues = append(issues, "existing policy lockfile is not fresh and bootstrap will not replace it: "+err.Error())
		}
	}
	plan := &Plan{
		FormatVersion: PlanFormatVersion, ProductVersion: productVersion, RepoRoot: root,
		Selection: selection, Actions: actions, CompileRequired: compileRequired,
		BlockingIssues: issues,
	}
	digest, err := computePlanDigest(plan)
	if err != nil {
		return nil, err
	}
	plan.PlanDigest = digest
	return plan, nil
}

func WritePlan(path string, plan *Plan) (string, error) {
	data, err := encodePlan(plan)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve bootstrap plan output: %w", err)
	}
	if current, err := os.ReadFile(abs); err == nil {
		if bytes.Equal(current, data) {
			return "unchanged", nil
		}
		return "", fmt.Errorf("bootstrap plan output already exists with different content: %s", abs)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read bootstrap plan output: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("create bootstrap plan parent: %w", err)
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("create bootstrap plan output: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		closeErr := file.Close()
		removeErr := os.Remove(abs)
		return "", combineWriteFailure("write bootstrap plan output", err, closeErr, removeErr)
	}
	if err := file.Sync(); err != nil {
		closeErr := file.Close()
		removeErr := os.Remove(abs)
		return "", combineWriteFailure("sync bootstrap plan output", err, closeErr, removeErr)
	}
	if err := file.Close(); err != nil {
		removeErr := os.Remove(abs)
		return "", combineWriteFailure("close bootstrap plan output", err, nil, removeErr)
	}
	return "created", nil
}

// ReplacePlan atomically replaces only a valid existing Reconc bootstrap plan
// for the same repository. It refuses arbitrary files and cross-repository
// reuse, making stale-plan recovery explicit without a shell-specific rm step.
func ReplacePlan(path string, plan *Plan) (string, error) {
	data, err := encodePlan(plan)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve bootstrap plan output: %w", err)
	}
	if _, err := os.Lstat(abs); os.IsNotExist(err) {
		return WritePlan(abs, plan)
	} else if err != nil {
		return "", fmt.Errorf("inspect bootstrap plan output: %w", err)
	}
	current, err := LoadPlan(abs)
	if err != nil {
		return "", fmt.Errorf("refuse to replace non-plan output %s: %w", abs, err)
	}
	if current.RepoRoot != plan.RepoRoot {
		return "", fmt.Errorf("refuse to replace bootstrap plan for %s with a plan for %s", current.RepoRoot, plan.RepoRoot)
	}
	changed, err := atomicfile.WriteIfChanged(abs, data, 0o644)
	if err != nil {
		return "", fmt.Errorf("replace bootstrap plan output: %w", err)
	}
	if !changed {
		return "unchanged", nil
	}
	return "replaced", nil
}

func encodePlan(plan *Plan) ([]byte, error) {
	if err := ValidatePlan(plan); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode bootstrap plan: %w", err)
	}
	return append(data, '\n'), nil
}

func LoadPlan(path string) (*Plan, error) {
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect bootstrap plan: %w", err)
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("bootstrap plan must be a real regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bootstrap plan: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		closeErr := file.Close()
		return nil, combineWriteFailure("stat bootstrap plan", err, closeErr, nil)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("bootstrap plan is not a regular file")
	}
	if info.Size() > maxPlanBytes {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("bootstrap plan exceeds %d bytes; close: %w", maxPlanBytes, closeErr)
		}
		return nil, fmt.Errorf("bootstrap plan exceeds %d bytes", maxPlanBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxPlanBytes))
	decoder.DisallowUnknownFields()
	var plan Plan
	decodeErr := decoder.Decode(&plan)
	var extra interface{}
	extraErr := decoder.Decode(&extra)
	closeErr := file.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("decode bootstrap plan: %w", decodeErr)
	}
	if extraErr != io.EOF {
		return nil, fmt.Errorf("bootstrap plan must contain exactly one JSON document")
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close bootstrap plan: %w", closeErr)
	}
	if err := ValidatePlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func ValidatePlan(plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("bootstrap plan is nil")
	}
	if plan.FormatVersion != PlanFormatVersion {
		return fmt.Errorf("unsupported bootstrap plan format_version %q", plan.FormatVersion)
	}
	root, err := canonicalRepoRoot(plan.RepoRoot)
	if err != nil {
		return err
	}
	if root != plan.RepoRoot {
		return fmt.Errorf("bootstrap plan repo_root is not canonical: %s", plan.RepoRoot)
	}
	if strings.TrimSpace(plan.ProductVersion) == "" {
		return fmt.Errorf("bootstrap plan product_version must be non-empty")
	}
	profile, err := profileByName(plan.Selection.Profile)
	if err != nil {
		return err
	}
	if len(plan.Selection.Packs) < len(profile.DefaultPacks) {
		return fmt.Errorf("bootstrap plan selection omits profile default packs")
	}
	for index, name := range profile.DefaultPacks {
		if plan.Selection.Packs[index] != name {
			return fmt.Errorf("bootstrap plan selection must preserve profile default pack %q at index %d", name, index)
		}
	}
	if err := presets.ValidateSelection(plan.Selection.Packs); err != nil {
		return fmt.Errorf("validate bootstrap plan packs: %w", err)
	}
	if err := validateHarnessPackSelections(plan.Selection); err != nil {
		return err
	}
	for index, kind := range plan.Selection.Hooks {
		if !containsString(hooks.BootstrapKinds(), kind) {
			return fmt.Errorf("bootstrap plan contains unsupported hook kind %q", kind)
		}
		if index > 0 && plan.Selection.Hooks[index-1] >= kind {
			return fmt.Errorf("bootstrap plan hook kinds must be uniquely sorted")
		}
	}
	if profile.Wrapper && plan.Selection.TrustExistingWrapper {
		return fmt.Errorf("bootstrap profiles that own the wrapper cannot trust an unmanaged existing wrapper")
	}
	if binary := plan.Selection.Binary; binary != nil {
		if !filepath.IsAbs(binary.SourcePath) || !validSHA256(binary.SHA256) {
			return fmt.Errorf("bootstrap plan binary source and checksum are invalid")
		}
		if err := validatePlatform(binary.OS, binary.Arch); err != nil {
			return err
		}
	}
	for index, action := range plan.Actions {
		if action.Path == "" || !validSHA256(action.DesiredSHA256) || action.Component == "" {
			return fmt.Errorf("bootstrap plan action %d is incomplete", index)
		}
		if _, err := safeBootstrapTarget(root, action.Path); err != nil {
			return err
		}
		if action.Mode != 0o644 && action.Mode != 0o755 {
			return fmt.Errorf("bootstrap plan action %s has unsupported mode %04o", action.Path, action.Mode)
		}
		if index > 0 && plan.Actions[index-1].Path >= action.Path {
			return fmt.Errorf("bootstrap plan actions must be uniquely sorted by path")
		}
		if action.State != ActionCreate && action.State != ActionUnchanged && action.State != ActionConflict {
			return fmt.Errorf("bootstrap plan action %s has invalid state %q", action.Path, action.State)
		}
		switch action.State {
		case ActionCreate:
			if action.ExistingKind != "absent" || action.ExistingSHA256 != "" || action.ExistingMode != 0 || action.CandidatePath != "" {
				return fmt.Errorf("bootstrap create action %s has contradictory existing state", action.Path)
			}
		case ActionUnchanged:
			if action.ExistingKind != "file" || action.ExistingSHA256 != action.DesiredSHA256 || action.CandidatePath != "" {
				return fmt.Errorf("bootstrap unchanged action %s has contradictory existing state", action.Path)
			}
		case ActionConflict:
			expectedCandidate := action.Path + ".reconc-candidate-" + action.DesiredSHA256[:12]
			if action.ExistingKind == "absent" || action.CandidatePath != expectedCandidate {
				return fmt.Errorf("bootstrap conflict action %s has invalid candidate state", action.Path)
			}
			if _, err := safeBootstrapTarget(root, action.CandidatePath); err != nil {
				return err
			}
		}
	}
	digest, err := computePlanDigest(plan)
	if err != nil {
		return err
	}
	if plan.PlanDigest != digest {
		return fmt.Errorf("bootstrap plan digest mismatch: expected %s, got %s", digest, plan.PlanDigest)
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func normalizeSelection(request Request, inspection *Inspection) (Selection, error) {
	profileName := request.Profile
	if profileName == "" {
		profileName = ProfileMinimal
	}
	profile, err := profileByName(profileName)
	if err != nil {
		return Selection{}, err
	}
	if !profile.Policy && len(request.Packs) > 0 {
		return Selection{}, fmt.Errorf("profile %q does not own policy and cannot select packs", profile.Name)
	}
	packs := dedupePreservingOrder(append(append([]string{}, profile.DefaultPacks...), request.Packs...))
	if err := presets.ValidateSelection(packs); err != nil {
		return Selection{}, fmt.Errorf("validate bootstrap pack selection: %w", err)
	}
	stackSet := map[string]bool{}
	for _, stack := range inspection.DetectedStacks {
		stackSet[stack] = true
	}
	for _, name := range request.Packs {
		metadata, err := presets.Inspect(name)
		if err != nil {
			return Selection{}, err
		}
		if metadata.Manifest == nil || containsString(metadata.Manifest.Stacks, "*") {
			continue
		}
		applicable := false
		for _, stack := range metadata.Manifest.Stacks {
			if stackSet[stack] {
				applicable = true
				break
			}
		}
		if !applicable {
			return Selection{}, fmt.Errorf("pack %q is not applicable to detected stacks %v; inspect first or select it after the stack exists", name, inspection.DetectedStacks)
		}
	}
	hookKinds, err := normalizeHooks(request.Hooks, inspection.RepoRoot)
	if err != nil {
		return Selection{}, err
	}
	if request.Binary != nil {
		validated, err := BinarySelectionFor(request.Binary.SourcePath, request.Binary.SHA256, request.Binary.OS, request.Binary.Arch)
		if err != nil {
			return Selection{}, err
		}
		request.Binary = validated
	}
	return Selection{
		Profile: profile.Name, Packs: packs, HarnessPacks: []HarnessPackSelection{},
		Hooks: hookKinds, Binary: request.Binary,
		TrustExistingWrapper: request.TrustExistingWrapper,
	}, nil
}

func normalizeHooks(requested []string, root string) ([]string, error) {
	kinds := []string{}
	for _, raw := range requested {
		kind := strings.TrimSpace(raw)
		if kind == "all" {
			kinds = append(kinds, hooks.BootstrapKinds()...)
			continue
		}
		if !containsString(hooks.BootstrapKinds(), kind) {
			return nil, fmt.Errorf("unknown bootstrap hook kind %q; supported: %s", kind, strings.Join(hooks.BootstrapKinds(), ", "))
		}
		kinds = append(kinds, kind)
	}
	kinds = dedupePreservingOrder(kinds)
	sort.Strings(kinds)
	if containsString(kinds, hooks.KindGitPreCommit) {
		info, err := os.Stat(filepath.Join(root, ".git"))
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("git-pre-commit is not applicable: %s has no .git directory", root)
		}
	}
	return kinds, nil
}

func profileByName(name ProfileName) (Profile, error) {
	for _, profile := range Profiles() {
		if profile.Name == name {
			return profile, nil
		}
	}
	return Profile{}, fmt.Errorf("unknown bootstrap profile %q; supported: advanced, existing, governed, minimal", name)
}

func planAction(root string, artifact desiredArtifact) (Action, error) {
	desiredSHA, err := artifactSHA256(artifact)
	if err != nil {
		return Action{}, err
	}
	action := Action{
		Component: artifact.component, Path: artifact.path, Mode: artifact.mode,
		DesiredSHA256: desiredSHA, ExistingKind: "absent", State: ActionCreate,
	}
	target := filepath.Join(root, filepath.FromSlash(artifact.path))
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return action, nil
	}
	if err != nil {
		return Action{}, fmt.Errorf("inspect bootstrap target %s: %w", artifact.path, err)
	}
	action.ExistingMode = uint32(info.Mode().Perm())
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		action.ExistingKind = "symlink"
	case info.IsDir():
		action.ExistingKind = "directory"
	case info.Mode().IsRegular():
		action.ExistingKind = "file"
		digest, err := fileSHA256(target)
		if err != nil {
			return Action{}, err
		}
		action.ExistingSHA256 = digest
		if digest == desiredSHA && modeSatisfies(info.Mode(), artifact.mode) {
			action.State = ActionUnchanged
			return action, nil
		}
	default:
		action.ExistingKind = "special"
	}
	action.State = ActionConflict
	action.CandidatePath = artifact.path + ".reconc-candidate-" + desiredSHA[:12]
	return action, nil
}

func artifactSHA256(artifact desiredArtifact) (string, error) {
	if artifact.sourcePath != "" {
		return fileSHA256(artifact.sourcePath)
	}
	return bytesSHA256(artifact.content), nil
}

func computePlanDigest(plan *Plan) (string, error) {
	copy := *plan
	copy.PlanDigest = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("encode bootstrap plan digest: %w", err)
	}
	return bytesSHA256(data), nil
}

func dedupePreservingOrder(values []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func combineWriteFailure(context string, primary, closeErr, removeErr error) error {
	parts := []string{context + ": " + primary.Error()}
	if closeErr != nil {
		parts = append(parts, "close: "+closeErr.Error())
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		parts = append(parts, "cleanup: "+removeErr.Error())
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}
