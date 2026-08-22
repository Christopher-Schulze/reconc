package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/assurance"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/shellcommand"
)

// evalContext owns the root-bound path state and evaluation-local matcher,
// command, and evidence caches shared across one policy evaluation.
type evalContext struct {
	repoRoot         string
	paths            *evaluationPathState
	rawCommands      []string
	currentCommands  []string
	preCommand       bool
	matchers         *runtimePathMatchers
	templateMatchers *runtimeTemplateMatchers
	commandCache     *commandInvocationCache
	commandEvidence  *commandEvidenceIndex
	evidenceCache    *evidenceSnapshotCache
	evidenceMemo     *evidenceMatchMemo
	contextMemo      *matchContextMemo
}

func (ctx *evalContext) resolvePolicyFile(relative string) (string, error) {
	if ctx != nil && ctx.paths != nil {
		return ctx.paths.resolvePolicyFile(relative)
	}
	return resolvePolicyFile(ctxRepoRoot(ctx), relative)
}

type observedCommandInvocations struct {
	segments []shellcommand.Invocation
	complete bool
}

type commandInvocationCache struct {
	expected           map[string]shellcommand.CompiledExpectation
	normalizedExpected map[string]string
	observed           map[string]observedCommandInvocations
}

type normalizedCommandEvidence struct {
	raw        string
	normalized string
	outcome    string
	epoch      uint64
}

type commandEvidenceIndex struct {
	commands []normalizedCommandEvidence
	results  []normalizedCommandEvidence
}

func newCommandEvidenceIndex(inputs ExecutionInputs, repoRoot string) *commandEvidenceIndex {
	index := &commandEvidenceIndex{
		commands: make([]normalizedCommandEvidence, 0, len(inputs.Commands)),
		results:  make([]normalizedCommandEvidence, 0, len(inputs.CommandResults)),
	}
	for _, command := range inputs.Commands {
		index.commands = append(index.commands, normalizedCommandEvidence{
			raw: command, normalized: normalizeCommandSemantics(command, repoRoot),
		})
	}
	for _, result := range inputs.CommandResults {
		index.results = append(index.results, normalizedCommandEvidence{
			raw: result.Command, normalized: normalizeCommandSemantics(result.Command, repoRoot),
			outcome: result.Outcome, epoch: result.EvidenceEpoch,
		})
	}
	return index
}

func newCommandInvocationCache(rules []policy.Rule, repoRoot string) *commandInvocationCache {
	commands := map[string]struct{}{}
	normalizedExpected := map[string]string{}
	add := func(values []string) {
		for _, value := range values {
			if normalized := normalizeCommandSemantics(value, repoRoot); normalized != "" {
				commands[normalized] = struct{}{}
				normalizedExpected[value] = normalized
			}
		}
	}
	for index := range rules {
		add(rules[index].Commands)
		for assuranceIndex := range rules[index].Assurance {
			add(rules[index].Assurance[assuranceIndex].Commands)
		}
		for checkIndex := range rules[index].Checks {
			add(rules[index].Checks[checkIndex].Commands)
		}
	}
	ordered := make([]string, 0, len(commands))
	for command := range commands {
		ordered = append(ordered, command)
	}
	sort.Strings(ordered)
	expected := make(map[string]shellcommand.CompiledExpectation, len(ordered))
	for _, command := range ordered {
		expected[command] = shellcommand.CompileExpectation(command, 8)
	}
	return &commandInvocationCache{
		expected:           expected,
		normalizedExpected: normalizedExpected,
		observed:           make(map[string]observedCommandInvocations),
	}
}

func (c *commandInvocationCache) normalizedExpectedCommands(expected []string, repoRoot string) []string {
	if c == nil {
		return normalizeExpectedCommands(expected, repoRoot)
	}
	out := make([]string, 0, len(expected))
	for _, value := range expected {
		normalized, ok := c.normalizedExpected[value]
		if !ok {
			normalized = normalizeCommandSemantics(value, repoRoot)
		}
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func (c *commandInvocationCache) expectedMatcher(command string) shellcommand.CompiledExpectation {
	if c == nil {
		return shellcommand.CompileExpectation(command, 8)
	}
	if matcher, ok := c.expected[command]; ok {
		return matcher
	}
	// This path is only for a caller that supplies a command outside the
	// immutable plan. It still validates once before use and never calls an
	// unchecked matcher with unparsed policy text.
	return shellcommand.CompileExpectation(command, 8)
}

func (c *commandInvocationCache) observedInvocations(command string) observedCommandInvocations {
	if c == nil {
		segments, complete := executableCommandSegments(command, 0)
		return observedCommandInvocations{segments: segments, complete: complete}
	}
	if cached, ok := c.observed[command]; ok {
		return cached
	}
	segments, complete := executableCommandSegments(command, 0)
	cached := observedCommandInvocations{segments: segments, complete: complete}
	c.observed[command] = cached
	return cached
}

// AssertRuleByID evaluates a SINGLE rule (selected by id) against the
// provided inputs, augmented by template-variable bindings from `vars`.
//
// Behavior:
//   - Loads the lockfile and validates freshness as usual
//   - Finds the rule with the given id (else *LockfileError "rule not found")
//   - Synthesizes write_paths from rule.when_paths with `vars` substituted,
//     so a rule whose when_paths is `docs/todo/{task_id}.md` and vars
//     contains task_id=TODO-001 will trigger as if that exact write
//     happened
//   - Merges synthesized writes with any explicit inputs.WritePaths
//   - Evaluates ONLY that rule (other rules in the lockfile are skipped)
//   - Returns a CheckReport whose decision is solely based on this one rule
//
// This is the primitive behind `reconc assert` (W27), replacing
// repo-specific assertion subcommands with one generic path.
func AssertRuleByID(startPath, ruleID string, vars map[string]string, inputs ExecutionInputs) (*CheckReport, error) {
	return NewEvaluator().AssertRuleByID(startPath, ruleID, vars, inputs)
}

// AssertRuleByID evaluates one indexed rule through this evaluator's plan.
func (e *Evaluator) AssertRuleByID(startPath, ruleID string, vars map[string]string, inputs ExecutionInputs) (*CheckReport, error) {
	discovery, err := ingest.DiscoverPolicyRepo(startPath)
	if err != nil {
		return nil, err
	}
	if !discovery.Discovered {
		warning := "no policy markers discovered"
		if len(discovery.Warnings) > 0 {
			warning = discovery.Warnings[0]
		}
		return nil, fmt.Errorf("%s", warning)
	}
	root := discovery.RepoRoot

	plan, err := e.loadFreshRuntimePlan(root)
	if err != nil {
		return nil, err
	}
	targetIndex, ok := plan.ruleByID[ruleID]
	if !ok {
		return nil, &rerrors.LockfileError{Message: "rule '" + ruleID + "' not found in compiled lockfile"}
	}
	target := &plan.rules[targetIndex]

	// Synthesize write_paths from when_paths with vars substituted.
	whenPatterns := stringListField(target, "when_paths")
	synthesized := []string{}
	for _, pat := range whenPatterns {
		if !HasTemplateVars(pat) {
			synthesized = append(synthesized, pat) // literal pattern doubles as a path
			continue
		}
		concrete, err := SubstituteTemplate(pat, vars)
		if err != nil {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' when_paths substitution: " + err.Error() + " (provide via --var)",
			}
		}
		synthesized = append(synthesized, concrete)
	}

	// Merge: explicit inputs first, then synthesized (preserves
	// caller-provided evidence and adds our trigger paths).
	mergedWrites := newStableStringCollector(inputs.WritePaths)
	for _, p := range synthesized {
		mergedWrites.add(p)
	}
	merged := inputs
	merged.WritePaths = mergedWrites.values()

	normalized, err := normalizeEvaluationInput(root, merged)
	if err != nil {
		return nil, err
	}

	report := NewEmptyReport(root, ingest.LockfilePath, plan.defaultMode, normalized.inputs)
	ctx := &evalContext{
		repoRoot:         root,
		paths:            normalized.paths,
		rawCommands:      normalized.rawCommands,
		currentCommands:  normalized.currentCommands,
		matchers:         plan.pathMatchers,
		templateMatchers: plan.templateMatchers,
		commandCache:     newCommandInvocationCache([]policy.Rule{*target}, root),
		commandEvidence:  newCommandEvidenceIndex(normalized.inputs, root),
		evidenceCache:    newEvidenceSnapshotCache(),
		evidenceMemo:     newEvidenceMatchMemo(),
		contextMemo:      newMatchContextMemo(),
	}

	v, err := evaluateRule(ctx, target, plan.defaultMode, normalized.inputs)
	if err != nil {
		return nil, err
	}
	if err := normalized.paths.revalidateRoot(); err != nil {
		return nil, err
	}
	if v != nil {
		report.Violations = append(report.Violations, *v)
	}
	report.Finalize()
	report.NextAction = nextActionForViolations(report.Violations)
	report.Summary = summarizeReport(report.Decision, report.ViolationCount, report.BlockingViolationCount)
	return &report, nil
}

// CheckRepoPolicy is the pure-function judge.
//
// It loads the compiled lockfile, validates its freshness against the
// live policy sources, normalizes evidence to repo-relative POSIX
// paths, and evaluates each rule. The result is a CheckReport carrying
// a structured decision (pass/warn/block), a list of Violations with
// prescriptive next-step text, and metadata about the inputs.
//
// Deliberately offline, deterministic, side-effect-free: no network
// calls, no model inference, no implicit fallbacks. A malformed
// lockfile or unsupported rule kind raises *LockfileError instead of
// degrading to a silent pass; an evidence path that escapes the
// discovered repo root raises *RepoBoundaryError.
//
// startPath is any path inside the repo (or the repo root itself).
// inputs carry the runtime evidence (typically merged from CLI flags
// + events file + stdin payload before this call).
func CheckRepoPolicy(startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return NewEvaluator().CheckRepoPolicy(startPath, inputs)
}

// CheckRepoPolicy evaluates every rule through this evaluator's plan.
func (e *Evaluator) CheckRepoPolicy(startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return e.checkRepoPolicy(startPath, inputs, nil, false)
}

// CheckRepoPolicyForKinds evaluates only the requested top-level rule kinds
// while keeping lockfile loading, freshness checks, path normalization and
// unsupported-kind validation identical to CheckRepoPolicy.
func CheckRepoPolicyForKinds(startPath string, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}) (*CheckReport, error) {
	return NewEvaluator().CheckRepoPolicyForKinds(startPath, inputs, allowedKinds)
}

// CheckRepoPolicyForKinds evaluates an indexed subset of top-level kinds.
func (e *Evaluator) CheckRepoPolicyForKinds(startPath string, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}) (*CheckReport, error) {
	return e.checkRepoPolicy(startPath, inputs, allowedKinds, false)
}

// CheckRepoPolicyForPreCommand evaluates prevention rules before a shell
// command executes. Top-level forbid_command rules and composites containing a
// forbid_command sub-check are included so composing a prevention rule never
// silently demotes it to Stop-time detection.
func CheckRepoPolicyForPreCommand(startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return NewEvaluator().CheckRepoPolicyForPreCommand(startPath, inputs)
}

// CheckRepoPolicyForPreCommand evaluates the precomputed prevention subset.
func (e *Evaluator) CheckRepoPolicyForPreCommand(startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return e.checkRepoPolicy(startPath, inputs, nil, true)
}

func (e *Evaluator) checkRepoPolicy(startPath string, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}, preCommand bool) (*CheckReport, error) {
	discovery, err := ingest.DiscoverPolicyRepo(startPath)
	if err != nil {
		return nil, err
	}
	if !discovery.Discovered {
		warning := "no policy markers discovered"
		if len(discovery.Warnings) > 0 {
			warning = discovery.Warnings[0]
		}
		return nil, fmt.Errorf("%s", warning)
	}
	root := discovery.RepoRoot

	plan, err := e.loadFreshRuntimePlan(root)
	if err != nil {
		return nil, err
	}
	return evaluateRuntimePlan(root, plan, inputs, allowedKinds, preCommand)
}

func evaluateRuntimePlan(root string, plan *runtimePlan, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}, preCommand bool) (*CheckReport, error) {
	return evaluateRuntimePlanWithRootResolver(root, plan, inputs, allowedKinds, preCommand, pathidentity.ResolveExisting)
}

func evaluateRuntimePlanWithRootResolver(root string, plan *runtimePlan, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}, preCommand bool, resolveRoot func(string) (string, error)) (*CheckReport, error) {
	normalized, err := normalizeEvaluationInputWithRootResolver(root, inputs, resolveRoot)
	if err != nil {
		return nil, err
	}
	report := NewEmptyReport(root, ingest.LockfilePath, plan.defaultMode, normalized.inputs)
	ctx := &evalContext{
		repoRoot:         root,
		paths:            normalized.paths,
		rawCommands:      normalized.rawCommands,
		currentCommands:  normalized.currentCommands,
		preCommand:       preCommand,
		matchers:         plan.pathMatchers,
		templateMatchers: plan.templateMatchers,
		commandCache:     newCommandInvocationCache(plan.rules, root),
		commandEvidence:  newCommandEvidenceIndex(normalized.inputs, root),
		evidenceCache:    newEvidenceSnapshotCache(),
		evidenceMemo:     newEvidenceMatchMemo(),
		contextMemo:      newMatchContextMemo(),
	}
	ruleIndexes := plan.indexesFor(allowedKinds, preCommand)
	rules := make([]*policy.Rule, 0, len(ruleIndexes))
	for _, ruleIndex := range ruleIndexes {
		rules = append(rules, &plan.rules[ruleIndex])
	}

	batchedScripts, err := evaluateBatchedRequireScripts(ctx, rules, plan.defaultMode, normalized.inputs)
	if err != nil {
		return nil, err
	}
	for i, rule := range rules {
		if batchedScripts.handled[i] {
			if v := batchedScripts.violations[i]; v != nil {
				report.Violations = append(report.Violations, *v)
			}
			continue
		}
		v, err := evaluateRule(ctx, rule, plan.defaultMode, normalized.inputs)
		if err != nil {
			return nil, err
		}
		if v != nil && (!preCommand || !rule.Kind.IsComposite() || compositeForbiddenCommandMatches(ctx, rule)) {
			report.Violations = append(report.Violations, *v)
		}
	}
	if err := normalized.paths.revalidateRoot(); err != nil {
		return nil, err
	}

	report.Finalize()
	report.NextAction = nextActionForViolations(report.Violations)
	report.Summary = summarizeReport(report.Decision, report.ViolationCount, report.BlockingViolationCount)
	return &report, nil
}

func (plan *runtimePlan) indexesFor(allowedKinds map[policy.Kind]struct{}, preCommand bool) []int {
	if preCommand {
		return plan.preCommandRules
	}
	if allowedKinds == nil {
		indexes := make([]int, len(plan.rules))
		for index := range indexes {
			indexes[index] = index
		}
		return indexes
	}
	selected := make([]bool, len(plan.rules))
	for kind := range allowedKinds {
		for _, index := range plan.rulesByKind[kind] {
			selected[index] = true
		}
	}
	indexes := make([]int, 0)
	for index, include := range selected {
		if include {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

type batchedScriptEvaluations struct {
	handled    map[int]bool
	violations map[int]*Violation
}

type workflowAuditBatchKey struct {
	scriptPath     string
	timeoutSec     int
	killTimeoutSec int
}

type workflowAuditBatchItem struct {
	index    int
	rule     *policy.Rule
	mode     string
	contexts []matchContext
}

type workflowAuditBatchOutput struct {
	Results []workflowAuditBatchResult `json:"results"`
}

type workflowAuditBatchResult struct {
	Mode     string   `json:"mode"`
	Failures []string `json:"failures"`
}

func evaluateBatchedRequireScripts(ctx *evalContext, rules []*policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (batchedScriptEvaluations, error) {
	results := batchedScriptEvaluations{
		handled:    map[int]bool{},
		violations: map[int]*Violation{},
	}
	// Group only on the cheap, immutable batch key first. Scope matching and
	// template-context collection are deliberately deferred until a group has
	// at least two candidates; otherwise a singleton would pay the same
	// preparation cost and then immediately fall back to evaluateRule.
	groups := map[workflowAuditBatchKey][]workflowAuditBatchItem{}
	groupOrder := []workflowAuditBatchKey{}
	for i, rule := range rules {
		scriptPath, mode, timeoutSec, killTimeoutSec, ok := workflowAuditBatchCandidate(rule)
		if !ok {
			continue
		}
		key := workflowAuditBatchKey{scriptPath: scriptPath, timeoutSec: timeoutSec, killTimeoutSec: killTimeoutSec}
		if _, ok := groups[key]; !ok {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], workflowAuditBatchItem{
			index: i,
			rule:  rule,
			mode:  mode,
		})
	}

	for _, key := range groupOrder {
		items := groups[key]
		if len(items) < 2 {
			continue
		}
		eligible := make([]workflowAuditBatchItem, 0, len(items))
		for _, item := range items {
			scopeMatches, scopeErr := ruleScopeMatchesWithMatchers(ctx.matchers, item.rule, inputs)
			if scopeErr != nil || !scopeMatches {
				// Scope errors remain owned by evaluateRule so they produce the
				// established fail-closed synthetic violation. A definite scope
				// miss is rejected before match-context collection or subprocess IO.
				continue
			}
			contexts, err := ctx.contextMemo.collect(ctx.templateMatchers, inputs.WritePaths, stringListField(item.rule, "when_paths"))
			if err != nil {
				return results, err
			}
			if len(contexts) == 0 {
				continue
			}
			item.contexts = contexts
			eligible = append(eligible, item)
		}
		if len(eligible) < 2 {
			continue
		}
		items = eligible
		modes := uniqueBatchModes(items)
		if len(modes) == 0 {
			continue
		}

		args := append([]string{"--batch-json"}, modes...)
		input := ScriptInput{
			RuleID:         "workflow-audit-batch",
			RepoRoot:       ctx.repoRoot,
			Captures:       map[string]string{},
			WritePaths:     inputs.WritePaths,
			ReadPaths:      inputs.ReadPaths,
			Commands:       inputs.Commands,
			Claims:         inputs.Claims,
			CommandResults: inputs.CommandResults,
		}
		outcome, err := RunScript(ctx.repoRoot, key.scriptPath, args, input, key.timeoutSec, key.killTimeoutSec)
		evaluation := classifyScriptOutcome(outcome, err, key.timeoutSec)
		if evaluation.disposition != scriptOutcomePass && evaluation.disposition != scriptOutcomeBlock {
			continue
		}
		failuresByMode, hasFailures, ok := parseWorkflowAuditBatchOutputDisposition(outcome.Stdout, modes)
		if !ok {
			continue
		}
		contradictory := (evaluation.disposition == scriptOutcomePass && hasFailures) ||
			(evaluation.disposition == scriptOutcomeBlock && !hasFailures)
		if contradictory {
			detail := fmt.Sprintf("batch process disposition and structured output contradict: status=%s exit_code=%d", outcome.Status, outcome.ExitCode)
			for _, item := range items {
				results.handled[item.index] = true
				results.violations[item.index] = buildBatchScriptViolation(item, key.scriptPath, defaultMode, []string{detail})
			}
			continue
		}

		for _, item := range items {
			results.handled[item.index] = true
			failures := failuresByMode[item.mode]
			if len(failures) == 0 {
				continue
			}
			results.violations[item.index] = buildBatchScriptViolation(item, key.scriptPath, defaultMode, failures)
		}
	}

	return results, nil
}

// workflowAuditBatchCandidate recognizes the documented batch-capable
// audit convention: a require_script whose script is named
// `run-workflow-audit` inside an `audits/` directory (any repo
// location) and whose single argument is the audit mode. Scripts that
// match the shape but do not speak the `--batch-json` protocol fall
// back to normal per-rule execution because unparseable batch output
// is never treated as handled.
func workflowAuditBatchCandidate(rule *policy.Rule) (scriptPath, mode string, timeoutSec, killTimeoutSec int, ok bool) {
	if rule.Kind != policy.KindRequireScript {
		return "", "", 0, 0, false
	}
	scriptPath = rule.Script
	slashScriptPath := filepath.ToSlash(scriptPath)
	batchConvention := strings.HasSuffix(slashScriptPath, "/audits/run-workflow-audit") || slashScriptPath == "audits/run-workflow-audit"
	if scriptPath == "" || HasTemplateVars(scriptPath) || !batchConvention {
		return "", "", 0, 0, false
	}
	if len(rule.Args) != 1 {
		return "", "", 0, 0, false
	}
	mode = rule.Args[0]
	if mode == "" || HasTemplateVars(mode) {
		return "", "", 0, 0, false
	}
	timeoutSec = rule.TimeoutSec
	killTimeoutSec = rule.KillTimeoutSec
	return scriptPath, mode, timeoutSec, killTimeoutSec, true
}

func uniqueBatchModes(items []workflowAuditBatchItem) []string {
	seen := map[string]struct{}{}
	modes := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item.mode]; ok {
			continue
		}
		seen[item.mode] = struct{}{}
		modes = append(modes, item.mode)
	}
	return modes
}

func parseWorkflowAuditBatchOutput(stdout string, expectedModes []string) (map[string][]string, bool) {
	failures, _, ok := parseWorkflowAuditBatchOutputDisposition(stdout, expectedModes)
	return failures, ok
}

func parseWorkflowAuditBatchOutputDisposition(stdout string, expectedModes []string) (map[string][]string, bool, bool) {
	var output workflowAuditBatchOutput
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(stdout)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return nil, false, false
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false, false
	}
	expected := make(map[string]struct{}, len(expectedModes))
	for _, mode := range expectedModes {
		expected[mode] = struct{}{}
	}
	failuresByMode := map[string][]string{}
	hasFailures := false
	for _, result := range output.Results {
		if _, wanted := expected[result.Mode]; !wanted {
			return nil, false, false
		}
		if _, duplicate := failuresByMode[result.Mode]; duplicate {
			return nil, false, false
		}
		failures := make([]string, 0, len(result.Failures))
		for _, failure := range result.Failures {
			if failure = strings.TrimSpace(failure); failure != "" {
				failures = append(failures, failure)
				hasFailures = true
			}
		}
		failuresByMode[result.Mode] = failures
	}
	for _, mode := range expectedModes {
		if _, ok := failuresByMode[mode]; !ok {
			return nil, false, false
		}
	}
	return failuresByMode, hasFailures, true
}

func buildBatchScriptViolation(item workflowAuditBatchItem, scriptPath string, defaultMode policy.Mode, failures []string) *Violation {
	triggeredPaths := triggeredPathsForContexts(item.contexts)
	violation := buildViolation(item.rule, defaultMode, triggeredPaths, nil, nil, []string{scriptPath}, nil, nil)
	details := batchScriptFailureDetails(scriptPath, failures)
	violation.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_script rule '%s'. %s",
		joinForHumans(triggeredPaths), violation.RuleID, strings.Join(details, "; "),
	)
	violation.RecommendedAction = batchScriptRecommendedAction(details)
	return violation
}

func triggeredPathsForContexts(contexts []matchContext) []string {
	paths := newStableStringCollector([]string{})
	for _, mc := range contexts {
		paths.add(mc.path)
	}
	return paths.values()
}

func batchScriptFailureDetails(scriptPath string, failures []string) []string {
	details := make([]string, 0, len(failures))
	for _, failure := range failures {
		failure = strings.TrimSpace(failure)
		if failure == "" {
			continue
		}
		details = append(details, fmt.Sprintf("script %s blocked: %s", scriptPath, failure))
	}
	if len(details) == 0 {
		return []string{fmt.Sprintf("script %s blocked: no output", scriptPath)}
	}
	return details
}

func batchScriptRecommendedAction(details []string) string {
	detail := strings.Join(details, "; ")
	runes := []rune(detail)
	if len(runes) > 600 {
		detail = string(runes[:600]) + "..."
	}
	return "Resolve batch audit failure(s): " + detail
}

func scriptRecommendedAction(failures []string) string {
	detail := strings.Join(failures, "; ")
	runes := []rune(detail)
	if len(runes) > 600 {
		detail = string(runes[:600]) + "..."
	}
	return "Resolve script failure(s): " + detail
}

// --- Path / command normalization ---

func normalizePaths(paths []string, root string) ([]string, error) {
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	return normalizePathsWithResolvedRoot(paths, resolvedRoot)
}

// normalizePathsWithResolvedRoot is the per-path normalization core. Callers
// on the check hot path resolve the repo root identity once via
// pathidentity.ResolveExisting and reuse it for every path, instead of paying
// the symlink/reparse resolution cost once per evidence path.
func normalizePathsWithResolvedRoot(paths []string, resolvedRoot string) ([]string, error) {
	prospective := pathidentity.NewProspectiveResolver()
	return normalizePathsWithResolver(paths, resolvedRoot, prospective)
}

func normalizePathsWithResolver(paths []string, resolvedRoot string, prospective *pathidentity.ProspectiveResolver) ([]string, error) {
	out := []string{}
	if prospective == nil {
		prospective = pathidentity.NewProspectiveResolver()
	}
	for _, raw := range paths {
		posix, keep, err := normalizePathWithResolver(raw, resolvedRoot, prospective)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, posix)
		}
	}
	return out, nil
}

func normalizePathWithResolver(raw, resolvedRoot string, prospective *pathidentity.ProspectiveResolver) (string, bool, error) {
	candidate := raw
	if candidate == "" {
		return "", false, nil
	}
	// Convert only separators native to the current platform. On POSIX a
	// backslash is a legal filename byte and must not be conflated with '/'.
	candidate = filepath.ToSlash(candidate)
	var absPath string
	if path.IsAbs(candidate) || filepath.IsAbs(candidate) {
		absPath = candidate
	} else {
		absPath = filepath.Join(resolvedRoot, candidate)
	}
	cleaned := filepath.Clean(absPath)
	if prospective == nil {
		prospective = pathidentity.NewProspectiveResolver()
	}
	cleaned, err := prospective.Resolve(cleaned)
	if err != nil {
		return "", false, fmt.Errorf("resolve evidence path %q: %w", raw, err)
	}

	rel, err := filepath.Rel(resolvedRoot, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, &rerrors.RepoBoundaryError{Path: raw, RepoRoot: resolvedRoot}
	}
	// Convert OS-native to POSIX
	posix := filepath.ToSlash(rel)
	if posix == "." {
		return "", false, nil
	}
	return posix, true, nil
}

func normalizeWriteEpochs(paths []string, epochs map[string]uint64, root string) (map[string]uint64, error) {
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	return normalizeWriteEpochsWithResolvedRoot(paths, epochs, resolvedRoot)
}

// normalizeWriteEpochsWithResolvedRoot maps raw write-path epoch keys onto
// their normalized repo-relative form. It reuses the caller-resolved repo
// root so the check hot path resolves the root identity once instead of once
// per write path.
func normalizeWriteEpochsWithResolvedRoot(paths []string, epochs map[string]uint64, resolvedRoot string) (map[string]uint64, error) {
	prospective := pathidentity.NewProspectiveResolver()
	return normalizeWriteEpochsWithResolver(paths, epochs, resolvedRoot, prospective)
}

func normalizeWriteEpochsWithResolver(paths []string, epochs map[string]uint64, resolvedRoot string, prospective *pathidentity.ProspectiveResolver) (map[string]uint64, error) {
	out := make(map[string]uint64, len(epochs))
	if prospective == nil {
		prospective = pathidentity.NewProspectiveResolver()
	}
	for _, raw := range paths {
		normalized, keep, err := normalizePathWithResolver(raw, resolvedRoot, prospective)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		epoch := epochs[raw]
		if epoch == 0 {
			epoch = epochs[filepath.ToSlash(raw)]
		}
		if epoch > out[normalized] {
			out[normalized] = epoch
		}
	}
	return out, nil
}

// RelativizeEpochKeys bridges the write-epoch key formats between agent
// sessions and git-derived paths: session hooks record epochs under the
// absolute tool payload path, while ci derives write paths repo-relative from
// git diff. Every absolute key under root gains a repo-relative slash alias
// (the original key is kept), so epoch lookups by either spelling hit the
// recorded value instead of silently reading zero and disabling the
// command-after-last-edit binding.
func RelativizeEpochKeys(root string, epochs map[string]uint64) map[string]uint64 {
	if len(epochs) == 0 {
		return epochs
	}
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return epochs
	}
	out := make(map[string]uint64, len(epochs)*2)
	for key, epoch := range epochs {
		if epoch > out[key] {
			out[key] = epoch
		}
		candidate := filepath.FromSlash(key)
		if !filepath.IsAbs(candidate) {
			continue
		}
		cleaned, resolveErr := pathidentity.ResolveProspective(filepath.Clean(candidate))
		if resolveErr != nil {
			continue
		}
		rel, relErr := filepath.Rel(resolvedRoot, cleaned)
		if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		relKey := filepath.ToSlash(rel)
		if epoch > out[relKey] {
			out[relKey] = epoch
		}
	}
	return out
}

// normalizeWhitespace collapses every run of whitespace (spaces,
// tabs, newlines) into a single space and trims leading/trailing
// whitespace. Used for command + claim matching so policy-side
// `"go test"` matches agent-reported `"go  test"` / `"go\ttest"`
// Empty / whitespace-only strings become empty.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normalizeCommandSemantics applies idempotent semantic normalisations
// that reflect transparent wrapping by other tools so that
// require_command / require_command_success / forbid_command rules can
// be authored in their literal-intent form even when the recorded
// command was rewritten by a CLI proxy or anchored to an absolute path
// by the agent runtime.
//
// Two normalisations are layered on top of normalizeWhitespace:
//
//  1. RTK proxy prefix: the literal token "rtk " is stripped at the
//     start of the command and after every unquoted shell compound boundary
//     (&&, ||, ;, |, |&, &), with or without surrounding whitespace. The trailing space in the
//     match prevents false positives like a directory named /rtkfoo or
//     a literal command named rtk-tool.
//  2. Absolute repo path in cd: "cd <repoRoot>" becomes "cd ." and
//     "cd <repoRoot>/<sub>" becomes "cd <sub>", but only when the cd
//     argument is the repo root prefix at a segment boundary. A
//     command like `echo /<repoRoot>` is untouched because no `cd ` is
//     at segment start.
//
// repoRoot may be empty; in that case only the RTK prefix strip runs.
// The transformation is applied to BOTH sides of every command match
// so forbid-rule semantics stay exact (a literal `rm -rf /` still
// matches only `rm -rf /` and `rtk rm -rf /`, not `echo "rm -rf /"`).
//
// Applied repeatedly the function is idempotent: every pass after the
// first returns the same string.
func normalizeCommandSemantics(cmd, repoRoot string) string {
	cmd = normalizeShellWhitespace(cmd)
	if cmd == "" {
		return ""
	}
	repoRoot = strings.TrimRight(strings.TrimSpace(repoRoot), "/")
	segments := splitCommandSegments(cmd)
	for i := range segments {
		segments[i].body = normalizeSegmentBody(segments[i].body, repoRoot)
	}
	// Drop leading `cd .` segments left by agents that anchor commands with
	// an explicit cd into the repo root: `cd /abs/repo && X` is semantically
	// `X` when /abs/repo IS the repo root. Only `&&` and `;` joins are safe
	// to drop; `cd . || X` or `cd . | X` would change meaning and stay as-is.
	for len(segments) >= 2 && segments[0].body == "cd ." &&
		(segments[1].sep == " && " || segments[1].sep == " ; ") {
		segments = segments[1:]
		segments[0].sep = ""
	}
	var out strings.Builder
	for i, s := range segments {
		if i > 0 {
			out.WriteString(s.sep)
		}
		out.WriteString(s.body)
	}
	return normalizeShellWhitespace(out.String())
}

// commandSegment is one slice of a normalized command between shell
// compound boundaries. The first segment has sep == "".
type commandSegment struct {
	sep  string
	body string
}

// commandSegmentSeparators lists the shell compound boundaries that start a
// new command position. Shell does not require surrounding whitespace, so the
// scanner canonicalizes both `a&&b` and `a && b` without inspecting quoted
// literal data.
var commandSegmentSeparators = []string{"&&", "||", "|&", ";", "|", "&"}

// splitCommandSegments splits a whitespace-normalized command into
// segments at every shell compound boundary while preserving the
// separators so the command can be reconstructed verbatim.
func splitCommandSegments(cmd string) []commandSegment {
	segments := make([]commandSegment, 0, 4)
	start := 0
	nextSeparator := ""
	var quote byte
	escaped := false
	substitutionDepth := 0
	for index := 0; index < len(cmd); index++ {
		current := cmd[index]
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		if current == '(' && (substitutionDepth > 0 || index > 0 && cmd[index-1] == '$') {
			substitutionDepth++
			continue
		}
		if current == ')' && substitutionDepth > 0 {
			substitutionDepth--
			continue
		}
		if substitutionDepth > 0 {
			continue
		}
		for _, separator := range commandSegmentSeparators {
			if !strings.HasPrefix(cmd[index:], separator) {
				continue
			}
			if separator == "&" && ((index > 0 && cmd[index-1] == '>') || (index+1 < len(cmd) && cmd[index+1] == '>')) {
				continue
			}
			segments = append(segments, commandSegment{sep: nextSeparator, body: cmd[start:index]})
			nextSeparator = " " + separator + " "
			index += len(separator) - 1
			start = index + 1
			break
		}
	}
	segments = append(segments, commandSegment{sep: nextSeparator, body: cmd[start:]})
	return segments
}

// normalizeShellWhitespace collapses only unquoted shell whitespace. Literal
// spaces inside single quotes, double quotes, backticks, or an escaped token
// are semantic data and must survive command normalization byte-for-byte.
func normalizeShellWhitespace(command string) string {
	command = shellcommand.StripLineContinuations(command)
	var normalized strings.Builder
	normalized.Grow(len(command))
	var quote byte
	escaped := false
	pendingSpace := false
	lastUnquotedSeparator := false
	flushSpace := func() {
		if pendingSpace && normalized.Len() > 0 {
			normalized.WriteByte(' ')
		}
		pendingSpace = false
	}
	for index := 0; index < len(command); index++ {
		current := command[index]
		if escaped {
			flushSpace()
			normalized.WriteByte(current)
			lastUnquotedSeparator = false
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			flushSpace()
			normalized.WriteByte(current)
			escaped = true
			continue
		}
		if quote != 0 {
			normalized.WriteByte(current)
			lastUnquotedSeparator = false
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			flushSpace()
			quote = current
			normalized.WriteByte(current)
			lastUnquotedSeparator = false
			continue
		}
		if current == '\r' || current == '\n' {
			flushSpace()
			if normalized.Len() > 0 && !lastUnquotedSeparator {
				normalized.WriteString(" ; ")
			}
			lastUnquotedSeparator = true
			if current == '\r' && index+1 < len(command) && command[index+1] == '\n' {
				index++
			}
			continue
		}
		if current == ' ' || current == '\t' {
			pendingSpace = true
			continue
		}
		flushSpace()
		normalized.WriteByte(current)
		lastUnquotedSeparator = current == ';' || current == '|' || current == '&'
	}
	return normalized.String()
}

// normalizeSegmentBody applies the two semantic normalisations to one
// command segment body (the text between compound boundaries).
//
// The cd-arg path is cleaned via path.Clean before comparison so that
// `cd /repo/`, `cd /repo/.`, `cd /repo//sub`, and `cd /repo/sub/..` all
// resolve to their canonical forms relative to repoRoot. Cleaning is
// only applied to unquoted absolute-style arguments to avoid touching
// `cd "/path with spaces"` and similar quoted forms.
func normalizeSegmentBody(body, repoRoot string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return body
	}
	// Every pass consumes at least the complete four-byte "rtk " token, so
	// the input-derived bound proves convergence even for adversarial stacks.
	maximumWrappers := len(body) / len("rtk ")
	for range maximumWrappers {
		if !strings.HasPrefix(body, "rtk ") {
			break
		}
		body = strings.TrimSpace(body[len("rtk "):])
	}
	if repoRoot != "" && strings.HasPrefix(body, "cd ") {
		arg := strings.TrimSpace(body[len("cd "):])
		// Skip quote-wrapped arguments — path.Clean would mishandle
		// embedded spaces in quoted shell tokens.
		if !strings.HasPrefix(arg, "\"") && !strings.HasPrefix(arg, "'") {
			cleaned := path.Clean(arg)
			cleanedRoot := path.Clean(repoRoot)
			if cleaned == cleanedRoot {
				body = "cd ."
			} else if strings.HasPrefix(cleaned, cleanedRoot+"/") {
				body = "cd " + strings.TrimPrefix(cleaned, cleanedRoot+"/")
			}
		}
	}
	return body
}

func normalizeCommands(commands []string) []string {
	out := make([]string, 0, len(commands))
	for _, c := range commands {
		norm := normalizeShellWhitespace(c)
		if norm != "" {
			out = append(out, norm)
		}
	}
	return out
}

func normalizeCommandResults(results []CommandResult) []CommandResult {
	out := make([]CommandResult, 0, len(results))
	for _, r := range results {
		c := normalizeShellWhitespace(r.Command)
		if c == "" {
			continue
		}
		out = append(out, CommandResult{Command: c, Outcome: r.Outcome, EvidenceEpoch: r.EvidenceEpoch})
	}
	return out
}

func dedupePreservingOrder(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// --- Per-rule evaluation ---

func ruleScopeMatchesWithMatchers(matchers *runtimePathMatchers, rule *policy.Rule, inputs ExecutionInputs) (bool, error) {
	if len(rule.ScopePaths) == 0 {
		return true, nil // global rule
	}
	// Check writes + reads; any single match is enough to put the
	// evaluation "inside" the scope.  Pattern errors abort the scope
	// match and propagate up so the rule evaluator can convert them
	// into a block-severity synthetic violation.
	for _, p := range inputs.WritePaths {
		_, matched, err := matchAnyPaths(matchers, rule.ScopePaths, p)
		if err != nil {
			return false, fmt.Errorf("scope_paths pattern error on input %q: %w", p, err)
		}
		if matched {
			return true, nil
		}
	}
	for _, p := range inputs.ReadPaths {
		_, matched, err := matchAnyPaths(matchers, rule.ScopePaths, p)
		if err != nil {
			return false, fmt.Errorf("scope_paths pattern error on input %q: %w", p, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func evaluateRule(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	if !rule.Kind.Valid() {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile contains unsupported rule kind: " + string(rule.Kind)}
	}

	// W17 monorepo scope filter. If the rule was declared inside a
	// `scopes:` block, it carries the scope's path patterns. The rule
	// only fires when at least one input read or write path matches
	// one of those scope patterns. Global rules (no scope_paths) skip
	// this gate and apply to every input as before.
	//
	// Scope pattern errors surface as a synthetic blocking violation
	// ("scope-pattern-error") rather than silently skipping the rule.
	// A malformed scope_paths in the lockfile must NOT neutralise a rule.
	matched, err := ruleScopeMatchesWithMatchers(ctx.matchers, rule, inputs)
	if err != nil {
		return &Violation{
			RuleID:            rule.ID,
			Kind:              rule.Kind,
			Mode:              policy.ModeBlock,
			Message:           "scope pattern failed to compile: " + err.Error(),
			Explanation:       "The rule's scope_paths contains a glob pattern that the matcher could not compile. reconc fails closed here to prevent a malformed scope from silently disabling a rule.",
			RecommendedAction: "Fix the scope_paths pattern in the rule source, then run `reconc refresh .`.",
		}, nil
	}
	if !matched {
		return nil, nil
	}

	switch rule.Kind {
	case policy.KindDenyWrite:
		return evalDenyWrite(ctx, rule, defaultMode, inputs)
	case policy.KindRequireRead:
		return evalRequireRead(ctx, rule, defaultMode, inputs)
	case policy.KindCoupleChange:
		return evalCoupleChange(ctx, rule, defaultMode, inputs)
	case policy.KindRequireClaim:
		return evalRequireClaim(ctx, rule, defaultMode, inputs)
	case policy.KindForbidCommand:
		return evalForbidCommand(ctx, rule, defaultMode, inputs)
	case policy.KindRequireCommand:
		return evalRequireCommand(ctx, rule, defaultMode, inputs, false)
	case policy.KindRequireCommandSuccess:
		return evalRequireCommand(ctx, rule, defaultMode, inputs, true)
	case policy.KindRequireFreshFile:
		return evalRequireFreshFile(ctx, rule, defaultMode, inputs)
	case policy.KindRequireEvidence:
		return evalRequireEvidence(ctx, rule, defaultMode, inputs)
	case policy.KindAllOf:
		return evalAllOf(ctx, rule, defaultMode, inputs)
	case policy.KindAnyOf:
		return evalAnyOf(ctx, rule, defaultMode, inputs)
	case policy.KindNot:
		return evalNot(ctx, rule, defaultMode, inputs)
	case policy.KindRequireScript:
		return evalRequireScript(ctx, rule, defaultMode, inputs)
	case policy.KindRequireAssurance:
		return evalRequireAssurance(ctx, rule, defaultMode, inputs)
	}
	return nil, nil
}

func evalRequireAssurance(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	triggered, err := matchingPathsWithMatchers(ctx.matchers, inputs.WritePaths, stringListField(rule, "when_paths"))
	if err != nil {
		return nil, err
	}
	if len(triggered) == 0 {
		return nil, nil
	}
	gates, err := assuranceGatesFromRule(rule)
	if err != nil {
		return nil, err
	}
	successful := newStableStringCollector([]string{})
	reportedSuccessful := newStableStringCollector([]string{})
	if ctx.commandEvidence != nil {
		for _, result := range ctx.commandEvidence.results {
			if result.outcome != CommandOutcomeSuccess {
				continue
			}
			reportedSuccessful.add(result.raw)
			successful.add(result.raw)
			successful.add(result.normalized)
		}
	} else {
		for _, result := range inputs.CommandResults {
			if result.Outcome != CommandOutcomeSuccess {
				continue
			}
			reportedSuccessful.add(result.Command)
			successful.add(result.Command)
			successful.add(normalizeCommandSemantics(result.Command, ctx.repoRoot))
		}
	}
	for gateIndex := range gates {
		gates[gateIndex].Commands = ctx.commandCache.normalizedExpectedCommands(gates[gateIndex].Commands, ctx.repoRoot)
	}
	findings, err := assurance.Evaluate(ctx.repoRoot, gates, assurance.Inputs{
		ChangedPaths:       inputs.WritePaths,
		SuccessfulCommands: successful.values(),
		Now:                time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	if len(findings) == 0 {
		return nil, nil
	}
	requiredPaths := newStableStringCollector([]string{})
	details := make([]string, 0, len(findings))
	for _, finding := range findings {
		details = append(details, "["+finding.GateID+"] "+finding.Message)
		for _, path := range finding.Paths {
			requiredPaths.add(path)
		}
	}
	v := buildViolation(rule, defaultMode, triggered, reportedSuccessful.values(), nil, requiredPaths.values(), nil, nil)
	v.Explanation = "Native assurance failed: " + strings.Join(details, "; ")
	v.RecommendedAction = findings[0].Remediation
	if len(findings) > 1 {
		v.RecommendedAction += fmt.Sprintf(" Then resolve the remaining %d assurance finding(s).", len(findings)-1)
	}
	return v, nil
}

// evalRequireScript runs an external script for each match context.
// Each context that produces a "block" or "error" outcome contributes
// to the violation. A "pass" exit (0) clears that context.
func evalRequireScript(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	whenPatterns := stringListField(rule, "when_paths")
	contexts, err := ctx.contextMemo.collect(ctx.templateMatchers, inputs.WritePaths, whenPatterns)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return nil, nil
	}

	scriptPath := rule.Script
	if scriptPath == "" {
		return nil, &rerrors.LockfileError{Message: "rule '" + ruleIDOf(rule) + "' missing script field in lockfile"}
	}
	args := rule.Args
	timeoutSec := rule.TimeoutSec
	killTimeoutSec := rule.KillTimeoutSec

	failures := []string{}
	triggeredPaths := newStableStringCollector([]string{})
	for _, mc := range contexts {
		triggeredPaths.add(mc.path)
		// Substitute captures into args.
		substArgs, err := SubstituteTemplateInList(args, mc.captures)
		if err != nil {
			return nil, &rerrors.RuleValidationError{Message: "script args: " + err.Error()}
		}
		input := ScriptInput{
			RuleID:         ruleIDOf(rule),
			RepoRoot:       ctx.repoRoot,
			Captures:       mc.captures,
			WritePaths:     inputs.WritePaths,
			ReadPaths:      inputs.ReadPaths,
			Commands:       inputs.Commands,
			Claims:         inputs.Claims,
			CommandResults: inputs.CommandResults,
		}
		outcome, err := RunScript(ctx.repoRoot, scriptPath, substArgs, input, timeoutSec, killTimeoutSec)
		evaluation := classifyScriptOutcome(outcome, err, timeoutSec)
		switch evaluation.disposition {
		case scriptOutcomePass:
			continue
		case scriptOutcomeBlock:
			failures = append(failures, fmt.Sprintf("[%s] script %s blocked: %s", mc.path, scriptPath, evaluation.detail))
		case scriptOutcomeError:
			failures = append(failures, fmt.Sprintf("[%s] script %s error: %s", mc.path, scriptPath, evaluation.detail))
		}
	}
	if len(failures) == 0 {
		return nil, nil
	}
	v := buildViolation(rule, defaultMode, triggeredPaths.values(), nil, nil, []string{scriptPath}, nil, nil)
	v.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_script rule '%s'. %s",
		joinForHumans(triggeredPaths.values()), v.RuleID, strings.Join(failures, "; "),
	)
	v.RecommendedAction = scriptRecommendedAction(failures)
	return v, nil
}

// evalRequireFreshFile fires when when_paths matches AND any required
// file is missing OR older than its max_age_hours.
//
// Template-aware: when when_paths contains {var} placeholders, each
// matched write path produces its own substitution context so a
// single rule can scale across many tasks/modules without enumerating
// every value.
func evalRequireFreshFile(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	whenPatterns := stringListField(rule, "when_paths")
	files := requiredFilesFromRule(rule)
	if len(files) == 0 {
		return nil, nil
	}

	// Collect all (write_path, captures) pairs that match the rule's
	// when_paths. For non-templated patterns captures is empty.
	contexts, err := ctx.contextMemo.collect(ctx.templateMatchers, inputs.WritePaths, whenPatterns)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return nil, nil
	}

	missing := newStableStringCollector([]string{})
	stale := newStableStringCollector([]string{})
	allRequired := map[string]struct{}{}
	triggeredPaths := newStableStringCollector([]string{})
	now := time.Now()

	for _, mc := range contexts {
		triggeredPaths.add(mc.path)
		for _, rf := range files {
			path, err := SubstituteTemplate(rf.Path, mc.captures)
			if err != nil {
				return nil, &rerrors.RuleValidationError{
					Message: "rule '" + ruleIDOf(rule) + "' required_files path: " + err.Error(),
				}
			}
			fullPath, err := ctx.resolvePolicyFile(path)
			if err != nil {
				return nil, err
			}
			snapshot, err := ctx.evidenceCache.snapshot(fullPath, false)
			if err != nil {
				return nil, &rerrors.LockfileError{Message: "stat required file " + path, Cause: err}
			}
			if !snapshot.exists {
				if rf.Optional {
					continue
				}
				missing.add(path)
				allRequired[path] = struct{}{}
				continue
			}
			info := snapshot.info
			allRequired[path] = struct{}{}
			if !info.Mode().IsRegular() {
				missing.add(path)
				continue
			}
			if rf.MaxAgeHours > 0 {
				age := now.Sub(info.ModTime())
				limit := time.Duration(rf.MaxAgeHours) * time.Hour
				if age > limit {
					stale.add(path)
				}
			}
		}
	}

	if len(missing.values()) == 0 && len(stale.values()) == 0 {
		return nil, nil
	}
	requiredPaths := mapKeysSorted(allRequired)
	v := buildViolation(rule, defaultMode, triggeredPaths.values(), nil, nil, requiredPaths, nil, nil)
	parts := []string{}
	if len(missing.values()) > 0 {
		parts = append(parts, "missing: "+joinForHumans(missing.values()))
	}
	if len(stale.values()) > 0 {
		parts = append(parts, "stale: "+joinForHumans(stale.values()))
	}
	v.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_fresh_file rule '%s' (%s).",
		joinForHumans(triggeredPaths.values()), v.RuleID, strings.Join(parts, "; "),
	)
	v.RecommendedAction = "Regenerate or refresh the listed files: " + joinForHumans(requiredPaths) + "."
	return v, nil
}

// evalRequireEvidence fires when when_paths matches AND any evidence
// check fails. Template-aware: each matched write path supplies its
// own captures for substitution into evidence file paths.
func evalRequireEvidence(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	whenPatterns := stringListField(rule, "when_paths")
	checks := evidenceChecksFromRule(rule)
	if len(checks) == 0 {
		return nil, nil
	}

	contexts, err := ctx.contextMemo.collect(ctx.templateMatchers, inputs.WritePaths, whenPatterns)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return nil, nil
	}

	failures := []string{}
	requiredFiles := map[string]struct{}{}
	triggeredPaths := newStableStringCollector([]string{})

	for _, mc := range contexts {
		triggeredPaths.add(mc.path)
		for _, c := range checks {
			file, err := SubstituteTemplate(c.File, mc.captures)
			if err != nil {
				return nil, &rerrors.RuleValidationError{
					Message: "rule '" + ruleIDOf(rule) + "' evidence file: " + err.Error(),
				}
			}
			requiredFiles[file] = struct{}{}
			fullPath, err := ctx.resolvePolicyFile(file)
			if err != nil {
				return nil, err
			}
			needContent := len(c.MustContain) > 0 || c.MustNotContain != "" || c.MaxLineCount > 0
			snapshot, err := ctx.evidenceCache.snapshot(fullPath, needContent)
			if err != nil {
				return nil, &rerrors.LockfileError{Message: "read evidence file " + file, Cause: err}
			}
			match := ctx.evidenceMemo.match(fullPath, snapshot, evidenceMatchOptions{
				file:           file,
				mustExist:      c.MustExist,
				mustContain:    c.MustContain,
				mustNotContain: c.MustNotContain,
				maxLineCount:   c.MaxLineCount,
				optional:       c.Optional,
			})
			if match.err != nil {
				return nil, match.err
			}
			failures = append(failures, match.reasons...)
		}
	}

	if len(failures) == 0 {
		return nil, nil
	}
	required := mapKeysSorted(requiredFiles)
	v := buildViolation(rule, defaultMode, triggeredPaths.values(), nil, nil, required, nil, nil)
	v.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_evidence rule '%s'. Failures: %s.",
		joinForHumans(triggeredPaths.values()), v.RuleID, strings.Join(failures, "; "),
	)
	v.RecommendedAction = "Update the evidence files to satisfy the listed assertions."
	return v, nil
}

// matchContext records one (write_path, captures) hit for a templated
// when_paths pattern. Used by template-aware rule evaluators to iterate
// over every substitution context produced by the evidence.
type matchContext struct {
	path     string
	pattern  string
	captures map[string]string
}

func collectMatchContextsWithMatchers(matchers *runtimeTemplateMatchers, writes, patterns []string) ([]matchContext, error) {
	out := []matchContext{}
	for _, w := range writes {
		for _, pat := range patterns {
			caps, ok, err := matchTemplateWithMatchers(matchers, pat, w)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			out = append(out, matchContext{path: w, pattern: pat, captures: caps})
			break // one match per write path is enough; mirrors matchingPaths()
		}
	}
	return out, nil
}

// stableStringCollector preserves first-seen order while keeping membership
// checks O(1). Its values are scoped to one collection build; callers retain
// their existing input/output cardinality bounds, so the index cannot outlive
// or exceed the collection it accelerates.
type stableStringCollector struct {
	items []string
	seen  map[string]struct{}
}

func newStableStringCollector(initial []string) stableStringCollector {
	items := initial
	if initial != nil {
		items = make([]string, len(initial))
		copy(items, initial)
	}
	seen := make(map[string]struct{}, len(initial))
	for _, value := range initial {
		seen[value] = struct{}{}
	}
	return stableStringCollector{items: items, seen: seen}
}

func (c *stableStringCollector) add(value string) {
	if _, exists := c.seen[value]; exists {
		return
	}
	c.seen[value] = struct{}{}
	c.items = append(c.items, value)
}

func (c *stableStringCollector) values() []string {
	return c.items
}

// mapKeysSorted returns the keys of a string-keyed set in sorted order.
// Used to produce stable required-paths lists in violation reports.
func mapKeysSorted(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ruleIDOf is a typed accessor for rule["id"] used in error messages.
func ruleIDOf(rule *policy.Rule) string {
	if rule == nil || rule.ID == "" {
		return "<unknown>"
	}
	return rule.ID
}

// requiredFilesFromRule returns the typed required_files list.
func requiredFilesFromRule(rule *policy.Rule) []policy.RequiredFile {
	if rule == nil || len(rule.RequiredFiles) == 0 {
		return nil
	}
	return rule.RequiredFiles
}

// evidenceChecksFromRule returns the typed evidence list.
func evidenceChecksFromRule(rule *policy.Rule) []policy.EvidenceCheck {
	if rule == nil || len(rule.Evidence) == 0 {
		return nil
	}
	return rule.Evidence
}

// assuranceGatesFromRule copies gate structs out of the immutable runtime plan.
// Commands needs its own copy because a struct copy would retain the nested
// slice's backing array; command evaluation must never gain mutable ownership
// of plan storage.
func assuranceGatesFromRule(rule *policy.Rule) ([]policy.AssuranceGate, error) {
	if rule == nil || len(rule.Assurance) == 0 {
		return nil, &rerrors.LockfileError{Message: "rule '" + ruleIDOf(rule) + "' missing assurance field in lockfile"}
	}
	gates := append([]policy.AssuranceGate(nil), rule.Assurance...)
	for index := range gates {
		gates[index].Commands = append([]string(nil), gates[index].Commands...)
	}
	return gates, nil
}

// numAsIntDefault is like numAsInt but returns the default when nil.
func numAsIntDefault(v interface{}, def int64) int64 {
	if v == nil {
		return def
	}
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i
		}
	}
	if i, ok := v.(int); ok {
		return int64(i)
	}
	if f, ok := v.(float64); ok {
		if float64(int64(f)) == f {
			return int64(f)
		}
	}
	return def
}

// quote wraps a string in double quotes for human-readable error
// messages without pulling in fmt.Sprintf("%q") on hot paths.
func quote(s string) string {
	return `"` + s + `"`
}

func evalDenyWrite(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	patterns := stringListField(rule, "paths")
	matched, err := matchingPathsWithMatchers(ctx.matchers, inputs.WritePaths, patterns)
	if err != nil {
		return nil, err
	}
	if len(matched) == 0 {
		return nil, nil
	}
	return buildViolation(rule, defaultMode, matched, nil, nil, nil, nil, nil), nil
}

func evalRequireRead(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	triggered, err := matchingPathsWithMatchers(ctx.matchers, inputs.WritePaths, stringListField(rule, "paths"))
	if err != nil {
		return nil, err
	}
	if len(triggered) == 0 {
		return nil, nil
	}
	required := stringListField(rule, "before_paths")
	matchedReads, err := matchingPathsWithMatchers(ctx.matchers, inputs.ReadPaths, required)
	if err != nil {
		return nil, err
	}
	if len(matchedReads) > 0 {
		return nil, nil
	}
	return buildViolation(rule, defaultMode, triggered, nil, nil, required, nil, nil), nil
}

func evalCoupleChange(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	triggered, err := matchingPathsWithMatchers(ctx.matchers, inputs.WritePaths, stringListField(rule, "paths"))
	if err != nil {
		return nil, err
	}
	if len(triggered) == 0 {
		return nil, nil
	}
	required := stringListField(rule, "when_paths")
	coupled, err := matchingPathsWithMatchers(ctx.matchers, inputs.WritePaths, required)
	if err != nil {
		return nil, err
	}
	if len(coupled) > 0 {
		return nil, nil
	}
	return buildViolation(rule, defaultMode, triggered, nil, nil, required, nil, nil), nil
}

func evalRequireClaim(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	triggered, err := matchingPathsWithMatchers(ctx.matchers, inputs.WritePaths, stringListField(rule, "when_paths"))
	if err != nil {
		return nil, err
	}
	if len(triggered) == 0 {
		return nil, nil
	}
	required := stringListField(rule, "claims")
	matchedClaims := matchingClaims(inputs.Claims, required)
	if len(matchedClaims) > 0 {
		return nil, nil
	}
	return buildViolation(rule, defaultMode, triggered, nil, nil, nil, nil, required), nil
}

func evalForbidCommand(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	required := stringListField(rule, "commands")
	forbidden := matchingForbiddenCommandsWithCache(ctx.commandCache, commandsForShellAnalysis(ctx, inputs.Commands), required, ctxRepoRoot(ctx), ruleCommandMatchMode(rule))
	if len(forbidden) == 0 {
		return nil, nil
	}
	whenPatterns := stringListField(rule, "when_paths")
	triggered := []string{}
	if len(whenPatterns) > 0 {
		var err error
		triggered, err = matchingPathsWithMatchers(ctx.matchers, inputs.WritePaths, whenPatterns)
		if err != nil {
			return nil, err
		}
		if len(triggered) == 0 {
			return nil, nil
		}
	}
	return buildViolation(rule, defaultMode, triggered, forbidden, nil, nil, nil, nil), nil
}

func commandsForShellAnalysis(ctx *evalContext, fallback []string) []string {
	if ctx != nil && ctx.preCommand {
		return ctx.currentCommands
	}
	if ctx != nil && ctx.rawCommands != nil {
		return ctx.rawCommands
	}
	return fallback
}

func compositeForbiddenCommandMatches(ctx *evalContext, rule *policy.Rule) bool {
	for _, check := range checksFromRule(rule) {
		if check.Kind != policy.KindForbidCommand {
			continue
		}
		if len(matchingForbiddenCommandsWithCache(ctx.commandCache, ctx.currentCommands, check.Commands, ctx.repoRoot, check.CommandMatch)) > 0 {
			return true
		}
	}
	return false
}

func rawCommandsPreservingSyntax(commands []string, results []CommandResult) []string {
	all := make([]string, 0, len(commands)+len(results))
	all = append(all, commands...)
	for _, result := range results {
		all = append(all, result.Command)
	}
	seen := make(map[string]struct{}, len(all))
	kept := make([]string, 0, len(all))
	for _, command := range all {
		if strings.TrimSpace(command) == "" {
			continue
		}
		if _, ok := seen[command]; ok {
			continue
		}
		seen[command] = struct{}{}
		kept = append(kept, command)
	}
	return kept
}

func evalRequireCommand(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs, requireSuccess bool) (*Violation, error) {
	triggered, err := matchingPathsWithMatchers(ctx.matchers, inputs.WritePaths, stringListField(rule, "when_paths"))
	if err != nil {
		return nil, err
	}
	if len(triggered) == 0 {
		return nil, nil
	}
	required := stringListField(rule, "commands")
	var matched []string
	repoRoot := ctxRepoRoot(ctx)
	if requireSuccess {
		minimumEpoch := latestWriteEpoch(triggered, inputs.WriteEpochs)
		matched = matchingCommandResultsSinceWithEvidence(ctx.commandEvidence, ctx.commandCache, inputs.CommandResults, required, CommandOutcomeSuccess, repoRoot, minimumEpoch, ruleCommandMatchMode(rule))
	} else {
		matched = matchingCommandsWithEvidence(ctx.commandEvidence, ctx.commandCache, inputs.Commands, required, repoRoot, ruleCommandMatchMode(rule))
	}
	if len(matched) > 0 {
		return nil, nil
	}
	return buildViolation(rule, defaultMode, triggered, nil, nil, nil, required, nil), nil
}

// ctxRepoRoot returns ctx.repoRoot if ctx is non-nil; otherwise the
// empty string. Lets matchers and evaluators safely access the root
// without nil-checking at every call site.
func ctxRepoRoot(ctx *evalContext) string {
	if ctx == nil {
		return ""
	}
	return ctx.repoRoot
}

// --- Match helpers (operate on already-normalized inputs) ---

func matchingPaths(paths, patterns []string) ([]string, error) {
	return matchingPathsWithMatchers(nil, paths, patterns)
}

func matchingPathsWithMatchers(matchers *runtimePathMatchers, paths, patterns []string) ([]string, error) {
	if len(patterns) == 0 || len(paths) == 0 {
		return nil, nil
	}
	out := []string{}
	for _, p := range paths {
		_, ok, err := matchAnyPaths(matchers, patterns, p)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func matchingCommands(commands, expected []string, repoRoot string, match policy.CommandMatch) []string {
	return matchingCommandsWithEvidence(nil, nil, commands, expected, repoRoot, match)
}

func matchingCommandsWithEvidence(evidence *commandEvidenceIndex, cache *commandInvocationCache, commands, expected []string, repoRoot string, match policy.CommandMatch) []string {
	if len(expected) == 0 {
		return nil
	}
	// Normalise both sides of the comparison so whitespace variation
	// can't defeat require_command / forbid_command, and so the literal
	// rule form matches commands that were transparently wrapped by a
	// CLI proxy (e.g. `rtk go test ...`) or anchored to an absolute
	// repo path by the agent runtime (e.g.
	// `cd /workspace/repo/sub && ...`). normalizeCommandSemantics
	// applies both transformations to expected and recorded sides.
	normalizedExpected := cache.normalizedExpectedCommands(expected, repoRoot)
	out := []string{}
	if evidence == nil {
		evidence = newCommandEvidenceIndex(ExecutionInputs{Commands: commands}, repoRoot)
	}
	for _, command := range evidence.commands {
		if commandMatchesExpected(command.normalized, normalizedExpected, match) {
			out = append(out, command.raw)
		}
	}
	return out
}

func matchingForbiddenCommands(commands, expected []string, repoRoot string, match policy.CommandMatch) []string {
	return matchingForbiddenCommandsWithCache(nil, commands, expected, repoRoot, match)
}

func matchingForbiddenCommandsWithCache(cache *commandInvocationCache, commands, expected []string, repoRoot string, match policy.CommandMatch) []string {
	normalizedExpected := cache.normalizedExpectedCommands(expected, repoRoot)
	if len(normalizedExpected) == 0 {
		return nil
	}
	out := []string{}
	for _, command := range commands {
		observed := cache.observedInvocations(command)
		if !observed.complete {
			out = append(out, command)
			continue
		}
		commandMatched := false
		for _, invocation := range observed.segments {
			for _, expectedCommand := range normalizedExpected {
				// Deny direction: fold the program name so a forbidden command
				// cannot be smuggled past the gate by changing its case on a
				// case-insensitive filesystem.
				compiled := cache.expectedMatcher(expectedCommand)
				matched, uncertain := compiled.Match(invocation, match == policy.CommandMatchPrefix, true)
				if matched || uncertain {
					commandMatched = true
					break
				}
			}
			if commandMatched {
				break
			}
		}
		if commandMatched {
			out = append(out, command)
		}
	}
	return out
}

const maxCommandSubstitutionDepth = 16

// executableCommandSegments returns every directly executable shell segment,
// including command substitutions inside double quotes and backticks. Single
// quoted text remains literal. The bounded recursion prevents adversarial hook
// payloads from turning policy evaluation into unbounded work.
func executableCommandSegments(command string, depth int) ([]shellcommand.Invocation, bool) {
	if depth > maxCommandSubstitutionDepth {
		return nil, false
	}
	return shellcommand.Invocations(command, maxCommandSubstitutionDepth-depth)
}

func matchingCommandResults(results []CommandResult, expected []string, outcome string, repoRoot string) []string {
	return matchingCommandResultsSince(results, expected, outcome, repoRoot, 0, policy.CommandMatchExact)
}

func matchingCommandResultsSince(results []CommandResult, expected []string, outcome string, repoRoot string, minimumEpoch uint64, match policy.CommandMatch) []string {
	return matchingCommandResultsSinceWithEvidence(nil, nil, results, expected, outcome, repoRoot, minimumEpoch, match)
}

func matchingCommandResultsSinceWithEvidence(evidence *commandEvidenceIndex, cache *commandInvocationCache, results []CommandResult, expected []string, outcome string, repoRoot string, minimumEpoch uint64, match policy.CommandMatch) []string {
	if len(expected) == 0 {
		return nil
	}
	// Normalise both sides of the comparison (whitespace + RTK prefix +
	// absolute repoRoot in cd). See matchingCommands for the rationale.
	normalizedExpected := cache.normalizedExpectedCommands(expected, repoRoot)
	out := []string{}
	if evidence == nil {
		evidence = newCommandEvidenceIndex(ExecutionInputs{CommandResults: results}, repoRoot)
	}
	for _, result := range evidence.results {
		if result.outcome != outcome || result.epoch < minimumEpoch {
			continue
		}
		if commandMatchesExpected(result.normalized, normalizedExpected, match) {
			out = append(out, result.raw)
			continue
		}
		// Tolerate trailing output REDIRECTIONS only (e.g. a rule
		// `cd x && go test ./...` is satisfied by a recorded
		// `cd x && go test ./... 2>&1` or `... > out.log`). Redirections
		// keep the command's own exit status, so a recorded success is
		// genuine. Pipes are deliberately NOT stripped: a pipeline's exit
		// status is the last stage's, so `go test ./... | tail` could
		// record success even when the test failed - tolerating it would
		// weaken require_command_success.
		if stripped := stripTrailingRedirects(result.normalized); stripped != result.normalized {
			if commandMatchesExpected(stripped, normalizedExpected, match) {
				out = append(out, result.raw)
			}
		}
	}
	return out
}

func normalizeExpectedCommands(expected []string, repoRoot string) []string {
	out := make([]string, 0, len(expected))
	for _, e := range expected {
		if normalized := normalizeCommandSemantics(e, repoRoot); normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

// commandMatchesExpected compares one normalized recorded command
// against the normalized expected commands. Exact mode requires
// equality; prefix mode (opt-in via command_match) also accepts the
// expected command extended at a token boundary, so
// "pip install" matches "pip install requests" but never
// "pip installer".
func commandMatchesExpected(normalized string, normalizedExpected []string, match policy.CommandMatch) bool {
	for _, e := range normalizedExpected {
		if normalized == e {
			return true
		}
		if match == policy.CommandMatchPrefix && strings.HasPrefix(normalized, e+" ") {
			return true
		}
	}
	return false
}

// ruleCommandMatchMode returns the compiled command_match mode; absent means
// exact.
func ruleCommandMatchMode(rule *policy.Rule) policy.CommandMatch {
	if rule == nil {
		return policy.CommandMatchExact
	}
	return rule.CommandMatch
}

func latestWriteEpoch(paths []string, epochs map[string]uint64) uint64 {
	var latest uint64
	for _, path := range paths {
		if epochs[path] > latest {
			latest = epochs[path]
		}
	}
	return latest
}

// stripTrailingRedirects removes trailing shell output-redirection clauses from
// a whitespace-normalized command, leaving the command and its arguments and
// any pipeline intact. It strips forms like ` 2>&1`, ` > file`, ` >>log`,
// ` 2> err`, ` < in`; it never strips a pipe (`| ...`) or a plain argument, so
// it cannot mask a failed pipeline or count a different command as success.
// Used only by require_command_success matching (matchingCommandResults), never
// by forbid_command, so forbid semantics stay exact.
func stripTrailingRedirects(cmd string) string {
	stripped, complete := shellcommand.StripTrailingRedirects(cmd)
	if !complete {
		return cmd
	}
	return stripped
}

func matchingClaims(claims, expected []string) []string {
	if len(expected) == 0 {
		return nil
	}
	// Normalise both sides of the comparison so claim whitespace
	// variation can't defeat require_claim.
	expectedSet := map[string]struct{}{}
	for _, e := range expected {
		expectedSet[normalizeWhitespace(e)] = struct{}{}
	}
	out := []string{}
	for _, c := range claims {
		if _, ok := expectedSet[normalizeWhitespace(c)]; ok {
			out = append(out, c)
		}
	}
	return out
}

// --- Violation building + explanations ---

func buildViolation(
	rule *policy.Rule,
	defaultMode policy.Mode,
	matchedPaths, matchedCommands, matchedClaims, requiredPaths, requiredCommands, requiredClaims []string,
) *Violation {
	mode := defaultMode
	if rule.Mode != "" {
		mode = rule.Mode
	}

	explanation, recommended := explainViolation(
		rule.ID, rule.Kind, rule,
		matchedPaths, matchedCommands,
		requiredPaths, requiredCommands, requiredClaims,
	)

	return &Violation{
		RuleID:            rule.ID,
		Kind:              rule.Kind,
		Mode:              mode,
		Message:           rule.Message,
		Explanation:       explanation,
		RecommendedAction: recommended,
		MatchedPaths:      coalesce(matchedPaths),
		MatchedCommands:   coalesce(matchedCommands),
		MatchedClaims:     coalesce(matchedClaims),
		RequiredPaths:     coalesce(requiredPaths),
		RequiredCommands:  coalesce(requiredCommands),
		RequiredClaims:    coalesce(requiredClaims),
		SourcePath:        rule.SourcePath,
		SourceBlockID:     rule.SourceBlockID,
	}
}

func coalesce(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func explainViolation(
	id string, kind policy.Kind, rule *policy.Rule,
	matchedPaths, matchedCommands, requiredPaths, requiredCommands, requiredClaims []string,
) (string, string) {
	pathList := joinForHumans(matchedPaths)
	commandList := joinForHumans(matchedCommands)
	requiredPathList := joinForHumans(requiredPaths)
	requiredCommandList := joinForHumans(requiredCommands)
	requiredClaimList := joinForHumans(requiredClaims)

	switch kind {
	case policy.KindDenyWrite:
		fallback := joinForHumans(stringListField(rule, "paths"))
		return fmt.Sprintf("Write activity %s matched deny_write rule '%s'.", pathList, id),
			fmt.Sprintf("Avoid writing paths matching %s.", fallback)
	case policy.KindRequireRead:
		return fmt.Sprintf("Write activity %s triggered require_read rule '%s', but no required read matched %s.", pathList, id, requiredPathList),
			fmt.Sprintf("Read at least one path matching %s before modifying %s.", requiredPathList, pathList)
	case policy.KindRequireCommand:
		return fmt.Sprintf("Write activity %s triggered require_command rule '%s', but no required command matched %s.", pathList, id, requiredCommandList),
			fmt.Sprintf("Run one of the required commands before finishing: %s.", requiredCommandList)
	case policy.KindRequireCommandSuccess:
		return fmt.Sprintf("Write activity %s triggered require_command_success rule '%s', but no required successful command matched %s.", pathList, id, requiredCommandList),
			fmt.Sprintf("Run one of the required commands successfully before finishing: %s.", requiredCommandList)
	case policy.KindForbidCommand:
		forbidden := joinForHumans(stringListField(rule, "commands"))
		whenList := joinForHumans(stringListField(rule, "when_paths"))
		if len(matchedPaths) > 0 {
			return fmt.Sprintf("Forbidden command(s) %s ran while writing %s, matching forbid_command rule '%s'.", commandList, pathList, id),
				fmt.Sprintf("Do not run %s when touching paths matching %s; revert or replace the invocation with an allowed alternative.", forbidden, whenList)
		}
		return fmt.Sprintf("Forbidden command(s) %s ran, matching forbid_command rule '%s'.", commandList, id),
			fmt.Sprintf("Do not run %s in this repository; revert or replace the invocation with an allowed alternative.", forbidden)
	case policy.KindCoupleChange:
		return fmt.Sprintf("Write activity %s triggered couple_change rule '%s', but no coupled change matched %s.", pathList, id, requiredPathList),
			fmt.Sprintf("Update at least one path matching %s alongside %s.", requiredPathList, pathList)
	case policy.KindRequireClaim:
		return fmt.Sprintf("Write activity %s triggered require_claim rule '%s', but no required claim matched %s.", pathList, id, requiredClaimList),
			fmt.Sprintf("Record one of the required claims before finishing: %s.", requiredClaimList)
	}
	return fmt.Sprintf("Rule '%s' triggered for paths %s and commands %s.", id, pathList, commandList),
		"Inspect the matched rule and input evidence, then rerun the policy check."
}

func stringListField(rule *policy.Rule, key string) []string {
	if rule == nil {
		return nil
	}
	switch key {
	case "paths":
		return rule.Paths
	case "before_paths":
		return rule.BeforePaths
	case "when_paths":
		return rule.WhenPaths
	case "commands":
		return rule.Commands
	case "claims":
		return rule.Claims
	case "scope_paths":
		return rule.ScopePaths
	default:
		return nil
	}
}

func joinForHumans(values []string) string {
	if len(values) == 0 {
		return "<none>"
	}
	if len(values) == 1 {
		return values[0]
	}
	return strings.Join(values, ", ")
}

// --- Summary + next-action ---

func summarizeReport(decision Decision, total, blocking int) string {
	if total == 0 {
		return "Policy check passed with no violations."
	}
	if decision == DecisionBlock {
		return fmt.Sprintf("Policy check found %d violation(s), including %d blocking violation(s).", total, blocking)
	}
	return fmt.Sprintf("Policy check found %d non-blocking violation(s).", total)
}

func nextActionForViolations(violations []Violation) string {
	if len(violations) == 0 {
		return ""
	}
	for _, v := range violations {
		if v.IsBlocking() {
			return v.RecommendedAction
		}
	}
	return violations[0].RecommendedAction
}
