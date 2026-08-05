// Package hooks generates, installs, and inspects platform-specific hook
// artifacts that wire reconc into git and supported agent runtimes.
//
// Generators are pure functions (return a string + metadata).
// Installers are the only entry points that touch the filesystem and
// they refuse to clobber existing hooks unless Force=true.
//
// Generated commands use reconc-specific paths and command names.
package hooks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	rerrors "reconc.dev/reconc/internal/errors"
)

// Hook artifact paths.
const (
	GitPreCommitPath          = ".git/hooks/pre-commit"
	GitPreCommitScaffoldPath  = ".githooks/pre-commit"
	ClaudeCodeSettingsPath    = ".claude/settings.json"
	CodexHooksPath            = ".codex/hooks.json"
	GitHubCopilotHooksPath    = ".github/hooks/reconc.json"
	CursorHooksPath           = ".cursor/hooks.json"
	OpenCodePluginPath        = ".opencode/plugins/reconc.js"
	DevinHooksPath            = ".devin/hooks.v1.json"
	AntigravityHooksPath      = ".agents/hooks.json"
	KiloPluginPath            = ".kilo/plugin/reconc.js"
	GrokHooksPath             = ".grok/hooks/reconc.json"
	OMPExtensionPath          = ".omp/extensions/reconc.ts"
	PiExtensionPath           = ".pi/extensions/reconc.ts"
	KimiCodeConfigDisplayPath = "~/.kimi-code/config.toml"
)

// Supported hook kinds.
const (
	KindGitPreCommit  = "git-pre-commit"
	KindClaudeCode    = "claude-code"
	KindCodex         = "codex"
	KindGitHubCopilot = "github-copilot"
	KindCursor        = "cursor"
	KindOpenCode      = "opencode"
	KindDevinCLI      = "devin-cli"
	KindAntigravity   = "antigravity"
	KindKilo          = "kilo"
	KindGrok          = "grok"
	KindOMP           = "omp"
	KindPi            = "pi"
	KindKimiCode      = "kimi-code"
)

// SupportedKinds returns every kind reconc hook generate can produce.
func SupportedKinds() []string {
	return platformKinds(false)
}

// InstallableKinds returns the kinds that reconc hook install can write
// directly. JSON configs are merged non-destructively; an unchanged managed
// git hook is reused without another filesystem write.
func InstallableKinds() []string {
	return platformKinds(false)
}

// HasManagedGrokHook reports whether the native Grok artifact is exactly the
// current generated file. Compatibility-route dedup must never trust a stale
// or merely self-labelled hook.
func HasManagedGrokHook(repoRoot string) bool {
	data, err := readManagedArtifact(filepath.Join(repoRoot, filepath.FromSlash(GrokHooksPath)))
	if err != nil {
		return false
	}
	artifact, err := generateGrok()
	return err == nil && string(data) == artifact.Content
}

// Artifact is one generated hook script + enough context to render it.
type Artifact struct {
	Kind       string `json:"kind"`
	TargetPath string `json:"target_path"`
	Executable bool   `json:"executable"`
	Content    string `json:"content"`
}

// InstallReport is the deterministic outcome of an install call.
type InstallReport struct {
	Kind       string `json:"kind"`
	RepoRoot   string `json:"repo_root"`
	TargetPath string `json:"target_path"`
	Action     string `json:"action"` // "created" | "updated" | "unchanged"
	Executable bool   `json:"executable"`
	NextAction string `json:"next_action"`
	// DroppedUserEdits lists any hooks-entry strings classified as
	// user-modified reconc entries that were replaced during a JSON
	// merge install. Callers typically surface these as stderr
	// warnings so users know their edits were overwritten.
	DroppedUserEdits []string `json:"dropped_user_edits,omitempty"`
	// BackupPath is set when --force replaced a malformed existing
	// config; the original bytes are preserved at this absolute path
	// instead of being discarded.
	BackupPath string `json:"backup_path,omitempty"`
	// WrapperAction reports whether a wrapper-dependent platform created,
	// updated, or reused the shared repository-local launcher.
	WrapperPath   string `json:"wrapper_path,omitempty"`
	WrapperAction string `json:"wrapper_action,omitempty"`
	// ActivationPath and ActivationAction report explicit host-discovery
	// configuration managed alongside the hook artifact.
	ActivationPath   string `json:"activation_path,omitempty"`
	ActivationAction string `json:"activation_action,omitempty"`
	// Partial is true when the wrapper is ready but the platform target failed.
	Partial bool `json:"partial,omitempty"`
}

// ScaffoldSyncReport is the deterministic result of syncing generated
// hook artifacts into a repo-root scaffold folder. Unlike Install,
// this writes only source-controlled scaffold artifacts and never
// touches a live .git/hooks directory.
type ScaffoldSyncReport struct {
	ScaffoldRoot string                   `json:"scaffold_root"`
	Artifacts    []ScaffoldArtifactReport `json:"artifacts"`
}

// ScaffoldArtifactReport describes one generated scaffold artifact.
type ScaffoldArtifactReport struct {
	Kind       string `json:"kind"`
	TargetPath string `json:"target_path"`
	Action     string `json:"action"` // "created" | "updated" | "unchanged"
	Executable bool   `json:"executable"`
}

// Generate dispatches through the platform registry.
func Generate(kind string) (*Artifact, error) {
	definition, ok := lookupPlatformDefinition(kind)
	if !ok {
		return nil, &rerrors.PolicySourceError{
			Message: fmt.Sprintf("unknown hook kind: %q (supported: %v)", kind, SupportedKinds()),
		}
	}
	if err := validatePlatform(definition.Platform); err != nil {
		return nil, err
	}
	switch definition.generator {
	case generatorGitPreCommit:
		return generateGitPreCommit(), nil
	case generatorClaudeCode:
		return generateClaudeCode()
	case generatorCodex:
		return generateCodex()
	case generatorGitHubCopilot:
		return generateGitHubCopilot()
	case generatorCursor:
		return generateCursor()
	case generatorOpenCode:
		return generateOpenCodeThin()
	case generatorDevinCLI:
		return generateDevinCLI()
	case generatorAntigravity:
		return generateAntigravity()
	case generatorKilo:
		return generateKilo()
	case generatorGrok:
		return generateGrok()
	case generatorOMP:
		return generateOMP()
	case generatorPi:
		return generatePi()
	case generatorKimiCode:
		return generateKimiCode()
	}
	return nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("hook kind %q has no generator", kind)}
}

// Install writes an installable hook into the repo. Refuses to
// overwrite an existing hook unless force is true.
//
// Supported kinds are installable:
//   - git-pre-commit: creates .git/hooks/pre-commit, reuses an identical
//     managed hook, and refuses to clobber different content unless --force
//   - claude-code: merges reconc hook entries into .claude/settings.json
//     non-destructively. Idempotent: reconc-owned hook entries are
//     identified by their repo-local wrapper/runtime signature and replaced
//     wholesale on each install; non-reconc keys are preserved.
//   - codex: same merge strategy for .codex/hooks.json.
//   - cursor: same merge strategy for .cursor/hooks.json.
//   - opencode: writes .opencode/plugins/reconc.js as a project-local
//     plugin, refusing to clobber non-reconc plugin content unless
//     --force is set.
//   - JSON platforms merge reconc-owned entries without replacing unrelated
//     hooks; plugin platforms replace only reconc-managed plugin files.
func Install(kind, repoRoot string, force bool) (*InstallReport, error) {
	definition, ok := lookupPlatformDefinition(kind)
	if !ok {
		return nil, &rerrors.PolicySourceError{
			Message: fmt.Sprintf("unknown installable hook kind: %q (installable: %v)", kind, InstallableKinds()),
		}
	}
	if definition.Kind == KindKimiCode {
		return installKimiCode(force)
	}
	root, err := existingRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	var activationPlan *codexActivationPlan
	if definition.Kind == KindCodex {
		activationPlan, err = planCodexActivation(root, force)
		if err != nil {
			return nil, err
		}
	}
	wrapperAction := ""
	if definition.Activation.RequiresWrapper {
		wrapperAction, err = ensureWrapper(root, force)
		if err != nil {
			return nil, err
		}
	}
	report, installErr := installPlatform(definition, root, force)
	if installErr != nil {
		if definition.Activation.RequiresWrapper {
			return &InstallReport{
				Kind: kind, RepoRoot: root, TargetPath: definition.TargetPath, Action: "not-installed",
				WrapperPath: WrapperPath, WrapperAction: wrapperAction, Partial: true,
				NextAction: "Resolve the target error, then rerun `reconc hook install " + kind + " " + root + "`.",
			}, installErr
		}
		return nil, installErr
	}
	if definition.Activation.RequiresWrapper {
		report.WrapperPath = WrapperPath
		report.WrapperAction = wrapperAction
	}
	if activationPlan != nil {
		report.ActivationPath = codexActivationPath
		report.ActivationAction, err = applyCodexActivation(activationPlan)
		if err != nil {
			report.Partial = true
			report.NextAction = "Resolve the activation error, then rerun `reconc hook install " + kind + " " + root + "`."
			return report, err
		}
	}
	return report, nil
}

func installPlatform(definition platformDefinition, repoRoot string, force bool) (*InstallReport, error) {
	switch definition.InstallMode {
	case InstallExecutable:
		return installGitPreCommit(repoRoot, force)
	case InstallNestedJSON:
		return installJSONHooks(definition.Kind, definition.TargetPath, repoRoot, force)
	case InstallFlatJSON:
		return installDevinCLI(repoRoot, force)
	case InstallOwnedJSON:
		return installAntigravity(repoRoot, force)
	case InstallPlugin:
		switch definition.Kind {
		case KindOpenCode:
			return installOpenCode(repoRoot, force)
		case KindOMP:
			return installOMP(repoRoot, force)
		case KindPi:
			return installPi(repoRoot, force)
		}
		return installKilo(repoRoot, force)
	case InstallManagedJSON:
		switch definition.Kind {
		case KindGitHubCopilot:
			return installGitHubCopilot(repoRoot, force)
		case KindGrok:
			return installGrok(repoRoot, force)
		}
	}
	return nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("hook kind %q has no installer", definition.Kind)}
}

func ensureWrapper(root string, force bool) (string, error) {
	artifact := GenerateWrapper()
	target := filepath.Join(root, filepath.FromSlash(artifact.TargetPath))
	if err := requireManagedTargetWithin(root, target); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", &rerrors.PolicySourceError{Message: "create wrapper parent directory", Cause: err}
	}
	if existing, err := readManagedArtifact(target); err == nil {
		managed := strings.Contains(string(existing), "# Managed by Reconc. Repo-local agent hook wrapper.")
		if string(existing) != artifact.Content && !managed && !force {
			return "", &rerrors.PolicySourceError{Message: WrapperPath + " exists and is not reconc-managed; pass --force to overwrite"}
		}
	} else if !os.IsNotExist(err) {
		return "", &rerrors.PolicySourceError{Message: "read " + WrapperPath, Cause: err}
	}
	action, err := writeGeneratedArtifact(target, artifact.Content, true)
	if err != nil {
		return "", err
	}
	if err := ensureWrapperTarget(root, force); err != nil {
		return "", err
	}
	return action, nil
}

// ScaffoldKinds returns every generated hook artifact that belongs in
// repo-root-scaffold. Git pre-commit is mapped to .githooks/pre-commit
// because .git/hooks is clone-local and cannot be source-controlled.
func ScaffoldKinds() []string {
	return BootstrapKinds()
}

// GenerateScaffoldArtifact returns the generated artifact at its
// source-controlled scaffold path.
func GenerateScaffoldArtifact(kind string) (*Artifact, error) {
	artifact, err := Generate(kind)
	if err != nil {
		return nil, err
	}
	if kind == KindGitPreCommit {
		copy := *artifact
		copy.TargetPath = GitPreCommitScaffoldPath
		return &copy, nil
	}
	return artifact, nil
}

// SyncRepoRootScaffold writes all generated hook artifacts into a
// repo-root-scaffold directory. It is intentionally generator-driven:
// no root repo or source-specific harness is used as a source of truth.
func SyncRepoRootScaffold(scaffoldRoot string) (*ScaffoldSyncReport, error) {
	root, err := filepath.Abs(scaffoldRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve scaffold path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "scaffold path does not exist: " + root, Cause: err}
	}
	if !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "scaffold path is not a directory: " + root}
	}

	type plannedScaffoldArtifact struct {
		artifact *Artifact
		target   string
	}
	planned := make([]plannedScaffoldArtifact, 0, len(ScaffoldKinds()))
	for _, kind := range ScaffoldKinds() {
		artifact, err := GenerateScaffoldArtifact(kind)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(root, filepath.FromSlash(artifact.TargetPath))
		if err := requireManagedTargetWithin(root, target); err != nil {
			return nil, err
		}
		planned = append(planned, plannedScaffoldArtifact{artifact: artifact, target: target})
	}
	report := &ScaffoldSyncReport{
		ScaffoldRoot: root,
		Artifacts:    make([]ScaffoldArtifactReport, 0, len(planned)),
	}
	for _, item := range planned {
		artifact := item.artifact
		target := item.target
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
		}
		action, err := writeGeneratedArtifact(target, artifact.Content, artifact.Executable)
		if err != nil {
			return nil, err
		}
		report.Artifacts = append(report.Artifacts, ScaffoldArtifactReport{
			Kind:       artifact.Kind,
			TargetPath: artifact.TargetPath,
			Action:     action,
			Executable: artifact.Executable,
		})
	}
	return report, nil
}

func installGitPreCommit(repoRoot string, force bool) (*InstallReport, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve repo path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "repo path does not exist: " + root, Cause: err}
	}
	if !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root}
	}

	target, displayPath, err := activeGitPreCommitPath(root)
	if err != nil {
		return nil, &rerrors.PolicySourceError{
			Message: "cannot resolve the active Git hooks path; run `git init` before installing the pre-commit hook",
			Cause:   err,
		}
	}
	owned, err := gitHookTargetIsRepositoryOwned(root, target)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "validate active Git hooks path", Cause: err}
	}
	if !owned {
		return nil, &rerrors.PolicySourceError{Message: "active Git hooks path is outside the repository and its Git common directory: " + target + "; refusing to modify a shared hooks directory"}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create active Git hooks directory", Cause: err}
	}

	artifact := generateGitPreCommit()
	action := "created"
	if info, statErr := os.Lstat(target); statErr == nil {
		if !info.Mode().IsRegular() {
			return nil, &rerrors.PolicySourceError{Message: displayPath + " is not a regular file"}
		}
		if info.Size() > 1<<20 {
			return nil, &rerrors.PolicySourceError{Message: displayPath + " exceeds the 1 MiB managed-hook limit"}
		}
		existing, readErr := readManagedArtifact(target)
		if readErr != nil {
			return nil, &rerrors.PolicySourceError{Message: "read " + displayPath, Cause: readErr}
		}
		managed := strings.HasPrefix(string(existing), "#!/bin/sh\n# Managed by `reconc hook install git-pre-commit`.\n")
		if !force && !managed && string(existing) != artifact.Content {
			return nil, &rerrors.PolicySourceError{
				Message: displayPath + " already contains a foreign hook; pass --force to overwrite",
			}
		}
		action = "updated"
	} else if !os.IsNotExist(statErr) {
		return nil, &rerrors.PolicySourceError{Message: "inspect " + displayPath, Cause: statErr}
	}
	if writeAction, writeErr := writeGeneratedArtifact(target, artifact.Content, true); writeErr != nil {
		return nil, writeErr
	} else if writeAction == "unchanged" {
		action = "unchanged"
	} else if action == "created" {
		action = writeAction
	}

	return &InstallReport{
		Kind:       KindGitPreCommit,
		RepoRoot:   root,
		TargetPath: displayPath,
		Action:     action,
		Executable: true,
		NextAction: "Stage a change and run `git commit` to verify the hook fires; use `git commit --no-verify` to bypass it for a single commit.",
	}, nil
}

func writeGeneratedArtifact(target, content string, executable bool) (string, error) {
	perm := os.FileMode(0o644)
	if executable {
		perm = 0o755
	}
	action := "created"
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return "", &rerrors.PolicySourceError{Message: target + " is not a regular file"}
		}
		if info.Size() > maxManagedArtifactBytes {
			return "", &rerrors.PolicySourceError{Message: fmt.Sprintf("%s exceeds the %d-byte managed-artifact limit", target, maxManagedArtifactBytes)}
		}
		action = "updated"
	} else if !os.IsNotExist(err) {
		return "", &rerrors.PolicySourceError{Message: "inspect " + target, Cause: err}
	}
	changed, err := atomicfile.WriteIfChanged(target, []byte(content), perm)
	if err != nil {
		return "", &rerrors.PolicySourceError{Message: "write " + target, Cause: err}
	}
	if !changed {
		return "unchanged", nil
	}
	return action, nil
}

// requireManagedTargetWithin prevents a repository-controlled symlinked
// parent (for example .claude -> ~/.claude) from redirecting an install or
// scaffold sync outside the caller-owned root.
func requireManagedTargetWithin(root, target string) error {
	owned, err := resolvedPathWithinDirectory(root, target)
	if err != nil {
		return &rerrors.PolicySourceError{Message: "validate managed artifact target " + target, Cause: err}
	}
	if !owned {
		return &rerrors.PolicySourceError{Message: "managed artifact target resolves outside its root: " + target}
	}
	return nil
}

// installJSONHooks merges reconc's hook entries into a nested JSON settings
// file. Preserves any non-reconc keys the user
// has set. Idempotent: reconc-owned entries (identified by a repo-local
// wrapper/runtime signature) are replaced on each run, so running
// `reconc hook install claude-code` twice produces identical output.
//
// Behaviour:
//   - Target missing       -> write the generated artefact verbatim.
//   - Target empty or "{}" -> treat as missing.
//   - Target has content   -> parse, merge, write.
//   - Non-reconc keys at any depth are preserved.
//   - Malformed JSON       -> error unless --force (then overwrite).
func installJSONHooks(kind, relPath, repoRoot string, force bool) (*InstallReport, error) {
	var mergeDiff MergeDiff
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve repo path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root, Cause: err}
	}

	target := filepath.Join(root, relPath)
	if err := requireManagedTargetWithin(root, target); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}

	artifact, err := Generate(kind)
	if err != nil {
		return nil, err
	}
	var reconcPart map[string]interface{}
	if err := json.Unmarshal([]byte(artifact.Content), &reconcPart); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "internal: generated artifact is not valid JSON", Cause: err}
	}

	action := "created"
	existing, err := readManagedArtifact(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}

	var merged map[string]interface{}
	backupPath := ""
	if len(existing) == 0 || strings.TrimSpace(string(existing)) == "{}" {
		merged = reconcPart
	} else {
		action = "updated"
		if err := json.Unmarshal(existing, &merged); err != nil {
			if !force {
				return nil, &rerrors.PolicySourceError{
					Message: target + " is not valid JSON; pass --force to overwrite with a fresh reconc config",
					Cause:   err,
				}
			}
			backupPath, err = backupMalformedConfig(target, existing)
			if err != nil {
				return nil, err
			}
			merged = reconcPart
		} else {
			// Collect dropped user-modified reconc entries so the caller
			// can warn. KeepUserEdits is false by default to preserve
			// reinstall semantics. Stored in the InstallReport.
			mergeDiff = mergeReconcHooks(merged, reconcPart, MergeOptions{KeepUserEdits: false})
		}
	}

	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "marshal merged config", Cause: err}
	}
	if writeAction, err := writeGeneratedArtifact(target, string(append(out, '\n')), false); err != nil {
		return nil, err
	} else if writeAction == "unchanged" {
		action = writeAction
	}

	nextAction := "Restart your agent session so it picks up the new hooks."
	return &InstallReport{
		Kind:             kind,
		RepoRoot:         root,
		TargetPath:       relPath,
		Action:           action,
		Executable:       false,
		NextAction:       nextAction,
		DroppedUserEdits: mergeDiff.Removed,
		BackupPath:       backupPath,
	}, nil
}

// backupMalformedConfig preserves the original bytes of a malformed
// config that --force is about to replace. The backup is hash-addressed
// and create-only, so identical content maps to one stable file and an
// existing backup with the same digest counts as already written.
func backupMalformedConfig(target string, existing []byte) (string, error) {
	sum := sha256.Sum256(existing)
	backup := target + ".reconc-backup-" + hex.EncodeToString(sum[:4])
	file, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			backupData, readErr := readManagedArtifact(backup)
			if readErr != nil {
				return "", &rerrors.PolicySourceError{Message: "verify existing malformed-config backup " + backup, Cause: readErr}
			}
			if !bytes.Equal(backupData, existing) {
				return "", &rerrors.PolicySourceError{Message: "existing malformed-config backup does not match the source: " + backup}
			}
			if chmodErr := os.Chmod(backup, 0o600); chmodErr != nil {
				return "", &rerrors.PolicySourceError{Message: "secure existing malformed-config backup " + backup, Cause: chmodErr}
			}
			return backup, nil
		}
		return "", &rerrors.PolicySourceError{Message: "back up malformed config to " + backup, Cause: err}
	}
	if written, err := file.Write(existing); err != nil || written != len(existing) {
		_ = file.Close()
		_ = os.Remove(backup)
		if err == nil {
			err = fmt.Errorf("short write: wrote %d of %d bytes", written, len(existing))
		}
		return "", &rerrors.PolicySourceError{Message: "back up malformed config to " + backup, Cause: err}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(backup)
		return "", &rerrors.PolicySourceError{Message: "sync malformed-config backup " + backup, Cause: err}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(backup)
		return "", &rerrors.PolicySourceError{Message: "back up malformed config to " + backup, Cause: err}
	}
	if err := syncManagedArtifactParent(backup); err != nil {
		return "", &rerrors.PolicySourceError{Message: "sync malformed-config backup directory for " + backup, Cause: err}
	}
	return backup, nil
}

func installOpenCode(repoRoot string, force bool) (*InstallReport, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve repo path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root, Cause: err}
	}
	target := filepath.Join(root, OpenCodePluginPath)
	if err := requireManagedTargetWithin(root, target); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}
	artifact, err := Generate(KindOpenCode)
	if err != nil {
		return nil, err
	}
	action := "created"
	if existing, err := readManagedArtifact(target); err == nil {
		action = "updated"
		text := string(existing)
		if !force && !strings.Contains(text, "Managed by reconc") && !strings.Contains(text, "reconc hook runtime") {
			return nil, &rerrors.PolicySourceError{
				Message: OpenCodePluginPath + " already exists and is not reconc-managed; pass --force to overwrite",
			}
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	if writeAction, err := writeGeneratedArtifact(target, artifact.Content, false); err != nil {
		return nil, err
	} else if writeAction == "unchanged" {
		action = writeAction
	}
	return &InstallReport{
		Kind:       KindOpenCode,
		RepoRoot:   root,
		TargetPath: OpenCodePluginPath,
		Action:     action,
		Executable: false,
		NextAction: "Restart OpenCode in this repository so it loads .opencode/plugins/reconc.js.",
	}, nil
}

func installAntigravity(repoRoot string, force bool) (*InstallReport, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "resolve repo path", Cause: err}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, &rerrors.PolicySourceError{Message: "repo path is not a directory: " + root, Cause: err}
	}
	target := filepath.Join(root, AntigravityHooksPath)
	if err := requireManagedTargetWithin(root, target); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}
	artifact, err := generateAntigravity()
	if err != nil {
		return nil, err
	}
	var reconcPart map[string]interface{}
	if err := json.Unmarshal([]byte(artifact.Content), &reconcPart); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "internal: generated Antigravity artifact is not valid JSON", Cause: err}
	}

	action := "created"
	existing, err := readManagedArtifact(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}

	merged := map[string]interface{}{}
	backupPath := ""
	if len(existing) == 0 || strings.TrimSpace(string(existing)) == "{}" {
		merged = reconcPart
	} else {
		action = "updated"
		if err := json.Unmarshal(existing, &merged); err != nil {
			if !force {
				return nil, &rerrors.PolicySourceError{
					Message: target + " is not valid JSON; pass --force to overwrite with a fresh reconc config",
					Cause:   err,
				}
			}
			backupPath, err = backupMalformedConfig(target, existing)
			if err != nil {
				return nil, err
			}
			merged = reconcPart
		} else if existingReconc, ok := merged["reconc"]; ok && !force && !antigravityHookObjectIsReconcManaged(existingReconc) {
			return nil, &rerrors.PolicySourceError{
				Message: AntigravityHooksPath + " has a non-reconc top-level `reconc` hook; pass --force to overwrite it",
			}
		} else {
			merged["reconc"] = reconcPart["reconc"]
		}
	}

	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "marshal merged Antigravity hooks", Cause: err}
	}
	if writeAction, err := writeGeneratedArtifact(target, string(append(out, '\n')), false); err != nil {
		return nil, err
	} else if writeAction == "unchanged" {
		action = writeAction
	}
	return &InstallReport{
		Kind:       KindAntigravity,
		RepoRoot:   root,
		TargetPath: AntigravityHooksPath,
		Action:     action,
		Executable: false,
		NextAction: "Restart Antigravity CLI in this repository so it reloads .agents/hooks.json.",
		BackupPath: backupPath,
	}, nil
}

func antigravityHookObjectIsReconcManaged(value interface{}) bool {
	body, err := json.Marshal(value)
	if err != nil {
		return false
	}
	text := string(body)
	return strings.Contains(text, "reconc hook runtime ") ||
		(strings.Contains(text, "tools/reconc/dist/reconc-") && strings.Contains(text, " hook runtime ")) ||
		strings.Contains(text, "tools/reconc/bin/hook")
}
