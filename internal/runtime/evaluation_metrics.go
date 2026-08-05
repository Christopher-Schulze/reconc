package runtime

import (
	"fmt"
	"sort"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
)

// EvaluationMetrics is a deterministic structural cost model. It reports
// matcher work units rather than wall-clock time so repeated offline replays
// remain byte-stable and comparable across machines.
type EvaluationMetrics struct {
	RuleCount              int   `json:"rule_count"`
	EvidenceItemCount      int   `json:"evidence_item_count"`
	PatternComparisonUnits int64 `json:"pattern_comparison_units"`
	ExternalRuleCount      int   `json:"external_rule_count"`
	EstimatedUnits         int64 `json:"estimated_units"`
}

// CompiledPolicyEvaluator owns one validated in-memory runtime plan.
type CompiledPolicyEvaluator struct {
	plan *runtimePlan
}

// EvaluationTrace records which rules were actually triggered by the replay
// inputs, including satisfied rules that therefore emitted no violation.
type EvaluationTrace struct {
	MatchedRuleIDs []string
}

// NewCompiledPolicyEvaluator strictly decodes current compiler output without
// reading or publishing a repository lockfile.
func NewCompiledPolicyEvaluator(lockfile []byte) (*CompiledPolicyEvaluator, error) {
	if int64(len(lockfile)) > maxLockfileBytes {
		return nil, fmt.Errorf("compiled policy exceeds %d bytes", maxLockfileBytes)
	}
	decoded, err := decodeLockfile(lockfile)
	if err != nil {
		return nil, err
	}
	plan, err := compileRuntimePlan(decoded.payload)
	if err != nil {
		return nil, err
	}
	return &CompiledPolicyEvaluator{plan: plan}, nil
}

// Check evaluates inputs through the same typed plan and matcher used by the
// repository evaluator, while keeping the candidate lockfile in memory.
func (e *CompiledPolicyEvaluator) Check(repoRoot string, inputs ExecutionInputs) (*CheckReport, EvaluationMetrics, error) {
	report, metrics, _, err := e.CheckWithTrace(repoRoot, inputs)
	return report, metrics, err
}

// CheckWithTrace evaluates one immutable plan and returns exact trigger
// evidence in addition to the production report.
func (e *CompiledPolicyEvaluator) CheckWithTrace(repoRoot string, inputs ExecutionInputs) (*CheckReport, EvaluationMetrics, EvaluationTrace, error) {
	if e == nil || e.plan == nil {
		return nil, EvaluationMetrics{}, EvaluationTrace{}, fmt.Errorf("compiled policy evaluator is nil")
	}
	_, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return nil, EvaluationMetrics{}, EvaluationTrace{}, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	report, err := evaluateRuntimePlan(repoRoot, e.plan, inputs, nil, false)
	if err != nil {
		return nil, EvaluationMetrics{}, EvaluationTrace{}, err
	}
	matched, err := matchedRuleIDs(repoRoot, e.plan.rules, report.Inputs, inputs)
	if err != nil {
		return nil, EvaluationMetrics{}, EvaluationTrace{}, err
	}
	return report, evaluationMetrics(e.plan.rules, report.Inputs), EvaluationTrace{MatchedRuleIDs: matched}, nil
}

// RuleIDs returns every compiled candidate rule in stable lexical order.
func (e *CompiledPolicyEvaluator) RuleIDs() []string {
	if e == nil || e.plan == nil {
		return []string{}
	}
	ids := make([]string, 0, len(e.plan.rules))
	for _, rule := range e.plan.rules {
		ids = append(ids, rule.ID)
	}
	sort.Strings(ids)
	return ids
}

// RequireScriptRuleIDs returns every external-script rule before evaluation so
// callers that promise side-effect-free inspection can fail closed.
func (e *CompiledPolicyEvaluator) RequireScriptRuleIDs() []string {
	if e == nil || e.plan == nil {
		return []string{}
	}
	return requireScriptRuleIDs(e.plan.rules)
}

// CheckRepoPolicyWithMetrics validates the current repository lock and returns
// the same report plus deterministic structural cost evidence.
func (e *Evaluator) CheckRepoPolicyWithMetrics(startPath string, inputs ExecutionInputs) (*CheckReport, EvaluationMetrics, error) {
	discovery, err := ingest.DiscoverPolicyRepo(startPath)
	if err != nil {
		return nil, EvaluationMetrics{}, err
	}
	if !discovery.Discovered {
		return nil, EvaluationMetrics{}, fmt.Errorf("no policy markers discovered")
	}
	plan, err := e.loadFreshRuntimePlan(discovery.RepoRoot)
	if err != nil {
		return nil, EvaluationMetrics{}, err
	}
	report, err := evaluateRuntimePlan(discovery.RepoRoot, plan, inputs, nil, false)
	if err != nil {
		return nil, EvaluationMetrics{}, err
	}
	return report, evaluationMetrics(plan.rules, report.Inputs), nil
}

// CurrentCompiledPolicyEvaluator validates and snapshots the current immutable
// plan without evaluating it. The source digest lets comparison callers prove
// that separately compiled candidates used the same source snapshot.
func (e *Evaluator) CurrentCompiledPolicyEvaluator(startPath string) (*CompiledPolicyEvaluator, string, error) {
	discovery, err := ingest.DiscoverPolicyRepo(startPath)
	if err != nil {
		return nil, "", err
	}
	if !discovery.Discovered {
		return nil, "", fmt.Errorf("no policy markers discovered")
	}
	plan, err := e.loadFreshRuntimePlan(discovery.RepoRoot)
	if err != nil {
		return nil, "", err
	}
	return &CompiledPolicyEvaluator{plan: plan}, plan.sourceDigest, nil
}

// NormalizeReplayInputs applies production path, command, result, and claim
// normalization without evaluating a policy.
func NormalizeReplayInputs(repoRoot string, inputs ExecutionInputs) (ExecutionInputs, error) {
	normalized, err := normalizeEvaluationInput(repoRoot, inputs)
	if err != nil {
		return Empty(), err
	}
	return normalized.inputs, nil
}

type normalizedEvaluationInputSet struct {
	inputs          ExecutionInputs
	rawCommands     []string
	currentCommands []string
}

func normalizeEvaluationInput(root string, inputs ExecutionInputs) (normalizedEvaluationInputSet, error) {
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return normalizedEvaluationInputSet{}, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	reads, err := normalizePathsWithResolvedRoot(inputs.ReadPaths, resolvedRoot)
	if err != nil {
		return normalizedEvaluationInputSet{}, err
	}
	writes, err := normalizePathsWithResolvedRoot(inputs.WritePaths, resolvedRoot)
	if err != nil {
		return normalizedEvaluationInputSet{}, err
	}
	epochs, err := normalizeWriteEpochsWithResolvedRoot(inputs.WritePaths, inputs.WriteEpochs, resolvedRoot)
	if err != nil {
		return normalizedEvaluationInputSet{}, err
	}
	return finishInputNormalization(inputs, reads, writes, epochs), nil
}

func finishInputNormalization(inputs ExecutionInputs, reads, writes []string, epochs map[string]uint64) normalizedEvaluationInputSet {
	results := normalizeCommandResults(inputs.CommandResults)
	commands := append([]string{}, inputs.Commands...)
	for _, result := range results {
		commands = append(commands, result.Command)
	}
	return normalizedEvaluationInputSet{
		inputs: ExecutionInputs{
			ReadPaths: reads, WritePaths: writes, WriteEpochs: epochs,
			Commands: dedupePreservingOrder(normalizeCommands(commands)),
			Claims:   normalizeCommands(inputs.Claims), CommandResults: results,
		},
		rawCommands:     rawCommandsPreservingSyntax(inputs.Commands, inputs.CommandResults),
		currentCommands: rawCommandsPreservingSyntax(inputs.Commands, nil),
	}
}

func evaluationMetrics(rules []policy.Rule, inputs ExecutionInputs) EvaluationMetrics {
	metrics := EvaluationMetrics{RuleCount: len(rules), EvidenceItemCount: evidenceItemCount(inputs)}
	for index := range rules {
		metrics.PatternComparisonUnits += ruleComparisonUnits(&rules[index], inputs)
		metrics.ExternalRuleCount += requireScriptBoundaryCount(&rules[index])
	}
	metrics.EstimatedUnits = int64(metrics.RuleCount+metrics.EvidenceItemCount+metrics.ExternalRuleCount) + metrics.PatternComparisonUnits
	return metrics
}

func requireScriptRuleIDs(rules []policy.Rule) []string {
	ids := []string{}
	for index := range rules {
		if requireScriptBoundaryCount(&rules[index]) > 0 {
			ids = append(ids, rules[index].ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func requireScriptBoundaryCount(rule *policy.Rule) int {
	count := 0
	if rule.Kind == policy.KindRequireScript {
		count++
	}
	for _, check := range rule.Checks {
		if check.Kind == policy.KindRequireScript {
			count++
		}
	}
	return count
}

func matchedRuleIDs(repoRoot string, rules []policy.Rule, normalized, original ExecutionInputs) ([]string, error) {
	ctx := &evalContext{
		repoRoot:    repoRoot,
		rawCommands: rawCommandsPreservingSyntax(original.Commands, original.CommandResults),
	}
	ids := []string{}
	for index := range rules {
		matched, err := ruleTriggerMatches(ctx, &rules[index], normalized)
		if err != nil {
			return nil, err
		}
		if matched {
			ids = append(ids, rules[index].ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func ruleTriggerMatches(ctx *evalContext, rule *policy.Rule, inputs ExecutionInputs) (bool, error) {
	scopeMatched, err := ruleScopeMatches(rule, inputs)
	if err != nil {
		return true, nil
	}
	if !scopeMatched {
		return false, nil
	}
	var paths []string
	switch rule.Kind {
	case policy.KindDenyWrite:
		paths, err = matchingPaths(inputs.WritePaths, rule.Paths)
	case policy.KindRequireRead, policy.KindCoupleChange:
		paths, err = matchingPaths(inputs.WritePaths, rule.Paths)
	case policy.KindForbidCommand:
		commands := matchingForbiddenCommands(ctx.rawCommands, rule.Commands, ctx.repoRoot, rule.CommandMatch)
		if len(commands) == 0 {
			return false, nil
		}
		if len(rule.WhenPaths) == 0 {
			return true, nil
		}
		paths, err = matchingPaths(inputs.WritePaths, rule.WhenPaths)
	case policy.KindRequireFreshFile, policy.KindRequireEvidence,
		policy.KindAllOf, policy.KindAnyOf, policy.KindNot, policy.KindRequireScript:
		var contexts []matchContext
		contexts, err = collectMatchContexts(inputs.WritePaths, rule.WhenPaths)
		return len(contexts) > 0, err
	default:
		paths, err = matchingPaths(inputs.WritePaths, rule.WhenPaths)
	}
	return len(paths) > 0, err
}

func evidenceItemCount(inputs ExecutionInputs) int {
	return len(inputs.ReadPaths) + len(inputs.WritePaths) + len(inputs.Commands) + len(inputs.Claims) + len(inputs.CommandResults)
}

func ruleComparisonUnits(rule *policy.Rule, inputs ExecutionInputs) int64 {
	reads, writes := int64(len(inputs.ReadPaths)), int64(len(inputs.WritePaths))
	commands := int64(len(inputs.Commands) + len(inputs.CommandResults))
	claims := int64(len(inputs.Claims))
	units := int64(len(rule.ScopePaths)) * (reads + writes)
	switch rule.Kind {
	case policy.KindDenyWrite:
		units += int64(len(rule.Paths)) * writes
	case policy.KindRequireRead:
		units += int64(len(rule.Paths))*writes + int64(len(rule.BeforePaths))*reads
	case policy.KindRequireCommand, policy.KindRequireCommandSuccess, policy.KindForbidCommand:
		units += int64(len(rule.WhenPaths))*writes + int64(len(rule.Commands))*commands
	case policy.KindCoupleChange:
		units += int64(len(rule.Paths)+len(rule.WhenPaths)) * writes
	case policy.KindRequireClaim:
		units += int64(len(rule.WhenPaths))*writes + int64(len(rule.Claims))*claims
	default:
		units += int64(len(rule.WhenPaths)) * writes
	}
	return units
}
