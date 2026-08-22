package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func runHook(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc hook: missing subcommand (generate | install | uninstall | status | verify | bridge | conform | sync-scaffold | claim | evidence-status | evidence-resolve)"}
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprintln(stdout, "Usage: reconc hook generate <kind> [--json] [--output PATH]")
		fmt.Fprintln(stdout, "       reconc hook install  <kind> [repo] [--force] [--json] [--output PATH]")
		fmt.Fprintln(stdout, "       reconc hook uninstall <kind> [repo] [--json] [--output PATH]")
		fmt.Fprintln(stdout, "       reconc hook status   [repo] [--json]")
		fmt.Fprintln(stdout, "       reconc hook verify   [--host KIND [--surface SURFACE]] [--json]")
		fmt.Fprintln(stdout, "       reconc hook verify   --live --host KIND --surface SURFACE --allow-authenticated [--json]")
		fmt.Fprintln(stdout, "       reconc hook bridge  <runtime> <host-event> [repo]   (reads host JSON from stdin)")
		fmt.Fprintln(stdout, "       reconc hook conform <manifest.json> <fixtures.json> [--json]")
		fmt.Fprintln(stdout, "       reconc hook sync-scaffold <repo-root-scaffold> [--json]")
		fmt.Fprintln(stdout, "       reconc hook claim    <repo> <claim-name> [--session ID] [--json] [--output PATH]")
		fmt.Fprintln(stdout, "       reconc hook evidence-status [repo] [--json]")
		fmt.Fprintln(stdout, "       reconc hook evidence-resolve <repo> --token TOKEN --reason TEXT [--json]")
		fmt.Fprintln(stdout, "")
		fmt.Fprintf(stdout, "Kinds: %s (all installable)\n", strings.Join(hooks.SupportedKinds(), ", "))
		fmt.Fprintln(stdout, "Kimi Code hooks are user-global; install or uninstall kimi-code without a repo argument.")
		return nil
	case "generate":
		return runHookGenerate(args[1:], stdout, stderr)
	case "install":
		return runHookInstall(args[1:], stdout, stderr)
	case "uninstall":
		return runHookUninstall(args[1:], stdout)
	case "status":
		return runHookStatus(args[1:], stdout)
	case "verify":
		return runHookVerify(args[1:], stdout, stderr)
	case "__verify-offline":
		return runHookVerificationOfflineChild(args[1:], stdout)
	case "__verify-live-setup":
		return runHookVerificationLiveSetupChild(args[1:], stdout)
	case "sync-scaffold":
		return runHookSyncScaffold(args[1:], stdout, stderr)
	case "runtime":
		return runHookRuntime(args[1:], stdout, stderr)
	case "bridge":
		return runHookBridge(args[1:], os.Stdin, stdout, stderr)
	case "conform":
		return runHookConform(args[1:], stdout)
	case "worker":
		return runHookWorker(args[1:], os.Stdin, stdout)
	case "kimi-runtime":
		return runKimiCodeRuntime(args[1:], stdout, stderr)
	case "grok-pre-tool-guard":
		return runGrokPreToolGuard(args[1:], stdout, stderr)
	case "claim":
		return runHookClaim(args[1:], stdout, stderr)
	case "evidence-status":
		return runHookEvidenceStatus(args[1:], stdout)
	case "evidence-resolve":
		return runHookEvidenceResolve(args[1:], stdout)
	}
	return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook: unknown subcommand %q (expected generate | install | uninstall | status | verify | bridge | conform | sync-scaffold | claim | evidence-status | evidence-resolve)", args[0])}
}

func runHookEvidenceStatus(args []string, stdout io.Writer) error {
	repo := "."
	jsonOut := false
	repoSet := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc hook evidence-status [repo] [--json]")
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook evidence-status: unknown flag %q", arg)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc hook evidence-status: accepts at most one repo path"}
			}
			repo = arg
			repoSet = true
		}
	}
	status, err := agentsession.ReadEvidenceTaintStatus(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook evidence-status: " + err.Error()}
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	}
	if !status.Present {
		fmt.Fprintln(stdout, "evidence taint: none")
		return nil
	}
	fmt.Fprintf(stdout, "evidence taint: %s/%s session=%s token=%s\n", status.Field, status.Limit, status.SessionID, status.Token)
	return nil
}

func runHookEvidenceResolve(args []string, stdout io.Writer) error {
	repo := ""
	token := ""
	reason := ""
	jsonOut := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc hook evidence-resolve <repo> --token TOKEN --reason TEXT [--json]")
			return nil
		case "--token":
			if index+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc hook evidence-resolve: --token requires a value"}
			}
			index++
			token = args[index]
		case "--reason":
			if index+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc hook evidence-resolve: --reason requires a value"}
			}
			index++
			reason = args[index]
		default:
			if strings.HasPrefix(args[index], "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook evidence-resolve: unknown flag %q", args[index])}
			}
			if repo != "" {
				return &CLIError{ExitCode: 1, Message: "reconc hook evidence-resolve: accepts exactly one repo path"}
			}
			repo = args[index]
		}
	}
	if repo == "" || token == "" || reason == "" {
		return &CLIError{ExitCode: 1, Message: "reconc hook evidence-resolve: usage: reconc hook evidence-resolve <repo> --token TOKEN --reason TEXT [--json]"}
	}
	status, err := agentsession.ResolveEvidenceTaint(repo, token, reason)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook evidence-resolve: " + err.Error()}
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	}
	fmt.Fprintf(stdout, "resolved evidence taint %s (%s/%s); start a fresh session and reproduce required evidence\n", status.Token, status.Field, status.Limit)
	return nil
}

func runHookStatus(args []string, stdout io.Writer) error {
	repo := "."
	repoSet := false
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc hook status [repo] [--json]")
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook status: unknown flag %q", arg)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc hook status: accepts at most one repo path"}
			}
			repo = arg
			repoSet = true
		}
	}
	reports, err := hooks.InspectPlatforms(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook status: " + err.Error()}
	}
	customReports, err := inspectCustomRuntimeStatuses(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook status: custom runtimes: " + err.Error()}
	}
	reports = append(reports, customReports...)
	liveness, err := agentsession.ReadHookLiveness(repo)
	if err != nil {
		for i := range reports {
			reports[i].LivenessError = err.Error()
		}
	} else {
		for i := range reports {
			if live, ok := liveness[hookRuntimeName(reports[i].Kind)]; ok {
				reports[i].LastSeen = live.LastSeen
				reports[i].LastEvent = live.Event
				if len(live.Observations) > 0 {
					reports[i].Observations = make(map[string]hooks.HookObservationStatus, len(live.Observations))
					for event, observation := range live.Observations {
						reports[i].Observations[event] = hooks.HookObservationStatus{
							Count:              observation.Count,
							LastSeen:           observation.LastSeen,
							WorkingDirectory:   observation.WorkingDirectory,
							CodeBytes:          observation.CodeBytes,
							ExcludeFromContext: observation.ExcludeFromContext,
						}
					}
				}
				for _, event := range reports[i].ExpectedEvents {
					if _, seen := live.Routes[event]; seen {
						reports[i].LiveEvents = append(reports[i].LiveEvents, event)
					} else {
						reports[i].UnseenEvents = append(reports[i].UnseenEvents, event)
					}
				}
			} else {
				reports[i].UnseenEvents = append(reports[i].UnseenEvents, reports[i].ExpectedEvents...)
			}
			reports[i].Live = reports[i].LastSeen != ""
		}
	}
	enrichMCPPlatformStatus(repo, reports)
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(reports); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook status: json encode: " + err.Error()}
		}
		return nil
	}
	style := newTextStyler(stdout)
	for _, report := range reports {
		live := "never seen"
		if report.LastSeen != "" {
			live = fmt.Sprintf("routes %d/%d seen; last %s", len(report.LiveEvents), len(report.ExpectedEvents), report.LastSeen)
		} else if report.LivenessError != "" {
			live = "liveness unavailable: " + report.LivenessError
		}
		fmt.Fprintf(stdout, "%s: %s (%s; %s)\n", report.Kind, style.decision(string(report.State)), report.Detail, live)
		fmt.Fprintf(stdout, "  generated=%t installed=%t executable=%t configured=%t live=%t\n",
			report.Generated, report.Installed, report.Executable, report.Configured, report.Live)
		observationEvents := make([]string, 0, len(report.Observations))
		for event := range report.Observations {
			observationEvents = append(observationEvents, event)
		}
		sort.Strings(observationEvents)
		for _, event := range observationEvents {
			observation := report.Observations[event]
			fmt.Fprintf(stdout, "  observation %s: count=%d last=%s cwd=%s code_bytes=%d exclude_from_context=%t\n",
				event, observation.Count, observation.LastSeen, observation.WorkingDirectory,
				observation.CodeBytes, observation.ExcludeFromContext)
		}
		if report.Remediation != "" {
			fmt.Fprintf(stdout, "  remediation: %s\n", report.Remediation)
		}
		if report.MCP != nil {
			fmt.Fprintf(stdout, "  mcp: mode=%s mappings=%d classified=%d unclassified=%d denied=%d failures=%d strict-deny=%t\n",
				report.MCP.UnclassifiedMode, len(report.MCP.Mappings), report.MCP.ClassifiedObserved,
				report.MCP.UnclassifiedObserved, report.MCP.Denied, report.MCP.Failures,
				report.MCP.StrictUnclassifiedDeny)
			if report.MCP.Limitation != "" {
				fmt.Fprintf(stdout, "  mcp limitation: %s\n", report.MCP.Limitation)
			}
			if report.MCP.ObservationError != "" {
				fmt.Fprintf(stdout, "  mcp observations unavailable: %s\n", report.MCP.ObservationError)
			}
		}
	}
	return nil
}

// mcpDiscriminatorAvailable reports whether a host lets Reconc tell an MCP call
// apart from a built-in or custom tool before it executes. Cursor publishes a
// dedicated MCP event; Claude Code and Codex publish the `mcp__<server>__<tool>`
// namespace on their generic tool events and accept a matcher for it. Only on
// those hosts can `unclassified: deny` be enforced instead of reported as a
// limitation.
func mcpDiscriminatorAvailable(platform policy.MCPPlatform) bool {
	switch platform {
	case policy.MCPPlatformCursor, policy.MCPPlatformClaudeCode, policy.MCPPlatformCodex:
		return true
	default:
		return false
	}
}

func enrichMCPPlatformStatus(repo string, reports []hooks.PlatformStatus) {
	contract, contractErr := runtime.LoadMCPPolicy(repo)
	audit, auditErr := agentsession.ReadMCPAudit(repo)
	for index := range reports {
		platform := policy.MCPPlatform(reports[index].Kind)
		if !platform.Valid() {
			continue
		}
		status := &hooks.MCPStatus{
			UnclassifiedMode:       string(policy.MCPUnclassifiedHost),
			Mappings:               []hooks.MCPMappingStatus{},
			StrictUnclassifiedDeny: mcpDiscriminatorAvailable(platform),
		}
		if strings.HasPrefix(string(platform), "custom:") {
			status.Limitation = "custom MCP enforcement requires a non-degraded manifest route with exact host MCP identity; unsupported routes never dispatch"
		} else if !mcpDiscriminatorAvailable(platform) {
			status.Limitation = "generic tool hooks expose an exact host tool identity but no MCP discriminator; configured identities are enforceable, unconfigured MCP calls cannot be distinguished from built-in or custom tools"
		}
		if contractErr != nil {
			status.ObservationError = "policy: " + contractErr.Error()
		} else if contract != nil {
			status.UnclassifiedMode = string(contract.Unclassified)
			for _, mapping := range contract.Tools {
				if mapping.Platform != platform {
					continue
				}
				status.Mappings = append(status.Mappings, hooks.MCPMappingStatus{
					Tool:              mapping.Tool,
					ServerFingerprint: mapping.ServerFingerprint,
					Effect:            string(mapping.Effect),
					SourcePath:        mapping.SourcePath,
				})
			}
		}
		if auditErr != nil {
			if status.ObservationError != "" {
				status.ObservationError += "; "
			}
			status.ObservationError += "audit: " + auditErr.Error()
		} else {
			prefix := string(platform) + "/"
			for key, count := range audit.Classified {
				if strings.HasPrefix(key, prefix) {
					status.ClassifiedObserved += count
				}
			}
			status.UnclassifiedObserved = audit.Unclassified[string(platform)]
			status.Denied = audit.Denied[string(platform)]
			status.Failures = audit.Failures[string(platform)]
			status.StrictUnavailable = audit.StrictUnavailable[string(platform)]
		}
		reports[index].MCP = status
	}
}

func hookRuntimeName(kind string) string {
	if strings.HasPrefix(kind, "custom:") {
		return "custom-" + strings.TrimPrefix(kind, "custom:")
	}
	switch kind {
	case hooks.KindClaudeCode:
		return "claude"
	case hooks.KindDevinCLI:
		return "devin"
	default:
		return kind
	}
}
