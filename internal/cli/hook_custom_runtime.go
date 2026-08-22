package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/customruntime"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func inspectCustomRuntimeStatuses(repoRoot string) ([]hooks.PlatformStatus, error) {
	sources, err := ingest.LoadCustomRuntimeSources(repoRoot)
	if err != nil {
		return nil, err
	}
	globalFreshnessError := ""
	var compiledEvaluator *runtime.CompiledPolicyEvaluator
	if len(sources) > 0 {
		if current, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repoRoot); err != nil {
			globalFreshnessError = err.Error()
		} else {
			compiledEvaluator = current
		}
	}
	reports := make([]hooks.PlatformStatus, 0, len(sources))
	for _, source := range sources {
		freshnessError := globalFreshnessError
		manifest, err := customruntime.DecodeManifest([]byte(source.Content))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.Path, err)
		}
		if compiledEvaluator != nil {
			expectedDigest, ok := compiledEvaluator.CustomRuntimeManifestDigest(manifest.Runtime())
			if !ok || expectedDigest != manifest.Digest() {
				freshnessError = "manifest bytes do not match the validated compiled runtime identity"
			}
		}
		report := hooks.PlatformStatus{
			Kind: manifest.Runtime(), DisplayName: manifest.DisplayName, TargetPath: source.Path,
			State: hooks.StateConfigured, Detail: fmt.Sprintf("%d declarative routes compiled", len(manifest.Routes)),
			Installed: true, Executable: true, Configured: true,
			ExpectedEvents: []string{}, MissingEvents: []string{},
		}
		degraded := []string{}
		for _, route := range manifest.Routes {
			report.ExpectedEvents = append(report.ExpectedEvents, customruntime.LivenessEvent(route.HostEvent))
			if reasons := route.DegradedReasons(); len(reasons) > 0 {
				report.MissingEvents = append(report.MissingEvents, route.HostEvent)
				degraded = append(degraded, route.HostEvent+": "+strings.Join(reasons, ", "))
			}
		}
		if freshnessError != "" {
			report.State = hooks.StateDegraded
			report.Configured = false
			report.Detail = "manifest is not bound to a fresh compiled lock: " + freshnessError
			report.Remediation = "run reconc refresh after reviewing the custom runtime manifest"
		} else if len(degraded) > 0 {
			report.State = hooks.StateDegraded
			report.Configured = false
			report.Detail = strings.Join(degraded, "; ")
			report.Remediation = "add the missing host guarantees or mark unsupported routes intentionally"
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func runHookConform(args []string, stdout io.Writer) error {
	jsonOutput := false
	paths := []string{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc hook conform <manifest.json> <fixtures.json> [--json]")
			return nil
		case "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook conform: unknown flag %q", arg)}
			}
			paths = append(paths, arg)
		}
	}
	if len(paths) != 2 {
		return &CLIError{ExitCode: 1, Message: "reconc hook conform: expected <manifest.json> <fixtures.json>"}
	}
	manifestBody, err := readStrictConformanceFile(paths[0], customruntime.MaxManifestBytes)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook conform: " + err.Error()}
	}
	manifest, err := customruntime.DecodeManifest(manifestBody)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook conform: " + err.Error()}
	}
	fixtureBody, err := readStrictConformanceFile(paths[1], customruntime.MaxConformanceBytes)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook conform: " + err.Error()}
	}
	suite, err := customruntime.DecodeConformanceSuite(fixtureBody)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook conform: " + err.Error()}
	}
	report, err := customruntime.RunConformance(manifest, suite)
	if err != nil {
		return &CLIError{ExitCode: 2, Message: "reconc hook conform: " + err.Error()}
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	fmt.Fprintf(stdout, "%s: PASS (%d cases; %s)\n", report.Runtime, report.CaseCount, strings.Join(conformanceCapabilities(report.Capabilities), ", "))
	return nil
}

func readStrictConformanceFile(path string, maxBytes int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a non-symlink regular file", path)
	}
	body, err := boundedio.ReadFile(path, int64(maxBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return body, nil
}

func conformanceCapabilities(values []customruntime.ConformanceCapability) []string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = string(value)
	}
	return out
}

func runHookBridge(args []string, input io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, "Usage: reconc hook bridge <runtime> <host-event> [repo]   (reads host JSON from stdin)")
		return nil
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook bridge: unknown flag %q", arg)}
		}
	}
	if len(args) < 2 || len(args) > 3 {
		return &CLIError{ExitCode: 1, Message: "reconc hook bridge: expected <runtime> <host-event> [repo]"}
	}
	name := strings.TrimPrefix(args[0], "custom:")
	hostEvent := args[1]
	repo := "."
	if len(args) == 3 {
		repo = args[2]
	}
	root, err := agentsession.ResolveRepoRootRef(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook bridge: " + err.Error()}
	}
	evaluator := runtime.NewEvaluator()
	compiledEvaluator, _, err := evaluator.CurrentCompiledPolicyEvaluator(root.Path())
	if err != nil {
		return &CLIError{ExitCode: 2, Message: "reconc hook bridge: " + err.Error()}
	}
	manifest, err := loadCustomRuntime(root.Path(), name)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook bridge: " + err.Error()}
	}
	expectedDigest, ok := compiledEvaluator.CustomRuntimeManifestDigest(manifest.Runtime())
	if !ok || expectedDigest != manifest.Digest() {
		return &CLIError{ExitCode: 2, Message: "reconc hook bridge: manifest bytes do not match the validated compiled runtime identity"}
	}
	route, ok := manifest.Route(hostEvent)
	if !ok {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook bridge: runtime %q has no host event %q", manifest.Runtime(), hostEvent)}
	}
	if reasons := route.DegradedReasons(); len(reasons) > 0 {
		return writeCustomRuntimeResponse(manifest, route, customruntime.BuildResponse(manifest, route, 0, "", "", nil, false), stdout)
	}
	body, err := agentsession.ReadPayload(input)
	if err != nil {
		response := customruntime.BuildResponse(manifest, route, 0, "", "", err, false)
		return writeCustomRuntimeDecision(manifest, route, response, stdout)
	}
	_, payload, err := customruntime.NormalizeHostPayload(manifest, route, body)
	if err != nil {
		response := customruntime.BuildResponse(manifest, route, 0, "", "", err, false)
		return writeCustomRuntimeDecision(manifest, route, response, stdout)
	}
	handler, ok := customRuntimeHandler(route.Event)
	if !ok {
		response := customruntime.BuildResponse(manifest, route, 0, "", "", fmt.Errorf("neutral event %q has no executable handler", route.Event), false)
		return writeCustomRuntimeDecision(manifest, route, response, stdout)
	}
	result := agentsession.RunHookRequestWithEvaluator(root, handler, manifest.Runtime()+"-"+string(route.Event), payload, evaluator)
	if err := agentsession.RecordHookLivenessResolved(root, manifest.LivenessRuntime(), customruntime.LivenessEvent(route.HostEvent)); err != nil {
		if result.Stderr != "" {
			result.Stderr += "; "
		}
		result.Stderr += "reconc custom runtime liveness (warn): " + err.Error()
	}
	response := customruntime.BuildResponse(manifest, route, result.ExitCode, result.Stdout, result.Stderr, nil, false)
	if response.Reason != "" && response.Decision != customruntime.DecisionBlock && response.Decision != customruntime.DecisionContinue {
		fmt.Fprintln(stderr, response.Reason)
	}
	return writeCustomRuntimeDecision(manifest, route, response, stdout)
}

func loadCustomRuntime(repoRoot, name string) (customruntime.Manifest, error) {
	sources, err := ingest.LoadCustomRuntimeSources(repoRoot)
	if err != nil {
		return customruntime.Manifest{}, err
	}
	for _, source := range sources {
		manifest, err := customruntime.DecodeManifest([]byte(source.Content))
		if err != nil {
			return customruntime.Manifest{}, fmt.Errorf("%s: %w", source.Path, err)
		}
		if manifest.Name == name {
			return manifest, nil
		}
	}
	return customruntime.Manifest{}, fmt.Errorf("custom runtime %q is not configured", name)
}

func customRuntimeHandler(event customruntime.Event) (agentsession.HookHandler, bool) {
	switch event {
	case customruntime.EventSessionStart:
		return agentsession.HookHandlerSessionStart, true
	case customruntime.EventPreToolUse:
		return agentsession.HookHandlerPreToolUse, true
	case customruntime.EventPermissionRequest:
		return agentsession.HookHandlerPermissionRequest, true
	case customruntime.EventPostToolUse:
		return agentsession.HookHandlerPostToolUseComplete, true
	case customruntime.EventPostToolUseFailure:
		return agentsession.HookHandlerPostToolUseFailure, true
	case customruntime.EventMCPBefore:
		return agentsession.HookHandlerMCPBefore, true
	case customruntime.EventMCPAfter:
		return agentsession.HookHandlerMCPAfter, true
	case customruntime.EventStop:
		return agentsession.HookHandlerStop, true
	case customruntime.EventSessionEnd:
		return agentsession.HookHandlerSessionEnd, true
	case customruntime.EventPostCompaction:
		return agentsession.HookHandlerPostCompaction, true
	case customruntime.EventWorkspaceOpen:
		return agentsession.HookHandlerWorkspaceOpen, true
	case customruntime.EventUserPromptSubmit, customruntime.EventPermissionResult,
		customruntime.EventInterrupt, customruntime.EventNotification,
		customruntime.EventSubagentStart, customruntime.EventSubagentStop,
		customruntime.EventPreCompaction:
		return agentsession.HookHandlerPassive, true
	default:
		return "", false
	}
}

func writeCustomRuntimeDecision(manifest customruntime.Manifest, route customruntime.Route, response customruntime.NeutralResponse, stdout io.Writer) error {
	if err := writeCustomRuntimeResponse(manifest, route, response, stdout); err != nil {
		return err
	}
	if response.ExitCode != 0 {
		return &CLIError{ExitCode: response.ExitCode, Message: ""}
	}
	return nil
}

func writeCustomRuntimeResponse(_ customruntime.Manifest, route customruntime.Route, response customruntime.NeutralResponse, stdout io.Writer) error {
	body, err := customruntime.BoundResponse(response, route.MaxOutputBytes)
	if err != nil {
		return &CLIError{ExitCode: 2, Message: "reconc hook bridge: " + err.Error()}
	}
	_, err = stdout.Write(body)
	return err
}
