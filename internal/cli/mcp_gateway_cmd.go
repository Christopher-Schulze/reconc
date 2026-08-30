package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/mcpgateway"
	"reconc.dev/reconc/internal/pathidentity"
	productruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

type mcpGatewayOptions struct {
	repository          string
	serverLabel         string
	principal           string
	role                string
	environment         string
	credentialLabels    []string
	runID               string
	sessionID           string
	expectedLockDigest  string
	repositoryManaged   bool
	approvalAuthorities string
	approvalPolicyID    string
	workingDirectory    string
	inheritedEnvNames   []string
	timeout             time.Duration
	reconcHome          string
	command             string
	arguments           []string
}

func runMCP(args []string, version string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc mcp: missing required subcommand"}
	}
	if isHelpFlag(args[0]) {
		fmt.Fprintln(stdout, "Usage: reconc mcp gateway [repo] [flags] -- COMMAND [ARG...]")
		fmt.Fprintln(stdout, "Run an enforcing tools-only MCP stdio gateway.")
		return nil
	}
	if args[0] != "gateway" {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc mcp: unknown subcommand %q", args[0])}
	}
	if len(args) == 2 && isHelpFlag(args[1]) {
		fmt.Fprintln(stdout, "Usage: reconc mcp gateway [repo] --server LABEL (--expect-lock-digest SHA256 | --allow-repository-managed-policy) --principal LABEL [trusted-context flags] -- COMMAND [ARG...]")
		return nil
	}
	return runMCPGateway(args[1:], version, stdout, stderr)
}

func runMCPGateway(args []string, version string, stdout, stderr io.Writer) error {
	options, err := parseMCPGatewayOptions(args)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc mcp gateway: " + err.Error()}
	}
	config := gatewayConfig(options, version, stdout, stderr)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := mcpGatewayRunError(mcpgateway.Run(ctx, config)); err != nil {
		return err
	}
	return nil
}

func mcpGatewayRunError(err error) error {
	if err == nil || isPureContextCancellation(err) {
		return nil
	}
	return &CLIError{ExitCode: 1, Message: "reconc mcp gateway: " + err.Error()}
}

func isPureContextCancellation(err error) bool {
	if err == nil {
		return false
	}
	switch unwrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children := unwrapped.Unwrap()
		if len(children) == 0 {
			return errors.Is(err, context.Canceled)
		}
		for _, child := range children {
			if child == nil || !isPureContextCancellation(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		child := unwrapped.Unwrap()
		if child == nil {
			return errors.Is(err, context.Canceled)
		}
		return isPureContextCancellation(child)
	default:
		return errors.Is(err, context.Canceled)
	}
}

func gatewayConfig(
	options mcpGatewayOptions,
	version string,
	stdout, stderr io.Writer,
) mcpgateway.Config {
	runtimeEvaluator := productruntime.NewEvaluator()
	authority := actionstate.PolicyAuthority{Mode: action.AuthorityRepositoryManaged}
	if options.expectedLockDigest != "" {
		authority = actionstate.PolicyAuthority{
			Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: options.expectedLockDigest,
		}
	}
	return mcpgateway.Config{
		Repository: options.repository, ServerLabel: options.serverLabel,
		PolicyAuthority: authority, Principal: options.principal, Role: options.role,
		Environment: options.environment, CredentialLabels: options.credentialLabels,
		RunID: options.runID, SessionID: options.sessionID,
		ApprovalAuthorities: options.approvalAuthorities, ApprovalPolicyID: options.approvalPolicyID,
		ServerWorkingDir: options.workingDirectory, InheritedEnvNames: options.inheritedEnvNames,
		CallTimeout: options.timeout, Command: options.command, Arguments: options.arguments,
		ReconcHome: options.reconcHome, Version: version,
		Input: os.Stdin, Output: stdout, Diagnostics: stderr,
		PolicyLoader:     gatewayPolicyLoader{evaluator: runtimeEvaluator},
		EvidenceProvider: gatewayEvidenceProvider{evaluator: runtimeEvaluator},
	}
}

func parseMCPGatewayOptions(args []string) (mcpGatewayOptions, error) {
	separator := -1
	for index, argument := range args {
		if argument == "--" {
			separator = index
			break
		}
	}
	options := mcpGatewayOptions{repository: ".", timeout: mcpgateway.DefaultCallTimeout}
	if separator < 0 {
		if err := parseMCPGatewayFlags(args, &options); err != nil {
			return mcpGatewayOptions{}, err
		}
		return mcpGatewayOptions{}, fmt.Errorf("one downstream command is required after --")
	}
	if separator == len(args)-1 {
		return mcpGatewayOptions{}, fmt.Errorf("one downstream command is required after --")
	}
	if err := parseMCPGatewayFlags(args[:separator], &options); err != nil {
		return mcpGatewayOptions{}, err
	}
	options.command = args[separator+1]
	options.arguments = append([]string(nil), args[separator+2:]...)
	if err := validateMCPGatewayOptions(options); err != nil {
		return mcpGatewayOptions{}, err
	}
	return options, nil
}

func parseMCPGatewayFlags(args []string, options *mcpGatewayOptions) error {
	repositorySet := false
	seenFlags := make(map[string]struct{})
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") {
			if repositorySet {
				return fmt.Errorf("at most one repository path is accepted")
			}
			options.repository, repositorySet = argument, true
			continue
		}
		if _, duplicate := seenFlags[argument]; duplicate &&
			argument != "--credential" && argument != "--inherit-env" {
			return fmt.Errorf("flag %s is duplicated", argument)
		}
		seenFlags[argument] = struct{}{}
		if err := parseMCPGatewayFlag(args, &index, options); err != nil {
			return err
		}
	}
	return nil
}

func parseMCPGatewayFlag(args []string, index *int, options *mcpGatewayOptions) error {
	flag := args[*index]
	if flag == "--allow-repository-managed-policy" {
		options.repositoryManaged = true
		return nil
	}
	switch flag {
	case "--server", "--principal", "--role", "--environment", "--credential",
		"--run", "--session", "--expect-lock-digest", "--approval-authorities",
		"--approval-policy", "--server-working-dir", "--inherit-env", "--timeout",
		"--reconc-home":
	default:
		return fmt.Errorf("unknown flag %q", flag)
	}
	value, ok := nextArgValue(args, index, flag, argValueNoLeadingDash)
	if !ok {
		next := *index + 1
		if next < len(args) && strings.HasPrefix(args[next], "--") {
			return fmt.Errorf("%s requires a value before %s", flag, args[next])
		}
		return fmt.Errorf("%s requires a value", flag)
	}
	switch flag {
	case "--server":
		options.serverLabel = value
	case "--principal":
		options.principal = value
	case "--role":
		options.role = value
	case "--environment":
		options.environment = value
	case "--credential":
		options.credentialLabels = append(options.credentialLabels, value)
	case "--run":
		options.runID = value
	case "--session":
		options.sessionID = value
	case "--expect-lock-digest":
		options.expectedLockDigest = value
	case "--approval-authorities":
		options.approvalAuthorities = value
	case "--approval-policy":
		options.approvalPolicyID = value
	case "--server-working-dir":
		options.workingDirectory = value
	case "--inherit-env":
		options.inheritedEnvNames = append(options.inheritedEnvNames, value)
	case "--timeout":
		duration, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("--timeout is invalid: %w", err)
		}
		options.timeout = duration
	case "--reconc-home":
		options.reconcHome = value
	}
	return nil
}

func validateMCPGatewayOptions(options mcpGatewayOptions) error {
	if options.serverLabel == "" || options.principal == "" {
		return fmt.Errorf("--server and --principal are required")
	}
	if (options.expectedLockDigest == "") == !options.repositoryManaged {
		return fmt.Errorf("exactly one of --expect-lock-digest or --allow-repository-managed-policy is required")
	}
	if (options.approvalAuthorities == "") != (options.approvalPolicyID == "") {
		return fmt.Errorf("--approval-authorities and --approval-policy must be configured together")
	}
	if options.command == "" {
		return fmt.Errorf("downstream command is empty")
	}
	return nil
}

type gatewayPolicyLoader struct {
	evaluator *productruntime.Evaluator
}

func (l gatewayPolicyLoader) Load(
	ctx context.Context,
	repository string,
) (mcpgateway.PolicySnapshot, error) {
	if l.evaluator == nil {
		return mcpgateway.PolicySnapshot{}, fmt.Errorf("gateway runtime evaluator is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return mcpgateway.PolicySnapshot{}, err
	}
	discovery, err := ingest.DiscoverPolicyRepo(repository)
	if err != nil {
		return mcpgateway.PolicySnapshot{}, fmt.Errorf("discover policy repository: %w", err)
	}
	if !discovery.Discovered {
		return mcpgateway.PolicySnapshot{}, fmt.Errorf("discover policy repository: no policy markers found")
	}
	root, err := pathidentity.ResolveExisting(discovery.RepoRoot)
	if err != nil {
		return mcpgateway.PolicySnapshot{}, err
	}
	compiled, _, err := l.evaluator.CurrentCompiledPolicyEvaluator(root)
	if err != nil {
		return mcpgateway.PolicySnapshot{}, err
	}
	actionRuntime, err := compiled.ActionRuntime()
	if err != nil {
		return mcpgateway.PolicySnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return mcpgateway.PolicySnapshot{}, err
	}
	return mcpgateway.PolicySnapshot{
		Repository: root, Evaluator: actionRuntime.Evaluator, Plan: actionRuntime.Plan,
		SourceDigest: actionRuntime.SourceDigest, LockDigest: actionRuntime.LockDigest,
		RepositoryEffectCheck: compiledRepositoryEffectCheck(compiled, root),
	}, nil
}

type gatewayEvidenceProvider struct {
	evaluator *productruntime.Evaluator
}

func (p gatewayEvidenceProvider) Observe(
	ctx context.Context,
	snapshot mcpgateway.PolicySnapshot,
	request action.Request,
	tool action.Tool,
) (mcpgateway.EvidenceSnapshot, error) {
	taint, err := gatewayTaint(snapshot.Repository)
	if err != nil {
		return mcpgateway.EvidenceSnapshot{}, err
	}
	evidence := mcpgateway.EvidenceSnapshot{Taint: taint}
	if tool.Effect.Kind != action.EffectRepositoryRead && tool.Effect.Kind != action.EffectRepositoryWrite {
		if err := ctx.Err(); err != nil {
			return mcpgateway.EvidenceSnapshot{}, err
		}
		return evidence, nil
	}
	paths, bindings, err := gatewayRepositoryPaths(snapshot.Repository, request, tool)
	if err != nil {
		return mcpgateway.EvidenceSnapshot{}, err
	}
	evidence.RepositoryPaths = bindings
	if err := ctx.Err(); err != nil {
		return mcpgateway.EvidenceSnapshot{}, err
	}
	if snapshot.RepositoryEffectCheck != nil {
		var readPaths, writePaths []string
		if tool.Effect.Kind == action.EffectRepositoryRead {
			readPaths = paths
		} else {
			writePaths = paths
		}
		candidate, err := snapshot.RepositoryEffectCheck(
			ctx, snapshot.Repository, readPaths, writePaths,
		)
		if err != nil {
			return mcpgateway.EvidenceSnapshot{}, err
		}
		evidence.RepositoryEffect = candidate
	} else {
		inputs := productruntime.Empty()
		if tool.Effect.Kind == action.EffectRepositoryRead {
			inputs.ReadPaths = paths
		} else {
			inputs.WritePaths = paths
		}
		if p.evaluator == nil {
			return mcpgateway.EvidenceSnapshot{}, fmt.Errorf("gateway runtime evaluator is unavailable")
		}
		report, err := p.evaluator.CheckRepoPolicyContext(ctx, snapshot.Repository, inputs)
		if err != nil {
			return mcpgateway.EvidenceSnapshot{}, err
		}
		evidence.RepositoryEffect = repositoryEffectCandidate(report)
	}
	if err := ctx.Err(); err != nil {
		return mcpgateway.EvidenceSnapshot{}, err
	}
	return evidence, nil
}

func compiledRepositoryEffectCheck(
	compiled *productruntime.CompiledPolicyEvaluator,
	root string,
) mcpgateway.RepositoryEffectCheck {
	return func(
		ctx context.Context,
		repository string,
		readPaths, writePaths []string,
	) (*action.RepositoryEffectCandidate, error) {
		if compiled == nil {
			return nil, fmt.Errorf("compiled repository policy evaluator is unavailable")
		}
		if repository != root {
			return nil, fmt.Errorf("repository policy identity changed during evidence evaluation")
		}
		inputs := productruntime.Empty()
		inputs.ReadPaths = append([]string(nil), readPaths...)
		inputs.WritePaths = append([]string(nil), writePaths...)
		report, _, err := compiled.CheckContext(ctx, root, inputs)
		if err != nil {
			return nil, err
		}
		return repositoryEffectCandidate(report), nil
	}
}

func gatewayTaint(repository string) (action.TaintSnapshot, error) {
	status, err := agentsession.ReadEvidenceTaintStatus(repository)
	if err != nil {
		return action.TaintSnapshot{}, err
	}
	if !status.Present {
		return action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"}, nil
	}
	return action.TaintSnapshot{Status: action.TaintPresent, Identity: status.Token}, nil
}

func gatewayRepositoryPaths(
	repository string,
	request action.Request,
	tool action.Tool,
) ([]string, []mcpgateway.RepositoryPathBinding, error) {
	if request.Arguments == nil {
		return nil, nil, fmt.Errorf("repository-effect arguments are unavailable")
	}
	paths := make([]string, 0, len(tool.Effect.PathFields))
	for _, pointer := range tool.Effect.PathFields {
		selected, err := action.ResolvePointer(*request.Arguments, pointer)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve repository-effect path %q: %w", pointer, err)
		}
		if selected.State != action.PointerPresent {
			return nil, nil, fmt.Errorf("resolve repository-effect path %q: value is unavailable", pointer)
		}
		values, err := gatewayPathStrings(selected.Value)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve repository-effect path %q: %w", pointer, err)
		}
		paths = append(paths, values...)
	}
	return normalizeGatewayPaths(repository, paths)
}

func gatewayPathStrings(value action.Value) ([]string, error) {
	if text, ok := value.Text(); ok && text != "" {
		return []string{text}, nil
	}
	items, ok := value.Items()
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("path value must be a non-empty string or string array")
	}
	values := make([]string, len(items))
	for index, item := range items {
		text, ok := item.Text()
		if !ok || text == "" {
			return nil, fmt.Errorf("path array contains a non-string or empty value")
		}
		values[index] = text
	}
	return values, nil
}

func normalizeGatewayPaths(
	repository string,
	values []string,
) ([]string, []mcpgateway.RepositoryPathBinding, error) {
	unique := make(map[string]struct{}, len(values))
	bindings := make(map[string]mcpgateway.RepositoryPathBinding, len(values))
	for _, value := range values {
		candidate := filepath.FromSlash(value)
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(repository, candidate)
		}
		candidate = filepath.Clean(candidate)
		if !gatewayPathWithinRepository(repository, candidate) {
			return nil, nil, fmt.Errorf("repository-effect lexical path escapes the repository")
		}
		resolved, err := pathidentity.ResolveProspective(candidate)
		if err != nil {
			return nil, nil, err
		}
		relative, err := filepath.Rel(repository, resolved)
		if err != nil || relative == "." || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, nil, fmt.Errorf("repository-effect path escapes the repository")
		}
		unique[filepath.ToSlash(relative)] = struct{}{}
		key := candidate + "\x00" + resolved
		bindings[key] = mcpgateway.RepositoryPathBinding{Lexical: candidate, Identity: resolved}
	}
	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("repository-effect path set is empty")
	}
	pathBindings := make([]mcpgateway.RepositoryPathBinding, 0, len(bindings))
	for _, binding := range bindings {
		pathBindings = append(pathBindings, binding)
	}
	sort.Slice(pathBindings, func(i, j int) bool {
		if pathBindings[i].Lexical == pathBindings[j].Lexical {
			return pathBindings[i].Identity < pathBindings[j].Identity
		}
		return pathBindings[i].Lexical < pathBindings[j].Lexical
	})
	return paths, pathBindings, nil
}

func gatewayPathWithinRepository(repository, path string) bool {
	relative, err := filepath.Rel(repository, path)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func repositoryEffectCandidate(report *productruntime.CheckReport) *action.RepositoryEffectCandidate {
	decision := action.DecisionAllow
	if report.Decision == productruntime.DecisionWarn {
		decision = action.DecisionWarn
	} else if report.Decision == productruntime.DecisionBlock {
		decision = action.DecisionBlock
	}
	ruleIDs := append([]string(nil), report.RuleIDs...)
	sort.Strings(ruleIDs)
	ruleIDs = uniqueStrings(ruleIDs)
	return &action.RepositoryEffectCandidate{
		Decision: decision, Reason: action.ReasonRuleMatched,
		RuleIDs: ruleIDs, Complete: true,
	}
}

func uniqueStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}
