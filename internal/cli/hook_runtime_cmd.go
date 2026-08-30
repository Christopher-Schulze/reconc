package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"reconc.dev/reconc/internal/grokacp"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

type firstClassRouteReadiness struct {
	entries [3]firstClassRouteReadinessEntry
	count   int
	inspect func(string, string) (hooks.PlatformStatus, error)
}

type firstClassRouteReadinessEntry struct {
	kind   string
	report hooks.PlatformStatus
	err    error
}

func newFirstClassRouteReadiness() *firstClassRouteReadiness {
	return &firstClassRouteReadiness{
		inspect: hooks.InspectPlatform,
	}
}

func (readiness *firstClassRouteReadiness) platform(root, kind string) (hooks.PlatformStatus, error) {
	for index := 0; index < readiness.count; index++ {
		if readiness.entries[index].kind == kind {
			return readiness.entries[index].report, readiness.entries[index].err
		}
	}
	report, err := readiness.inspect(root, kind)
	if readiness.count < len(readiness.entries) {
		readiness.entries[readiness.count] = firstClassRouteReadinessEntry{kind: kind, report: report, err: err}
		readiness.count++
	}
	return report, err
}

func dedupToFirstClassRoute(readiness *firstClassRouteReadiness, root agentsession.ResolvedRepoRoot, sourceRuntime, firstClassKind, event string, stderr io.Writer) bool {
	report, err := readiness.platform(root.Path(), firstClassKind)
	if err != nil || report.State != hooks.StateConfigured {
		return false
	}
	fmt.Fprintf(stderr, "reconc hook runtime: %s deduplicated; first-class %s owns this event\n", event, report.TargetPath)
	if err := agentsession.RecordHookLivenessResolved(root, sourceRuntime, event); err != nil {
		fmt.Fprintf(stderr, "reconc hook liveness (warn): %s\n", err)
	}
	return true
}

// runKimiCodeRuntime is the global Kimi Code hook entry point. It no-ops
// outside repositories with an explicit Reconc config, then delegates the
// discovered root to the ordinary registry runtime.
func runKimiCodeRuntime(args []string, stdout, stderr io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, "Usage: reconc hook kimi-runtime <event>   (internal; reads JSON from stdin)")
		return nil
	}
	if len(args) != 1 {
		return &CLIError{ExitCode: 1, Message: "reconc hook kimi-runtime: expected exactly one <event>"}
	}
	event := args[0]
	route, ok := hooks.RuntimeEvent(event)
	if !ok || route.PlatformKind != hooks.KindKimiCode {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook kimi-runtime: unknown Kimi Code event %q", event)}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "reconc hook kimi-runtime warning: resolve working directory: "+err.Error())
		return nil
	}
	discovery, err := ingest.DiscoverPolicyRepo(cwd)
	if err != nil {
		fmt.Fprintln(stderr, "reconc hook kimi-runtime warning: discover repository: "+err.Error())
		return nil
	}
	if !discovery.Discovered || discovery.ConfigPath == nil {
		return nil
	}
	return runHookRuntime([]string{event, discovery.RepoRoot}, stdout, stderr)
}

// Design anchor: the threat model in docs/architecture.md#threat-model-hook-runtime
// specifies the behaviour for every failure mode (fail-closed vs
// fail-open per event, max payload size, depth limits, timeout).
// runHookRuntime is the single enforcement point for those contracts.
func runHookRuntime(args []string, stdout, stderr io.Writer) error {
	return runHookRuntimeWithInput(args, os.Stdin, stdout, stderr)
}

func runHookRuntimeWithInput(args []string, input io.Reader, stdout, stderr io.Writer) error {
	return runHookRuntimeWithResolver(args, input, stdout, stderr, agentsession.ResolveRepoRootRef)
}

func runHookRuntimeWithResolver(
	args []string,
	input io.Reader,
	stdout, stderr io.Writer,
	resolveRoot func(string) (agentsession.ResolvedRepoRoot, error),
) error {
	return runHookRuntimeWithResolverAndEvaluator(args, input, stdout, stderr, resolveRoot, runtime.NewEvaluator())
}

func runHookRuntimeWithResolverAndEvaluator(
	args []string,
	input io.Reader,
	stdout, stderr io.Writer,
	resolveRoot func(string) (agentsession.ResolvedRepoRoot, error),
	evaluator *runtime.Evaluator,
) error {
	return runHookRuntimeWithResolverEvaluatorAndStopCache(args, input, stdout, stderr, resolveRoot, evaluator, nil)
}

func runHookRuntimeWithResolverEvaluatorAndStopCache(
	args []string,
	input io.Reader,
	stdout, stderr io.Writer,
	resolveRoot func(string) (agentsession.ResolvedRepoRoot, error),
	evaluator *runtime.Evaluator,
	stopCache *agentsession.StopDecisionCache,
) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc hook runtime: missing <event> <repo>"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc hook runtime <event> <repo>   (reads JSON from stdin)")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Events:")
			for _, event := range hooks.RuntimeEvents() {
				fmt.Fprintln(stdout, "  "+event)
			}
			return nil
		}
	}
	if len(args) < 2 {
		return &CLIError{ExitCode: 1, Message: "reconc hook runtime: expected <event> <repo>"}
	}
	event := args[0]
	repo := args[1]
	route, knownEvent := hooks.RuntimeEvent(event)
	if !knownEvent {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook runtime: unknown event %q", event)}
	}
	timing := newHookRuntimeTiming(event, stderr)
	exitCode := 0
	defer func() {
		timing.finish(exitCode)
	}()
	if isObservationOnlyHookEvent(event) {
		lowerObservationHookPriorityBestEffort()
	}

	payload, err := agentsession.ReadPayload(input)
	if err != nil {
		exitCode, err = emitHookRuntimeFailure(adaptHookRuntimeFailure(route, hookRuntimeFailurePayloadRead, err), stdout, stderr)
		return err
	}
	timing.mark("payload_read")
	root, err := resolveRoot(repo)
	if err != nil {
		exitCode, err = emitHookRuntimeFailure(adaptHookRuntimeFailure(route, hookRuntimeFailureRootResolve, err), stdout, stderr)
		return err
	}
	repo = root.Path()
	timing.mark("root_resolve")
	readiness := newFirstClassRouteReadiness()
	if route.PlatformKind != hooks.KindCursor && agentsession.PayloadLooksLikeCursor(payload) {
		if dedupToFirstClassRoute(readiness, root, route.PlatformKind, hooks.KindCursor, event, stderr) {
			return nil
		}
	}
	if route.PlatformKind != hooks.KindDevinCLI && agentsession.PayloadLooksLikeDevin(payload, repo) {
		if dedupToFirstClassRoute(readiness, root, route.PlatformKind, hooks.KindDevinCLI, event, stderr) {
			return nil
		}
	}
	if route.PlatformKind != hooks.KindGrok && agentsession.PayloadLooksLikeGrok(payload) {
		if dedupToFirstClassRoute(readiness, root, route.PlatformKind, hooks.KindGrok, event, stderr) {
			return nil
		}
	}
	if platform, namespaced := namespacedMCPPlatform(route); namespaced {
		// Claude Code and Codex route their `mcp__<server>__<tool>` namespace
		// into the MCP path on generic tool events. Every other event on those
		// hosts keeps the host payload untouched.
		payload, err = agentsession.NormalizeNamespacedMCPPayload(platform, route.Event == hooks.EventMCPBefore, payload)
		timing.mark("namespaced_mcp_normalize")
	}
	switch route.PlatformKind {
	case hooks.KindCursor:
		payload, err = agentsession.NormalizeCursorPayload(event, payload)
		timing.mark("cursor_normalize")
	case hooks.KindDevinCLI:
		payload, err = agentsession.NormalizeDevinPayload(event, payload, repo)
		timing.mark("devin_normalize")
	case hooks.KindGitHubCopilot:
		payload, err = agentsession.NormalizeGitHubCopilotPayload(event, payload, repo)
		timing.mark("copilot_normalize")
	case hooks.KindGrok:
		payload, err = agentsession.NormalizeGrokPayload(event, payload, repo)
		timing.mark("grok_normalize")
	case hooks.KindKimiCode:
		payload, err = agentsession.NormalizeKimiCodePayload(event, payload, repo)
		timing.mark("kimi_code_normalize")
	case hooks.KindOMP:
		payload, err = agentsession.NormalizeOMPPayload(event, payload, repo)
		timing.mark("omp_normalize")
	case hooks.KindPi:
		payload, err = agentsession.NormalizePiPayload(event, payload, repo)
		timing.mark("pi_normalize")
	case hooks.KindZCode:
		payload, err = agentsession.NormalizeZCodePayload(event, payload, repo)
		timing.mark("zcode_normalize")
	}
	if err != nil {
		exitCode, err = emitHookRuntimeFailure(adaptHookRuntimeFailure(route, hookRuntimeFailurePayloadValidate, err), stdout, stderr)
		return err
	}
	grokPrepareWarning := ""
	if route.PlatformKind == hooks.KindGrok && event == "grok-stop" {
		prepared, _, prepareErr := grokacp.PrepareStrictTUIStop(payload)
		if prepareErr != nil {
			grokPrepareWarning = "reconc grok strict stop (warn): " + prepareErr.Error()
		} else {
			payload = prepared
		}
		timing.mark("grok_strict_prepare")
	}

	handler, executable := hookHandlerForRoute(event, route)
	if !executable {
		exitCode = 1
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook runtime: event %q is not executable", event)}
	}
	result := agentsession.RunHookRequestWithEvaluatorAndStopCache(root, handler, event, payload, evaluator, stopCache)
	timing.mark("handler")
	if result.Err != nil {
		if result.ExitCode == 0 {
			result.ExitCode = 2
		}
		if !strings.Contains(result.Stderr, result.Err.Error()) {
			if result.Stderr != "" {
				result.Stderr += "; "
			}
			result.Stderr += "reconc hook encoding failure: " + result.Err.Error()
		}
	}
	if result.Err == nil && result.ExitCode != 0 && route.ErrorPolicy == hooks.FailureAllow {
		result.ExitCode = 0
	}
	if grokPrepareWarning != "" {
		if result.Stderr != "" {
			result.Stderr += "; "
		}
		result.Stderr += grokPrepareWarning
	}
	if err := agentsession.RecordHookLivenessResolved(root, route.PlatformKind, event); err != nil {
		if result.Stderr != "" {
			result.Stderr += "; "
		}
		result.Stderr += "reconc hook liveness (warn): " + err.Error()
	}
	switch route.PlatformKind {
	case hooks.KindClaudeCode:
		if route.Event == hooks.EventPostCompaction {
			hookEventName := "PostCompact"
			if event == "claude-compaction-recovery" {
				hookEventName = "SessionStart"
			}
			result = agentsession.AdaptPostCompactionResult(result, hookEventName)
			timing.mark("claude_compaction_adapt")
		}
	case hooks.KindCodex:
		if route.Event == hooks.EventPostCompaction {
			result = agentsession.AdaptCodexCompactionResult(result)
			timing.mark("codex_compaction_adapt")
		}
	case hooks.KindCursor:
		result = agentsession.AdaptCursorResult(event, result)
		timing.mark("cursor_adapt")
	case hooks.KindGitHubCopilot:
		result = agentsession.AdaptGitHubCopilotResult(event, result)
		timing.mark("copilot_adapt")
	case hooks.KindGrok:
		if event == "grok-stop" {
			if note := grokacp.SteerTUIStop(repo, payload, result); note != "" {
				if result.Stderr != "" {
					result.Stderr += "; "
				}
				result.Stderr += note
			}
			timing.mark("grok_steer")
		}
		result = agentsession.AdaptGrokResult(event, result)
		timing.mark("grok_adapt")
	case hooks.KindKimiCode:
		result = agentsession.AdaptKimiCodeResult(event, result)
		timing.mark("kimi_code_adapt")
	}
	if result.Err != nil {
		if !strings.Contains(result.Stderr, result.Err.Error()) {
			if result.Stderr != "" {
				result.Stderr += "; "
			}
			result.Stderr += "reconc hook encoding failure: " + result.Err.Error()
		}
		if result.Stdout == "" {
			result.ExitCode = 2
		}
	}
	result = boundHookResult(result, route)

	if result.Stdout != "" {
		fmt.Fprintln(stdout, result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprintln(stderr, result.Stderr)
	}
	if result.ExitCode != 0 {
		exitCode = result.ExitCode
		return &CLIError{ExitCode: result.ExitCode, Message: ""}
	}
	return nil
}

type hookRuntimeFailureStage string

const (
	hookRuntimeFailurePayloadRead     hookRuntimeFailureStage = "read the hook payload"
	hookRuntimeFailureRootResolve     hookRuntimeFailureStage = "resolve the repository root"
	hookRuntimeFailurePayloadValidate hookRuntimeFailureStage = "validate the hook payload"
)

type hookRuntimeFailureAdaptation struct {
	exitCode          int
	stdout            string
	stderr            string
	cliError          string
	stdoutWriteAction string
}

// adaptHookRuntimeFailure is the side-effect-free owner of host transport
// behavior for failures before handler execution.
func adaptHookRuntimeFailure(route hooks.RuntimeRoute, stage hookRuntimeFailureStage, err error) hookRuntimeFailureAdaptation {
	diagnostic := hookRuntimeBoundaryDiagnostic(route.PlatformKind, stage, err)
	stopEvent := route.Event == hooks.EventStop || route.Event == hooks.EventSubagentStop
	if route.PlatformKind == hooks.KindGitHubCopilot && stopEvent {
		body, encodeErr := json.Marshal(map[string]string{
			"decision": "block",
			"reason":   strings.TrimSpace(diagnostic),
		})
		if encodeErr != nil {
			return hookRuntimeFailureAdaptation{exitCode: 2, cliError: "reconc hook runtime: encode GitHub Copilot block response: " + encodeErr.Error()}
		}
		return hookRuntimeFailureAdaptation{
			stdout:            string(body),
			stdoutWriteAction: "write GitHub Copilot block response",
		}
	}
	if route.PlatformKind == hooks.KindGrok && route.Event == hooks.EventPreToolUse {
		body, encodeErr := json.Marshal(map[string]string{
			"decision": "deny",
			"reason":   strings.TrimSpace(diagnostic),
		})
		if encodeErr != nil {
			return hookRuntimeFailureAdaptation{exitCode: 2, cliError: "reconc hook runtime: encode Grok denial response: " + encodeErr.Error()}
		}
		return hookRuntimeFailureAdaptation{
			stdout:            string(body),
			stdoutWriteAction: "write Grok denial response",
		}
	}
	if route.ErrorPolicy == hooks.FailureBlock {
		return hookRuntimeFailureAdaptation{exitCode: 2, cliError: "reconc hook runtime: " + diagnostic}
	}
	return hookRuntimeFailureAdaptation{stderr: "reconc hook runtime warning: " + diagnostic}
}

func emitHookRuntimeFailure(adaptation hookRuntimeFailureAdaptation, stdout, stderr io.Writer) (int, error) {
	if adaptation.stdout != "" {
		if _, err := fmt.Fprintln(stdout, adaptation.stdout); err != nil {
			return 2, &CLIError{ExitCode: 2, Message: "reconc hook runtime: " + adaptation.stdoutWriteAction + ": " + err.Error()}
		}
	}
	if adaptation.stderr != "" {
		fmt.Fprintln(stderr, adaptation.stderr)
	}
	if adaptation.exitCode != 0 {
		return adaptation.exitCode, &CLIError{ExitCode: adaptation.exitCode, Message: adaptation.cliError}
	}
	return 0, nil
}

func hookRuntimeBoundaryDiagnostic(platformKind string, operation hookRuntimeFailureStage, err error) string {
	displayName := map[string]string{
		hooks.KindCursor:        "Cursor",
		hooks.KindDevinCLI:      "Devin CLI",
		hooks.KindGitHubCopilot: "GitHub Copilot",
		hooks.KindGrok:          "Grok",
		hooks.KindAntigravity:   "Antigravity",
	}[platformKind]
	if displayName != "" {
		return fmt.Sprintf("Reconc could not safely %s for %s.", operation, displayName)
	}
	if err == nil {
		return "Reconc hook runtime failed without a diagnostic."
	}
	return err.Error()
}

// namespacedMCPPlatform reports the MCP policy platform for hosts that publish
// MCP calls as namespaced generic tool calls instead of a dedicated host event.
func namespacedMCPPlatform(route hooks.RuntimeRoute) (policy.MCPPlatform, bool) {
	if route.Event != hooks.EventMCPBefore && route.Event != hooks.EventMCPAfter {
		return "", false
	}
	switch route.PlatformKind {
	case hooks.KindClaudeCode:
		return policy.MCPPlatformClaudeCode, true
	case hooks.KindCodex:
		return policy.MCPPlatformCodex, true
	default:
		return "", false
	}
}

func hookHandlerForRoute(event string, route hooks.RuntimeRoute) (agentsession.HookHandler, bool) {
	if route.PlatformKind == hooks.KindAntigravity {
		switch event {
		case "antigravity-pre-invocation":
			return agentsession.HookHandlerAntigravityPreInvoke, true
		case "antigravity-pre-tool-use":
			return agentsession.HookHandlerAntigravityPreTool, true
		case "antigravity-post-tool-use":
			return agentsession.HookHandlerAntigravityPostTool, true
		case "antigravity-post-invocation":
			return agentsession.HookHandlerAntigravityPostInvoke, true
		case "antigravity-stop":
			return agentsession.HookHandlerAntigravityStop, true
		default:
			return "", false
		}
	}
	switch route.Event {
	case hooks.EventSessionStart:
		return agentsession.HookHandlerSessionStart, true
	case hooks.EventUserPromptSubmit, hooks.EventPermissionDenied, hooks.EventPermissionResult,
		hooks.EventStopFailure, hooks.EventInterrupt, hooks.EventToolObservation,
		hooks.EventNotification, hooks.EventContinuation, hooks.EventSubagentStart:
		if route.PlatformKind == hooks.KindCursor && route.Event == hooks.EventSubagentStart {
			return agentsession.HookHandlerSessionStart, true
		}
		return agentsession.HookHandlerPassive, true
	case hooks.EventWorkspaceOpen:
		return agentsession.HookHandlerWorkspaceOpen, true
	case hooks.EventSubagentStop:
		if route.PlatformKind == hooks.KindGitHubCopilot || route.PlatformKind == hooks.KindCursor {
			return agentsession.HookHandlerStop, true
		}
		return agentsession.HookHandlerPassive, true
	case hooks.EventPreCompaction:
		if route.PlatformKind == hooks.KindOpenCode || route.PlatformKind == hooks.KindKilo {
			return agentsession.HookHandlerPostCompaction, true
		}
		return agentsession.HookHandlerPassive, true
	case hooks.EventPreToolUse:
		if route.PlatformKind == hooks.KindOpenCode || route.PlatformKind == hooks.KindKilo || route.PlatformKind == hooks.KindOMP || route.PlatformKind == hooks.KindPi || route.PlatformKind == hooks.KindZCode {
			return agentsession.HookHandlerMCPAwarePreToolUse, true
		}
		return agentsession.HookHandlerPreToolUse, true
	case hooks.EventPermissionRequest:
		if route.PlatformKind == hooks.KindOMP {
			return agentsession.HookHandlerPassive, true
		}
		return agentsession.HookHandlerPermissionRequest, true
	case hooks.EventPostToolUse:
		if event == "opencode-post-tool-use" || event == "kilo-post-tool-use" || event == "omp-post-tool-use" || event == "pi-post-tool-use" || event == "zcode-post-tool-use" {
			return agentsession.HookHandlerMCPAwarePostToolUse, true
		}
		if event == "codex-post-tool-use" || event == "devin-post-tool-use" {
			return agentsession.HookHandlerPostToolUseComplete, true
		}
		return agentsession.HookHandlerPostToolUse, true
	case hooks.EventPostToolUseFailure:
		if route.PlatformKind == hooks.KindOpenCode || route.PlatformKind == hooks.KindKilo || route.PlatformKind == hooks.KindOMP || route.PlatformKind == hooks.KindPi || route.PlatformKind == hooks.KindZCode {
			return agentsession.HookHandlerMCPAwarePostToolUse, true
		}
		return agentsession.HookHandlerPostToolUseFailure, true
	case hooks.EventMCPBefore:
		return agentsession.HookHandlerMCPBefore, true
	case hooks.EventMCPAfter:
		return agentsession.HookHandlerMCPAfter, true
	case hooks.EventStop:
		return agentsession.HookHandlerStop, true
	case hooks.EventSessionEnd:
		return agentsession.HookHandlerSessionEnd, true
	case hooks.EventPostCompaction:
		if route.PlatformKind == hooks.KindOpenCode || route.PlatformKind == hooks.KindKilo {
			return agentsession.HookHandlerPassive, true
		}
		return agentsession.HookHandlerPostCompaction, true
	default:
		return "", false
	}
}
