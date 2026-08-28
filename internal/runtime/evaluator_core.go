package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/shellcommand"
)

// evalContext owns the root-bound path state and evaluation-local matcher,
// command, and evidence caches shared across one policy evaluation.
type evalContext struct {
	lifecycle        context.Context
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

func (ctx *evalContext) lifecycleContext() context.Context {
	if ctx != nil && ctx.lifecycle != nil {
		return ctx.lifecycle
	}
	return context.Background()
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
	return AssertRuleByIDContext(context.Background(), startPath, ruleID, vars, inputs)
}

// AssertRuleByIDContext evaluates one rule under the caller lifecycle.
func AssertRuleByIDContext(ctx context.Context, startPath, ruleID string, vars map[string]string, inputs ExecutionInputs) (*CheckReport, error) {
	return NewEvaluator().AssertRuleByIDContext(ctx, startPath, ruleID, vars, inputs)
}

// AssertRuleByID evaluates one indexed rule through this evaluator's plan.
func (e *Evaluator) AssertRuleByID(startPath, ruleID string, vars map[string]string, inputs ExecutionInputs) (*CheckReport, error) {
	return e.AssertRuleByIDContext(context.Background(), startPath, ruleID, vars, inputs)
}

// AssertRuleByIDContext evaluates one indexed rule under the caller lifecycle.
func (e *Evaluator) AssertRuleByIDContext(lifecycle context.Context, startPath, ruleID string, vars map[string]string, inputs ExecutionInputs) (*CheckReport, error) {
	if lifecycle == nil {
		return nil, errors.New("runtime evaluation context is required")
	}
	if err := lifecycle.Err(); err != nil {
		return nil, err
	}
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
		lifecycle:        lifecycle,
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
	return CheckRepoPolicyContext(context.Background(), startPath, inputs)
}

// CheckRepoPolicyContext evaluates policy under the caller lifecycle.
func CheckRepoPolicyContext(ctx context.Context, startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return NewEvaluator().CheckRepoPolicyContext(ctx, startPath, inputs)
}

// CheckRepoPolicy evaluates every rule through this evaluator's plan.
func (e *Evaluator) CheckRepoPolicy(startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return e.CheckRepoPolicyContext(context.Background(), startPath, inputs)
}

// CheckRepoPolicyContext evaluates every rule under the caller lifecycle.
func (e *Evaluator) CheckRepoPolicyContext(ctx context.Context, startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return e.checkRepoPolicy(ctx, startPath, inputs, nil, false)
}

// CheckRepoPolicyForKinds evaluates only the requested top-level rule kinds
// while keeping lockfile loading, freshness checks, path normalization and
// unsupported-kind validation identical to CheckRepoPolicy.
func CheckRepoPolicyForKinds(startPath string, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}) (*CheckReport, error) {
	return CheckRepoPolicyForKindsContext(context.Background(), startPath, inputs, allowedKinds)
}

// CheckRepoPolicyForKindsContext evaluates selected kinds under the caller lifecycle.
func CheckRepoPolicyForKindsContext(ctx context.Context, startPath string, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}) (*CheckReport, error) {
	return NewEvaluator().CheckRepoPolicyForKindsContext(ctx, startPath, inputs, allowedKinds)
}

// CheckRepoPolicyForKinds evaluates an indexed subset of top-level kinds.
func (e *Evaluator) CheckRepoPolicyForKinds(startPath string, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}) (*CheckReport, error) {
	return e.CheckRepoPolicyForKindsContext(context.Background(), startPath, inputs, allowedKinds)
}

// CheckRepoPolicyForKindsContext evaluates indexed kinds under the caller lifecycle.
func (e *Evaluator) CheckRepoPolicyForKindsContext(ctx context.Context, startPath string, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}) (*CheckReport, error) {
	return e.checkRepoPolicy(ctx, startPath, inputs, allowedKinds, false)
}

// CheckRepoPolicyForPreCommand evaluates prevention rules before a shell
// command executes. Top-level forbid_command rules and composites containing a
// forbid_command sub-check are included so composing a prevention rule never
// silently demotes it to Stop-time detection.
func CheckRepoPolicyForPreCommand(startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return CheckRepoPolicyForPreCommandContext(context.Background(), startPath, inputs)
}

// CheckRepoPolicyForPreCommandContext evaluates prevention rules under the caller lifecycle.
func CheckRepoPolicyForPreCommandContext(ctx context.Context, startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return NewEvaluator().CheckRepoPolicyForPreCommandContext(ctx, startPath, inputs)
}

// CheckRepoPolicyForPreCommand evaluates the precomputed prevention subset.
func (e *Evaluator) CheckRepoPolicyForPreCommand(startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return e.CheckRepoPolicyForPreCommandContext(context.Background(), startPath, inputs)
}

// CheckRepoPolicyForPreCommandContext evaluates the prevention subset under the caller lifecycle.
func (e *Evaluator) CheckRepoPolicyForPreCommandContext(ctx context.Context, startPath string, inputs ExecutionInputs) (*CheckReport, error) {
	return e.checkRepoPolicy(ctx, startPath, inputs, nil, true)
}

func (e *Evaluator) checkRepoPolicy(ctx context.Context, startPath string, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}, preCommand bool) (*CheckReport, error) {
	if ctx == nil {
		return nil, errors.New("runtime evaluation context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	return evaluateRuntimePlanContext(ctx, root, plan, inputs, allowedKinds, preCommand)
}

func evaluateRuntimePlan(root string, plan *runtimePlan, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}, preCommand bool) (*CheckReport, error) {
	return evaluateRuntimePlanContext(context.Background(), root, plan, inputs, allowedKinds, preCommand)
}

func evaluateRuntimePlanContext(ctx context.Context, root string, plan *runtimePlan, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}, preCommand bool) (*CheckReport, error) {
	return evaluateRuntimePlanWithRootResolverContext(ctx, root, plan, inputs, allowedKinds, preCommand, pathidentity.ResolveExisting)
}

func evaluateRuntimePlanWithRootResolver(root string, plan *runtimePlan, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}, preCommand bool, resolveRoot func(string) (string, error)) (*CheckReport, error) {
	return evaluateRuntimePlanWithRootResolverContext(context.Background(), root, plan, inputs, allowedKinds, preCommand, resolveRoot)
}

func evaluateRuntimePlanWithRootResolverContext(lifecycle context.Context, root string, plan *runtimePlan, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}, preCommand bool, resolveRoot func(string) (string, error)) (*CheckReport, error) {
	if lifecycle == nil {
		return nil, errors.New("runtime evaluation context is required")
	}
	if err := lifecycle.Err(); err != nil {
		return nil, err
	}
	normalized, err := normalizeEvaluationInputWithRootResolver(root, inputs, resolveRoot)
	if err != nil {
		return nil, err
	}
	report := NewEmptyReport(root, ingest.LockfilePath, plan.defaultMode, normalized.inputs)
	ctx := &evalContext{
		lifecycle:        lifecycle,
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
		if err := ctx.lifecycleContext().Err(); err != nil {
			return nil, err
		}
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
