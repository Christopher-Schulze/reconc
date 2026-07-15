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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/execfile"
)

// Hook artifact paths.
const (
	GitPreCommitPath         = ".git/hooks/pre-commit"
	GitPreCommitScaffoldPath = ".githooks/pre-commit"
	ClaudeCodeSettingsPath   = ".claude/settings.json"
	CodexHooksPath           = ".codex/hooks.json"
	CursorHooksPath          = ".cursor/hooks.json"
	OpenCodePluginPath       = ".opencode/plugins/reconc.js"
	DevinHooksPath           = ".devin/hooks.v1.json"
	AntigravityHooksPath     = ".agents/hooks.json"
	KiloPluginPath           = ".kilo/plugin/reconc.js"
)

// Supported hook kinds.
const (
	KindGitPreCommit = "git-pre-commit"
	KindClaudeCode   = "claude-code"
	KindCodex        = "codex"
	KindCursor       = "cursor"
	KindOpenCode     = "opencode"
	KindDevinCLI     = "devin-cli"
	KindAntigravity  = "antigravity"
	KindKilo         = "kilo"
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
	switch definition.generator {
	case generatorGitPreCommit:
		return generateGitPreCommit(), nil
	case generatorClaudeCode:
		return generateClaudeCode(), nil
	case generatorCodex:
		return generateCodex(), nil
	case generatorCursor:
		return generateCursor(), nil
	case generatorOpenCode:
		return generateOpenCodeThin(), nil
	case generatorDevinCLI:
		return generateDevinCLI(), nil
	case generatorAntigravity:
		return generateAntigravity(), nil
	case generatorKilo:
		return generateKilo(), nil
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
	switch definition.InstallMode {
	case InstallExecutable:
		return installGitPreCommit(repoRoot, force)
	case InstallNestedJSON:
		return installJSONHooks(kind, definition.TargetPath, repoRoot, force)
	case InstallFlatJSON:
		return installDevinCLI(repoRoot, force)
	case InstallOwnedJSON:
		return installAntigravity(repoRoot, force)
	case InstallPlugin:
		if kind == KindOpenCode {
			return installOpenCode(repoRoot, force)
		}
		return installKilo(repoRoot, force)
	}
	return nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("hook kind %q has no installer", kind)}
}

// ScaffoldKinds returns every generated hook artifact that belongs in
// repo-root-scaffold. Git pre-commit is mapped to .githooks/pre-commit
// because .git/hooks is clone-local and cannot be source-controlled.
func ScaffoldKinds() []string {
	return platformKinds(false)
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

	report := &ScaffoldSyncReport{
		ScaffoldRoot: root,
		Artifacts:    []ScaffoldArtifactReport{},
	}
	for _, kind := range ScaffoldKinds() {
		artifact, err := GenerateScaffoldArtifact(kind)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(root, filepath.FromSlash(artifact.TargetPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
		}
		action, err := writeGeneratedArtifact(target, artifact.Content, artifact.Executable)
		if err != nil {
			return nil, err
		}
		report.Artifacts = append(report.Artifacts, ScaffoldArtifactReport{
			Kind:       kind,
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

	gitDir := filepath.Join(root, ".git")
	gitInfo, err := os.Stat(gitDir)
	if err != nil || !gitInfo.IsDir() {
		return nil, &rerrors.PolicySourceError{
			Message: "no .git directory at " + gitDir + "; run `git init` before installing the pre-commit hook",
		}
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create .git/hooks/", Cause: err}
	}
	target := filepath.Join(hooksDir, "pre-commit")

	artifact := generateGitPreCommit()
	action := "created"
	if existing, err := os.ReadFile(target); err == nil {
		_, statErr := os.Stat(target)
		if statErr != nil {
			return nil, &rerrors.PolicySourceError{Message: "stat " + target, Cause: statErr}
		}
		if string(existing) == artifact.Content && execfile.Is(target) {
			action = "unchanged"
		} else if !force {
			return nil, &rerrors.PolicySourceError{
				Message: GitPreCommitPath + " already exists; pass --force to overwrite",
			}
		} else {
			action = "updated"
		}
	} else if !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	if action != "unchanged" {
		writeAction, err := writeGeneratedArtifact(target, artifact.Content, true)
		if err != nil {
			return nil, err
		}
		action = writeAction
	}

	return &InstallReport{
		Kind:       KindGitPreCommit,
		RepoRoot:   root,
		TargetPath: GitPreCommitPath,
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
	if existing, err := os.ReadFile(target); err == nil {
		action = "updated"
		info, statErr := os.Stat(target)
		modeMatches := statErr == nil && generatedModeMatches(target, info, executable, perm)
		if string(existing) == content && modeMatches {
			return "unchanged", nil
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}
	if err := os.WriteFile(target, []byte(content), perm); err != nil {
		return "", &rerrors.PolicySourceError{Message: "write " + target, Cause: err}
	}
	if err := os.Chmod(target, perm); err != nil {
		return "", &rerrors.PolicySourceError{Message: "chmod " + target, Cause: err}
	}
	return action, nil
}

func generatedModeMatches(target string, info os.FileInfo, executable bool, permission os.FileMode) bool {
	if executable {
		return execfile.Is(target)
	}
	return runtime.GOOS == "windows" || info.Mode().Perm() == permission
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
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}

	var merged map[string]interface{}
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
	}, nil
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
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}
	artifact, err := Generate(KindOpenCode)
	if err != nil {
		return nil, err
	}
	action := "created"
	if existing, err := os.ReadFile(target); err == nil {
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
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "create parent dir of " + target, Cause: err}
	}
	artifact := generateAntigravity()
	var reconcPart map[string]interface{}
	if err := json.Unmarshal([]byte(artifact.Content), &reconcPart); err != nil {
		return nil, &rerrors.PolicySourceError{Message: "internal: generated Antigravity artifact is not valid JSON", Cause: err}
	}

	action := "created"
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "read " + target, Cause: err}
	}

	merged := map[string]interface{}{}
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
