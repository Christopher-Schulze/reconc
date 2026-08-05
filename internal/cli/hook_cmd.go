package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"reconc.dev/reconc/internal/grokacp"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

// runHook routes hook platform management and runtime events to the hooks and
// agent-session packages.
func runHook(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc hook: missing subcommand (generate | install | uninstall | status | verify | sync-scaffold | claim | evidence-status | evidence-resolve)"}
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprintln(stdout, "Usage: reconc hook generate <kind> [--json] [--output PATH]")
		fmt.Fprintln(stdout, "       reconc hook install  <kind> [repo] [--force] [--json] [--output PATH]")
		fmt.Fprintln(stdout, "       reconc hook uninstall <kind> [repo] [--json] [--output PATH]")
		fmt.Fprintln(stdout, "       reconc hook status   [repo] [--json]")
		fmt.Fprintln(stdout, "       reconc hook verify   [--host KIND [--surface SURFACE]] [--json]")
		fmt.Fprintln(stdout, "       reconc hook verify   --live --host KIND --surface SURFACE --allow-authenticated [--json]")
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
	case "sync-scaffold":
		return runHookSyncScaffold(args[1:], stdout, stderr)
	case "runtime":
		return runHookRuntime(args[1:], stdout, stderr)
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
	return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook: unknown subcommand %q (expected generate | install | uninstall | status | verify | sync-scaffold | claim | evidence-status | evidence-resolve)", args[0])}
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
			StrictUnclassifiedDeny: platform == policy.MCPPlatformCursor,
		}
		if platform != policy.MCPPlatformCursor {
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
	switch kind {
	case hooks.KindClaudeCode:
		return "claude"
	case hooks.KindDevinCLI:
		return "devin"
	default:
		return kind
	}
}

func runHookGenerate(args []string, stdout, stderr io.Writer) (resultErr error) {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook generate: missing kind (one of: %v)", hooks.SupportedKinds())}
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(stdout, "Usage: reconc hook generate <kind> [--json] [--output PATH]\nKinds: %v\n", hooks.SupportedKinds())
		return nil
	}
	kind := args[0]
	jsonOut := false
	outputPath := ""
	i := 1
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook generate: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintf(stdout, "Usage: reconc hook generate <kind> [--json] [--output PATH]\nKinds: %v\n", hooks.SupportedKinds())
			return nil
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook generate: unknown flag %q", a)}
		}
		i++
	}
	a, err := hooks.Generate(kind)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook generate: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook generate: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(a); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook generate: json encode: " + err.Error()}
		}
		return nil
	}
	// Write the raw artifact content to stdout so users can redirect.
	fmt.Fprint(out, a.Content)
	return nil
}

func runHookInstall(args []string, stdout, stderr io.Writer) (resultErr error) {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook install: missing kind (one of: %v)", hooks.InstallableKinds())}
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(stdout, "Usage: reconc hook install <kind> [repo] [--force] [--json] [--output PATH]\nInstallable: %v\n", hooks.InstallableKinds())
		return nil
	}
	kind := args[0]
	repo := "."
	repoSet := false
	force := false
	jsonOut := false
	outputPath := ""
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--force":
			force = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook install: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintf(stdout, "Usage: reconc hook install <kind> [repo] [--force] [--json] [--output PATH]\nInstallable: %v\n", hooks.InstallableKinds())
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook install: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc hook install: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
	}
	if kind == hooks.KindKimiCode && repoSet {
		return &CLIError{ExitCode: 1, Message: "reconc hook install: kimi-code is user-global and does not accept a repo path"}
	}
	report, installErr := hooks.Install(kind, repo, force)
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook install: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)
	if jsonOut && report != nil {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook install: json encode: " + err.Error()}
		}
	} else if report != nil {
		fmt.Fprintf(out, "Installed %s hook (%s)\n", report.Kind, report.Action)
		scopeLabel := "Repo"
		if report.RepoRoot == "global" {
			scopeLabel = "Scope"
		}
		fmt.Fprintf(out, "%-8s %s\n", scopeLabel+":", report.RepoRoot)
		fmt.Fprintf(out, "Target:  %s\n", report.TargetPath)
		if report.WrapperPath != "" {
			fmt.Fprintf(out, "Wrapper: %s (%s)\n", report.WrapperPath, report.WrapperAction)
		}
		if report.ActivationPath != "" {
			fmt.Fprintf(out, "Activate: %s (%s)\n", report.ActivationPath, report.ActivationAction)
		}
		fmt.Fprintf(out, "Next:    %s\n", report.NextAction)
	}
	if installErr != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook install: " + installErr.Error()}
	}
	// Surface any user-modified reconc entries that got overwritten so
	// operators notice.
	if len(report.DroppedUserEdits) > 0 {
		fmt.Fprintf(stderr, "reconc hook install: replaced %d user-modified reconc entr(y/ies):\n",
			len(report.DroppedUserEdits))
		for _, e := range report.DroppedUserEdits {
			fmt.Fprintf(stderr, "  - %s\n", e)
		}
		fmt.Fprintln(stderr, "  (If this was intentional, redo the edit via a wrapper command)")
	}
	if report.BackupPath != "" {
		fmt.Fprintf(stderr, "reconc hook install: replaced managed configuration; original preserved at %s\n",
			report.BackupPath)
	}
	return nil
}

func runHookUninstall(args []string, stdout io.Writer) (resultErr error) {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook uninstall: missing kind (one of: %v)", hooks.InstallableKinds())}
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(stdout, "Usage: reconc hook uninstall <kind> [repo] [--json] [--output PATH]\nUninstallable: %v\n", hooks.InstallableKinds())
		return nil
	}
	kind := args[0]
	repo := "."
	repoSet := false
	jsonOut := false
	outputPath := ""
	for index := 1; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--json":
			jsonOut = true
		case "--output":
			value, ok := nextArgValue(args, &index, arg)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: --output requires a path"}
			}
			outputPath = value
		case "-h", "--help":
			fmt.Fprintf(stdout, "Usage: reconc hook uninstall <kind> [repo] [--json] [--output PATH]\nUninstallable: %v\n", hooks.InstallableKinds())
			return nil
		default:
			if strings.HasPrefix(arg, "-") {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook uninstall: unknown flag %q", arg)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: accepts at most one repo path"}
			}
			repo = arg
			repoSet = true
		}
	}
	if kind == hooks.KindKimiCode && repoSet {
		return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: kimi-code is user-global and does not accept a repo path"}
	}
	report, err := hooks.Uninstall(kind, repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)
	if jsonOut {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook uninstall: json encode: " + err.Error()}
		}
		return nil
	}
	fmt.Fprintf(out, "Uninstalled %s hook (%s)\n", report.Kind, report.Action)
	scopeLabel := "Repo"
	if report.RepoRoot == "global" {
		scopeLabel = "Scope"
	}
	fmt.Fprintf(out, "%-11s %s\n", scopeLabel+":", report.RepoRoot)
	fmt.Fprintf(out, "Target:     %s\n", report.TargetPath)
	fmt.Fprintf(out, "Entries:    %d removed\n", report.RemovedEntries)
	if report.ActivationAction != "" {
		fmt.Fprintf(out, "Activation: %s\n", report.ActivationAction)
	}
	if report.WrapperPath != "" {
		fmt.Fprintf(out, "Wrapper:    %s (%s)\n", report.WrapperPath, report.WrapperAction)
	}
	fmt.Fprintf(out, "Next:       %s\n", report.NextAction)
	return nil
}

func runHookSyncScaffold(args []string, stdout, stderr io.Writer) error {
	scaffoldRoot := ""
	jsonOut := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc hook sync-scaffold <repo-root-scaffold> [--json]")
			fmt.Fprintln(stdout, "Writes every registry-managed hook artifact into a repo-root scaffold.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook sync-scaffold: unknown flag %q", a)}
			}
			if scaffoldRoot != "" {
				return &CLIError{ExitCode: 1, Message: "reconc hook sync-scaffold: accepts exactly one scaffold path"}
			}
			scaffoldRoot = a
		}
	}
	if scaffoldRoot == "" {
		return &CLIError{ExitCode: 1, Message: "reconc hook sync-scaffold: missing repo-root-scaffold path"}
	}
	report, err := hooks.SyncRepoRootScaffold(scaffoldRoot)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook sync-scaffold: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook sync-scaffold: json encode: " + err.Error()}
		}
		return nil
	}
	fmt.Fprintf(stdout, "Synced hook scaffold: %s\n", report.ScaffoldRoot)
	for _, artifact := range report.Artifacts {
		fmt.Fprintf(stdout, "- %s: %s -> %s\n", artifact.Kind, artifact.Action, artifact.TargetPath)
	}
	return nil
}

// runHookRuntime dispatches `reconc hook runtime <event> <repo>` to
// the agent-session adapter. Reads a JSON payload from stdin, runs
// the per-event handler, and translates the Result into exit code +
// stdout/stderr.
//
// dedupToFirstClassRoute suppresses a cross-runtime duplicate event only
// when the first-class platform's hooks are actually installed in this
// repository. Without that gate, a stray environment variable
// (DEVIN_PROJECT_DIR) or overlapping payload field names could silently
// no-op the ONLY enforcement route in arbitrary repos. The dedup is
// recorded (stderr note + liveness) so `reconc hook status` reflects
// activity instead of showing dead routes.
func dedupToFirstClassRoute(root agentsession.ResolvedRepoRoot, configRelPath, event string, stderr io.Writer) bool {
	if _, err := os.Stat(filepath.Join(root.Path(), filepath.FromSlash(configRelPath))); err != nil {
		return false
	}
	fmt.Fprintf(stderr, "reconc hook runtime: %s deduplicated; first-class %s owns this event\n", event, configRelPath)
	_ = agentsession.RecordHookLivenessResolved(root, event, event)
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
		if route.PlatformKind == hooks.KindGitHubCopilot && (route.Event == hooks.EventStop || route.Event == hooks.EventSubagentStop) {
			writeGitHubCopilotRuntimeBlock(stdout, "Reconc could not read the GitHub Copilot hook payload: "+err.Error())
			return nil
		}
		if route.PlatformKind == hooks.KindGrok && route.Event == hooks.EventPreToolUse {
			writeGrokRuntimeDeny(stdout, "Reconc could not read the Grok hook payload: "+err.Error())
			return nil
		}
		if route.ErrorPolicy == hooks.FailureBlock {
			exitCode = 2
			return &CLIError{ExitCode: 2, Message: "reconc hook runtime: " + err.Error()}
		}
		fmt.Fprintln(stderr, "reconc hook runtime warning: "+err.Error())
		return nil
	}
	timing.mark("payload_read")
	root, err := resolveRoot(repo)
	if err != nil {
		if route.PlatformKind == hooks.KindGitHubCopilot && (route.Event == hooks.EventStop || route.Event == hooks.EventSubagentStop) {
			writeGitHubCopilotRuntimeBlock(stdout, "Reconc rejected the GitHub Copilot repository root: "+err.Error())
			return nil
		}
		if route.PlatformKind == hooks.KindGrok && route.Event == hooks.EventPreToolUse {
			writeGrokRuntimeDeny(stdout, "Reconc rejected the Grok repository root: "+err.Error())
			return nil
		}
		if route.ErrorPolicy == hooks.FailureBlock {
			exitCode = 2
			return &CLIError{ExitCode: 2, Message: "reconc hook runtime: " + err.Error()}
		}
		fmt.Fprintln(stderr, "reconc hook runtime warning: "+err.Error())
		return nil
	}
	repo = root.Path()
	timing.mark("root_resolve")
	if route.PlatformKind != hooks.KindCursor && agentsession.PayloadLooksLikeCursor(payload) {
		if dedupToFirstClassRoute(root, hooks.CursorHooksPath, event, stderr) {
			return nil
		}
	}
	if route.PlatformKind != hooks.KindDevinCLI && agentsession.PayloadLooksLikeDevin(payload) {
		if dedupToFirstClassRoute(root, hooks.DevinHooksPath, event, stderr) {
			return nil
		}
	}
	if route.PlatformKind != hooks.KindGrok && agentsession.PayloadLooksLikeGrok(payload) {
		if hooks.HasManagedGrokHook(repo) {
			fmt.Fprintf(stderr, "reconc hook runtime: %s deduplicated; first-class %s owns this event\n", event, hooks.GrokHooksPath)
			_ = agentsession.RecordHookLivenessResolved(root, event, event)
			return nil
		}
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
	}
	if err != nil {
		if route.PlatformKind == hooks.KindGitHubCopilot && (route.Event == hooks.EventStop || route.Event == hooks.EventSubagentStop) {
			writeGitHubCopilotRuntimeBlock(stdout, "Reconc rejected the GitHub Copilot hook payload: "+err.Error())
			return nil
		}
		if route.PlatformKind == hooks.KindGrok && route.Event == hooks.EventPreToolUse {
			writeGrokRuntimeDeny(stdout, "Reconc rejected the Grok hook payload: "+err.Error())
			return nil
		}
		if route.ErrorPolicy == hooks.FailureBlock {
			exitCode = 2
			return &CLIError{ExitCode: 2, Message: "reconc hook runtime: " + err.Error()}
		}
		fmt.Fprintln(stderr, "reconc hook runtime warning: "+err.Error())
		return nil
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
	result := agentsession.RunHookRequestWithEvaluator(root, handler, event, payload, evaluator)
	timing.mark("handler")
	if result.ExitCode != 0 && route.ErrorPolicy == hooks.FailureAllow {
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

func writeGitHubCopilotRuntimeBlock(stdout io.Writer, reason string) {
	body, _ := json.Marshal(map[string]string{
		"decision": "block",
		"reason":   strings.TrimSpace(reason),
	})
	fmt.Fprintln(stdout, string(body))
}

func writeGrokRuntimeDeny(stdout io.Writer, reason string) {
	body, _ := json.Marshal(map[string]string{
		"decision": "deny",
		"reason":   strings.TrimSpace(reason),
	})
	fmt.Fprintln(stdout, string(body))
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
		if route.PlatformKind == hooks.KindOpenCode || route.PlatformKind == hooks.KindKilo || route.PlatformKind == hooks.KindOMP || route.PlatformKind == hooks.KindPi {
			return agentsession.HookHandlerMCPAwarePreToolUse, true
		}
		return agentsession.HookHandlerPreToolUse, true
	case hooks.EventPermissionRequest:
		if route.PlatformKind == hooks.KindOMP {
			return agentsession.HookHandlerPassive, true
		}
		return agentsession.HookHandlerPermissionRequest, true
	case hooks.EventPostToolUse:
		if event == "opencode-post-tool-use" || event == "kilo-post-tool-use" || event == "omp-post-tool-use" || event == "pi-post-tool-use" {
			return agentsession.HookHandlerMCPAwarePostToolUse, true
		}
		if event == "codex-post-tool-use" || event == "devin-post-tool-use" {
			return agentsession.HookHandlerPostToolUseComplete, true
		}
		return agentsession.HookHandlerPostToolUse, true
	case hooks.EventPostToolUseFailure:
		if route.PlatformKind == hooks.KindOpenCode || route.PlatformKind == hooks.KindKilo || route.PlatformKind == hooks.KindOMP || route.PlatformKind == hooks.KindPi {
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

func boundHookResult(result agentsession.Result, route hooks.RuntimeRoute) agentsession.Result {
	limit := route.MaxOutputBytes
	if limit <= 0 {
		return result
	}
	stderrLimit := limit / 2
	stdoutLimit := limit - stderrLimit
	result.Stderr = truncateWithSuffix(result.Stderr, stderrLimit, "\n[reconc stderr truncated]")
	if len(result.Stdout) <= stdoutLimit {
		return result
	}
	result.Stdout = ""
	result.Stderr = truncateUTF8("reconc hook output exceeded the platform byte budget", limit)
	if route.ErrorPolicy == hooks.FailureBlock {
		result.ExitCode = 2
	} else {
		result.ExitCode = 0
	}
	return result
}

func truncateWithSuffix(value string, limit int, suffix string) string {
	if len(value) <= limit {
		return value
	}
	if limit <= len(suffix) {
		return truncateUTF8(value, limit)
	}
	return truncateUTF8(value, limit-len(suffix)) + suffix
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

type hookRuntimeTiming struct {
	enabled    bool
	event      string
	diagnostic io.Writer
	probe      *os.File
	startedAt  time.Time
	lastMark   time.Time
	stages     []string
}

var hookTimingProbeFile = func(fd int) *os.File {
	return os.NewFile(uintptr(fd), "reconc-hook-timing")
}

func newHookRuntimeTiming(event string, stderr io.Writer) hookRuntimeTiming {
	diagnosticEnabled := strings.TrimSpace(os.Getenv("RECONC_HOOK_TIMING")) != "" ||
		strings.TrimSpace(os.Getenv("RECONC_HOOK_TIMING_THRESHOLD_MS")) != ""
	var probe *os.File
	if rawFD := strings.TrimSpace(os.Getenv("RECONC_HOOK_TIMING_FD")); rawFD != "" {
		fd, err := atoi(rawFD)
		if err == nil && fd >= 3 && fd <= 4096 {
			probe = hookTimingProbeFile(fd)
		}
	}
	if !diagnosticEnabled && probe == nil {
		return hookRuntimeTiming{}
	}
	now := time.Now()
	var diagnostic io.Writer
	if diagnosticEnabled {
		diagnostic = stderr
	}
	return hookRuntimeTiming{
		enabled:    true,
		event:      event,
		diagnostic: diagnostic,
		probe:      probe,
		startedAt:  now,
		lastMark:   now,
	}
}

func (t *hookRuntimeTiming) mark(name string) {
	if t == nil || !t.enabled {
		return
	}
	now := time.Now()
	t.stages = append(t.stages, fmt.Sprintf("%s=%s", name, now.Sub(t.lastMark).Round(time.Microsecond)))
	t.lastMark = now
}

func (t *hookRuntimeTiming) finish(exitCode int) {
	if t == nil || !t.enabled {
		return
	}
	total := time.Since(t.startedAt).Round(time.Microsecond)
	if t.probe != nil {
		fmt.Fprintf(t.probe, "duration_ns=%d\n", total.Nanoseconds())
		_ = t.probe.Close()
		t.probe = nil
	}
	if t.diagnostic == nil {
		return
	}
	if threshold := hookRuntimeTimingThreshold(); threshold > 0 && total < threshold {
		return
	}
	parts := []string{
		"reconc hook timing:",
		"event=" + t.event,
		fmt.Sprintf("exit=%d", exitCode),
		"total=" + total.String(),
	}
	parts = append(parts, t.stages...)
	fmt.Fprintln(t.diagnostic, strings.Join(parts, " "))
}

func hookRuntimeTimingThreshold() time.Duration {
	raw := strings.TrimSpace(os.Getenv("RECONC_HOOK_TIMING_THRESHOLD_MS"))
	if raw == "" {
		return 0
	}
	ms, err := atoi(raw)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func isObservationOnlyHookEvent(event string) bool {
	route, ok := hooks.RuntimeEvent(event)
	if !ok {
		return false
	}
	if route.PlatformKind == hooks.KindCursor &&
		(route.Event == hooks.EventUserPromptSubmit || route.Event == hooks.EventSubagentStart) {
		return false
	}
	switch route.Event {
	case hooks.EventUserPromptSubmit,
		hooks.EventPermissionDenied,
		hooks.EventPermissionResult,
		hooks.EventPostToolUse,
		hooks.EventPostToolUseFailure,
		hooks.EventToolObservation,
		hooks.EventContinuation,
		hooks.EventStopFailure,
		hooks.EventInterrupt,
		hooks.EventSessionEnd,
		hooks.EventNotification,
		hooks.EventSubagentStart,
		hooks.EventSubagentStop,
		hooks.EventPreCompaction,
		hooks.EventPostCompaction,
		hooks.EventWorkspaceOpen:
		return true
	default:
		return false
	}
}

// runHookClaim appends one explicit claim to the active session state.
func runHookClaim(args []string, stdout, stderr io.Writer) (resultErr error) {
	repo := ""
	claim := ""
	sessionID := ""
	jsonOut := false
	outputPath := ""
	positional := 0
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc hook claim <repo> <claim-name> [--session ID] [--json] [--output PATH]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Records a claim (e.g. 'ci-green') in the active session state so")
			fmt.Fprintln(stdout, "subsequent require_claim rules see it.")
			return nil
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc hook claim: --output requires a path"}
			}
			outputPath = val
		case "--session":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc hook claim: --session requires a value"}
			}
			sessionID = args[i+1]
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook claim: unknown flag %q", a)}
			}
			switch positional {
			case 0:
				repo = a
			case 1:
				claim = a
			default:
				return &CLIError{ExitCode: 1, Message: "reconc hook claim: too many positional arguments (expected <repo> <claim-name>)"}
			}
			positional++
		}
		i++
	}
	if repo == "" || claim == "" {
		return &CLIError{ExitCode: 1, Message: "reconc hook claim: usage: reconc hook claim <repo> <claim-name> [--session ID] [--json]"}
	}

	report, err := agentsession.RecordClaim(repo, claim, sessionID)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook claim: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook claim: open output file: " + err.Error()}
	}
	defer joinOutputCloseError(&resultErr, closeOutput)

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintln(out, agentsession.DescribeClaimReport(report))
	return nil
}
