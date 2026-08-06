package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func runOfflineHookVerification(surfaces []hooks.VerificationSurface) (hookVerificationReport, error) {
	repo, cleanup, err := prepareOfflineHookVerification()
	if err != nil {
		return hookVerificationReport{}, err
	}
	defer cleanup()
	byKind := make(map[string]offlineHookKindResult)
	for _, kind := range uniqueVerificationKinds(surfaces) {
		byKind[kind] = verifyOfflineHookKind(kind, repo)
	}
	return assembleOfflineHookReport(surfaces, byKind), nil
}

func prepareOfflineHookVerification() (string, func(), error) {
	return prepareHookVerificationRepo("reconc-hook-verify-", true)
}

func prepareHookVerificationRepo(prefix string, stageDeniedPath bool) (string, func(), error) {
	probeRoot, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", nil, fmt.Errorf("create disposable root: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(probeRoot) }
	repo := filepath.Join(probeRoot, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create disposable repository: %w", err)
	}
	restoreEnvironment, err := isolateHookVerificationEnvironment(probeRoot)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if err := initializeHookVerificationRepo(repo, stageDeniedPath); err != nil {
		restoreEnvironment()
		cleanup()
		return "", nil, err
	}
	return repo, func() { restoreEnvironment(); cleanup() }, nil
}

func assembleOfflineHookReport(surfaces []hooks.VerificationSurface, byKind map[string]offlineHookKindResult) hookVerificationReport {
	report := hookVerificationReport{
		FormatVersion: hookVerificationFormatVersion,
		Mode:          "offline",
		Complete:      true,
		Results:       make([]hookVerificationResult, 0, len(surfaces)),
	}
	for _, surface := range surfaces {
		result := offlineSurfaceResult(surface, byKind[surface.Kind])
		if !hookVerificationResultComplete(result) {
			report.Complete = false
		}
		report.Results = append(report.Results, result)
	}
	return report
}

func offlineSurfaceResult(surface hooks.VerificationSurface, source offlineHookKindResult) hookVerificationResult {
	resultClass := "synthetic-block"
	if surface.Kind == hooks.KindGitPreCommit {
		resultClass = "synthetic-commit-block"
	}
	return hookVerificationResult{
		Kind: surface.Kind, Surface: surface.Surface,
		ArtifactGeneration: source.artifactGeneration, Configuration: source.configuration,
		Transport: source.transport, PolicyDecision: source.policyDecision,
		ResponseAdaptation: source.responseAdaptation, DurationMillis: source.durationMillis,
		Configured: source.configured, Discoverable: source.discoverable,
		SyntheticEnforced: source.syntheticEnforced, Inferred: surface.Inferred,
		Degraded: source.degraded, Unsupported: append([]string{}, source.unsupported...),
		ExpectedEvents: append([]string(nil), surface.ExpectedEvents...),
		UnprovenEvents: append([]string(nil), surface.ExpectedEvents...),
		ObservedFields: []string{}, ResultClass: resultClass, Detail: source.detail,
		ActionRequired: surface.Action,
	}
}

func hookVerificationResultComplete(result hookVerificationResult) bool {
	return !result.Degraded && result.ArtifactGeneration == "verified" && result.Configuration == "verified" &&
		result.Transport == "verified" && result.PolicyDecision == "verified" && result.ResponseAdaptation == "verified"
}

func uniqueVerificationKinds(surfaces []hooks.VerificationSurface) []string {
	seen := map[string]bool{}
	kinds := make([]string, 0, len(surfaces))
	for _, surface := range surfaces {
		if seen[surface.Kind] {
			continue
		}
		seen[surface.Kind] = true
		kinds = append(kinds, surface.Kind)
	}
	sort.Strings(kinds)
	return kinds
}

func initializeHookVerificationRepo(repo string, stageDeniedPath bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", repo, "init", "-q")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("initialize disposable Git repository: %w: %s", err, strings.TrimSpace(string(output)))
	}
	files := map[string]string{
		"AGENTS.md":   "# Disposable Reconc hook verification repository\n",
		".reconc.yml": "rules:\n  - id: hook-verify-deny-write\n    kind: deny_write\n    paths: ['forbidden.txt']\n    mode: block\n    message: synthetic hook verification denial\n",
	}
	if stageDeniedPath {
		files["forbidden.txt"] = "synthetic verification input\n"
	}
	for relative, content := range files {
		if err := os.WriteFile(filepath.Join(repo, relative), []byte(content), 0o644); err != nil {
			return fmt.Errorf("write disposable %s: %w", relative, err)
		}
	}
	if _, err := compiler.CompileRepoPolicy(repo, "hook-verify"); err != nil {
		return fmt.Errorf("compile disposable policy: %w", err)
	}
	if stageDeniedPath {
		command = exec.CommandContext(ctx, "git", "-C", repo, "add", "--", "forbidden.txt")
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("stage disposable denied path: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func isolateHookVerificationEnvironment(probeRoot string) (func(), error) {
	binDir := filepath.Join(probeRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, fmt.Errorf("create disposable binary directory: %w", err)
	}
	if err := installHookVerificationBareExecutable(binDir); err != nil {
		return nil, err
	}
	values := hookVerificationEnvironmentValues(probeRoot, binDir)
	return replaceHookVerificationEnvironment(values)
}

func installHookVerificationBareExecutable(binDir string) error {
	running, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve running executable: %w", err)
	}
	bareName := "reconc"
	if runtime.GOOS == "windows" {
		bareName += ".exe"
	}
	return linkOrCopyVerificationExecutable(running, filepath.Join(binDir, bareName))
}

func hookVerificationEnvironmentValues(probeRoot, binDir string) map[string]string {
	return map[string]string{
		"RECONC_HOME":             filepath.Join(probeRoot, "reconc-home"),
		agentsession.StateRootEnv: filepath.Join(probeRoot, "session-state"),
		"KIMI_CODE_HOME":          filepath.Join(probeRoot, "kimi-code"),
		"PI_CODING_AGENT_DIR":     filepath.Join(probeRoot, "pi-agent"),
		"KILO_PURE":               "",
		"PATH":                    binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}

type hookVerificationPriorEnv struct {
	value string
	set   bool
}

func replaceHookVerificationEnvironment(values map[string]string) (func(), error) {
	prior := make(map[string]hookVerificationPriorEnv, len(values))
	for name, value := range values {
		old, set := os.LookupEnv(name)
		prior[name] = hookVerificationPriorEnv{value: old, set: set}
		if err := os.Setenv(name, value); err != nil {
			restoreHookVerificationEnvironment(prior)
			return nil, fmt.Errorf("set isolated %s: %w", name, err)
		}
	}
	return func() { restoreHookVerificationEnvironment(prior) }, nil
}

func restoreHookVerificationEnvironment(prior map[string]hookVerificationPriorEnv) {
	for name, old := range prior {
		if old.set {
			_ = os.Setenv(name, old.value)
		} else {
			_ = os.Unsetenv(name)
		}
	}
}

func linkOrCopyVerificationExecutable(source, target string) error {
	if err := os.Link(source, target); err == nil {
		return nil
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open running executable: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return fmt.Errorf("create disposable bare executable: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy running executable: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close disposable bare executable: %w", err)
	}
	return nil
}

func verifyOfflineHookKind(kind, repo string) offlineHookKindResult {
	result := offlineHookKindResult{
		artifactGeneration: "failed", configuration: "failed", transport: "failed",
		policyDecision: "failed", responseAdaptation: "failed",
	}
	if detail := verifyOfflineArtifactAndInstall(kind, repo); detail != "" {
		result.detail, result.degraded = detail, true
		return result
	}
	result.artifactGeneration, result.configuration = "verified", "verified"
	result.configured, result.discoverable = true, true
	decision := verifyOfflinePolicyDecision(kind, repo)
	result.durationMillis = decision.durationMillis
	if !decision.verified {
		result.detail, result.degraded = decision.detail, true
		return result
	}
	result.policyDecision, result.responseAdaptation = "verified", "verified"
	result.syntheticEnforced = true
	transport, adapter, unsupported, detail := verifyGeneratedHookTransport(kind, repo)
	applyOfflineTransportResult(&result, transport, adapter, unsupported, detail)
	return result
}

func verifyOfflineArtifactAndInstall(kind, repo string) string {
	artifact, err := hooks.Generate(kind)
	if err != nil {
		return "artifact generation failed: " + err.Error()
	}
	if artifact.TargetPath == "" || artifact.Content == "" {
		return "generated artifact is incomplete"
	}
	if _, err := hooks.Install(kind, repo, false); err != nil {
		return "isolated installation failed: " + err.Error()
	}
	return ""
}

type syntheticHookDecision struct {
	verified       bool
	durationMillis int64
	detail         string
}

func verifyOfflinePolicyDecision(kind, repo string) syntheticHookDecision {
	started := time.Now()
	if kind == hooks.KindGitPreCommit {
		return verifyOfflineGitDecision(repo, started)
	}
	return verifyOfflineAgentDecision(kind, repo, started)
}

func verifyOfflineGitDecision(repo string, started time.Time) syntheticHookDecision {
	var stdout, stderr bytes.Buffer
	err := runCI([]string{repo, "--staged", "--json"}, "hook-verify", &stdout, &stderr)
	verified := ExitCode(err) == 2 && strings.Contains(stdout.String()+stderr.String(), "hook-verify-deny-write")
	detail := ""
	if !verified {
		detail = fmt.Sprintf("synthetic staged decision was not blocked (exit %d)", ExitCode(err))
	}
	return syntheticHookDecision{verified: verified, durationMillis: elapsedMillis(started), detail: detail}
}

func verifyOfflineAgentDecision(kind, repo string, started time.Time) syntheticHookDecision {
	event, ok := hooks.RuntimeEventFor(kind, hooks.EventPreToolUse)
	if !ok {
		return syntheticHookDecision{detail: "registry has no first-class pre-tool route"}
	}
	payload, err := hookVerificationPayload(kind, repo)
	if err != nil {
		return syntheticHookDecision{detail: err.Error()}
	}
	var stdout, stderr bytes.Buffer
	runtimeErr := runHookRuntimeWithInput([]string{event, repo}, bytes.NewReader(payload), &stdout, &stderr)
	verified := hookVerificationBlocked(kind, runtimeErr, stdout.String(), stderr.String())
	detail := ""
	if !verified {
		detail = fmt.Sprintf("synthetic denied write was not blocked (exit %d)", ExitCode(runtimeErr))
	}
	return syntheticHookDecision{verified: verified, durationMillis: elapsedMillis(started), detail: detail}
}

func applyOfflineTransportResult(result *offlineHookKindResult, transport, adapter string, unsupported []string, detail string) {
	result.transport = transport
	if adapter != "not-applicable" {
		result.responseAdaptation = adapter
	}
	result.unsupported = unsupported
	if transport != "verified" || result.responseAdaptation != "verified" {
		result.degraded = true
	}
	result.detail = detail
	if result.detail == "" {
		result.detail = "artifact, isolated configuration, generated transport, policy decision, and native response contract verified; no live host execution claimed"
	}
}

func elapsedMillis(started time.Time) int64 {
	elapsed := time.Since(started).Milliseconds()
	if elapsed < 1 {
		return 1
	}
	return elapsed
}

func hookVerificationPayload(kind, repo string) ([]byte, error) {
	var payload map[string]interface{}
	switch kind {
	case hooks.KindClaudeCode, hooks.KindCodex:
		payload = map[string]interface{}{"session_id": "verify-" + kind, "tool_name": "Write", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}}
	case hooks.KindOpenCode, hooks.KindKilo:
		payload = map[string]interface{}{"session_id": "verify-" + kind, "reconc_runtime": kind, "tool_name": "Write", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}}
	case hooks.KindDevinCLI:
		payload = map[string]interface{}{"session_id": "verify-devin", "tool_name": "edit", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}}
	case hooks.KindCursor:
		payload = map[string]interface{}{"conversation_id": "verify-cursor", "tool_name": "Write", "tool_input": map[string]interface{}{"filePath": "forbidden.txt"}}
	case hooks.KindGitHubCopilot:
		payload = map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "verify-copilot", "cwd": repo, "tool_name": "Edit", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}}
	case hooks.KindGrok:
		payload = map[string]interface{}{"hookEventName": "pre_tool_use", "sessionId": "verify-grok", "workspaceRoot": repo, "toolName": "search_replace", "toolUseId": "call-1", "toolInput": map[string]interface{}{"path": "forbidden.txt", "old_string": "a", "new_string": "b"}, "toolInputTruncated": false}
	case hooks.KindOMP, hooks.KindPi:
		payload = map[string]interface{}{"hook_event_name": "tool_call", "session_id": "verify-" + kind, "cwd": repo, "tool_name": "write", "tool_input": map[string]interface{}{"path": "forbidden.txt"}, "tool_call_id": "call-1"}
	case hooks.KindAntigravity:
		payload = map[string]interface{}{"conversationId": "verify-antigravity", "stepIdx": 1, "toolCall": map[string]interface{}{"name": "write_to_file", "args": map[string]interface{}{"TargetFile": "forbidden.txt"}}}
	case hooks.KindKimiCode:
		payload = map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "verify-kimi", "cwd": repo, "tool_name": "Write", "tool_input": map[string]interface{}{"path": "forbidden.txt"}, "tool_call_id": "call-1"}
	case hooks.KindZCode:
		payload = map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "verify-zcode", "cwd": repo, "tool_name": "Write", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}, "tool_use_id": "call-1"}
	default:
		return nil, fmt.Errorf("no synthetic payload contract for %s", kind)
	}
	return json.Marshal(payload)
}

func hookVerificationBlocked(kind string, err error, stdout, stderr string) bool {
	combined := stdout + stderr
	if !strings.Contains(combined, "hook-verify-deny-write") {
		return false
	}
	switch kind {
	case hooks.KindCursor:
		return ExitCode(err) == 0 && strings.Contains(stdout, `"permission":"deny"`)
	case hooks.KindGitHubCopilot:
		return ExitCode(err) == 0 && strings.Contains(stdout, `"permissionDecision":"deny"`)
	case hooks.KindGrok, hooks.KindAntigravity:
		return ExitCode(err) == 0 && strings.Contains(stdout, `"decision":"deny"`)
	default:
		return ExitCode(err) == 2
	}
}

func verifyGeneratedHookTransport(kind, repo string) (string, string, []string, string) {
	if kind == hooks.KindKimiCode {
		return verifyGeneratedKimiTransport()
	}
	shell, err := hookVerificationShell()
	if err != nil {
		return "unavailable", "unavailable",
			[]string{"generated shell transport execution without POSIX sh"},
			"transport execution requires the shipped POSIX-compatible hook shell: " + err.Error()
	}
	if kind == hooks.KindGitPreCommit {
		return verifyGeneratedGitTransport(repo, shell)
	}
	if err := writeHookVerificationFakeBinary(repo); err != nil {
		return "failed", "failed", nil, "create transport probe: " + err.Error()
	}
	transport, detail := verifyGeneratedWrapperTransport(kind, repo, shell)
	if transport != "verified" {
		return transport, "failed", nil, detail
	}
	return verifyGeneratedAdapterTransport(kind, repo)
}

func hookVerificationShell() (string, error) {
	name := "/bin/sh"
	if runtime.GOOS == "windows" {
		name = "sh"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return path, nil
}

func verifyGeneratedGitTransport(repo, shell string) (string, string, []string, string) {
	if err := writeHookVerificationFakeBinary(repo); err != nil {
		return "failed", "failed", nil, "create transport probe: " + err.Error()
	}
	hookPath := filepath.Join(repo, filepath.FromSlash(hooks.GitPreCommitPath))
	command := exec.Command(shell, hookPath)
	command.Dir = repo
	output, err := command.CombinedOutput()
	if exitCodeOfProcess(err) != 2 || !strings.Contains(string(output), "hook-verify-transport") {
		return "failed", "failed", nil, "generated pre-commit transport did not preserve the blocking exit contract"
	}
	return "verified", "not-applicable", nil, ""
}

func verifyGeneratedKimiTransport() (string, string, []string, string) {
	artifact, err := hooks.Generate(hooks.KindKimiCode)
	if err != nil || !strings.Contains(artifact.Content, `command = "reconc hook kimi-runtime kimi-pre-tool-use"`) {
		return "failed", "failed", nil, "generated Kimi Code command transport is incomplete"
	}
	return "verified", "not-applicable", nil, ""
}

func verifyGeneratedWrapperTransport(kind, repo, shell string) (string, string) {
	event, ok := hooks.RuntimeEventFor(kind, hooks.EventPreToolUse)
	if !ok {
		return "failed", "registry pre-tool route missing"
	}
	wrapperPath := filepath.Join(repo, filepath.FromSlash(hooks.WrapperPath))
	if info, statErr := os.Stat(wrapperPath); statErr != nil {
		return "failed", "generated wrapper is unavailable after installation: " + statErr.Error()
	} else if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return "failed", "generated wrapper is not a regular executable file after installation"
	}
	command := exec.Command(shell, wrapperPath, event, repo)
	command.Dir = repo
	command.Stdin = strings.NewReader("{}")
	output, err := command.CombinedOutput()
	if kind == hooks.KindGrok {
		if err != nil || !strings.Contains(string(output), `"decision":"deny"`) {
			return "failed", "generated Grok wrapper did not preserve explicit deny JSON"
		}
	} else if exitCodeOfProcess(err) != 2 || !strings.Contains(string(output), "hook-verify-transport") {
		return "failed", fmt.Sprintf("generated wrapper did not preserve the blocking exit contract (exit=%d marker=%t error=%v)", exitCodeOfProcess(err), strings.Contains(string(output), "hook-verify-transport"), err)
	}
	return "verified", ""
}

func verifyGeneratedAdapterTransport(kind, repo string) (string, string, []string, string) {
	if kind != hooks.KindOpenCode && kind != hooks.KindKilo && kind != hooks.KindOMP && kind != hooks.KindPi {
		return "verified", "not-applicable", nil, ""
	}
	adapterStatus, detail := verifyGeneratedBunAdapter(kind, repo)
	if adapterStatus == "unavailable" {
		return "verified", adapterStatus, []string{"generated TypeScript adapter execution without Bun"}, detail
	}
	if adapterStatus != "verified" {
		return "failed", adapterStatus, nil, detail
	}
	return "verified", "verified", nil, ""
}

func writeHookVerificationFakeBinary(repo string) error {
	target := filepath.Join(repo, ".build", "bin", "reconc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	body := `#!/bin/sh
set -eu
if [ "${1:-}" != "hook" ] && [ "${1:-}" != "ci" ]; then
  exit 9
fi
if [ "${1:-}" = "ci" ]; then
  printf '%s\n' 'hook-verify-transport' >&2
  exit 2
fi
case "${2:-}" in
  grok-pre-tool-guard)
    printf '%s\n' '{"decision":"deny","reason":"hook-verify-transport"}'
    exit 0
    ;;
  runtime)
    cat >/dev/null
    printf '%s\n' 'hook-verify-transport' >&2
    exit 2
    ;;
esac
exit 9
`
	return os.WriteFile(target, []byte(body), 0o755)
}

func exitCodeOfProcess(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return -1
	}
	return exitError.ExitCode()
}

const generatedBunAdapterDriver = `import { pathToFileURL } from "node:url"
const path = Bun.argv[2]
const kind = Bun.argv[3]
const repo = Bun.argv[4]
const module = await import(pathToFileURL(path).href + "?verify=" + Date.now())
if (kind === "opencode" || kind === "kilo") {
  const factory = kind === "opencode" ? module.ReconcOpenCodePlugin : module.default?.server
  if (typeof factory !== "function") throw new Error("plugin factory missing")
  const hooks = await factory({ directory: repo, worktree: repo, client: {} })
  let blocked = false
  try {
    await hooks["tool.execute.before"](
      { sessionID: "verify-" + kind, tool: "write", callID: "call-1", args: { file_path: "forbidden.txt" } },
      {},
    )
  } catch (error) {
    blocked = String(error?.message || error).includes("hook-verify-transport")
  }
  if (!blocked) throw new Error("plugin did not adapt blocking transport")
} else {
  if (typeof module.default !== "function") throw new Error("extension factory missing")
  const handlers = new Map()
  if (kind === "omp") module.default({ logger: { warn: () => {} }, on: (event, handler) => handlers.set(event, handler) })
  else module.default({ on: (event, handler) => handlers.set(event, handler), sendUserMessage: () => {} })
  const ctx = { cwd: repo, signal: undefined, sessionManager: { getSessionId: () => "verify-" + kind, getSessionFile: () => undefined } }
  const decision = await handlers.get("tool_call")(
    { type: "tool_call", toolCallId: "call-1", toolName: "write", input: { path: "forbidden.txt" } },
    ctx,
  )
  if (decision?.block !== true || !String(decision.reason).includes("hook-verify-transport")) {
    throw new Error("extension did not adapt blocking transport")
  }
}
`

func verifyGeneratedBunAdapter(kind, repo string) (string, string) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		return "unavailable", "Bun is unavailable; the generated wrapper and native response were verified, but the TypeScript host adapter was not executed"
	}
	platform, ok := hooks.PlatformForKind(kind)
	if !ok {
		return "failed", "platform disappeared from registry"
	}
	artifactPath := filepath.Join(repo, filepath.FromSlash(platform.TargetPath))
	driverPath := filepath.Join(filepath.Dir(artifactPath), "reconc-hook-verify.js")
	driver := generatedBunAdapterDriver
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		return "failed", "write TypeScript adapter driver: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, driverPath, artifactPath, kind, repo)
	if output, err := command.CombinedOutput(); err != nil {
		return "failed", fmt.Sprintf("generated TypeScript adapter failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return "verified", ""
}
