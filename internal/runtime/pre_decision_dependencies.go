package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

// MaxPreDecisionDependencyPaths bounds the complete rule-derived path plan
// before the hook performs any filesystem identity work.
const MaxPreDecisionDependencyPaths = 2048

// PreDecisionRoute identifies the exact indexed policy subset evaluated by a
// pre-execution hook.
type PreDecisionRoute uint8

const (
	PreDecisionRouteCommand PreDecisionRoute = iota + 1
	PreDecisionRouteWrite
)

// PreDecisionDependency is one concrete repository-relative path read by a
// reachable rule. FreshnessHours contains every positive age boundary that
// can change the rule result while the path itself remains unchanged.
type PreDecisionDependency struct {
	Path           string
	FreshnessHours []int
	ContentBound   bool
}

// PreDecisionDependencyPlan is the bounded static dependency surface of one
// normalized pre-decision. ScriptEnvironmentIdentity binds the exact filtered
// environment supplied to a reached policy script.
type PreDecisionDependencyPlan struct {
	Dependencies              []PreDecisionDependency
	ScriptEnvironmentIdentity string
	Cacheable                 bool
}

// PreDecisionDependencies derives the external inputs of the same immutable
// rule subset and trigger surface used by pre-command or pre-write evaluation.
// It does not execute rules or touch dependency paths.
func (e *CompiledPolicyEvaluator) PreDecisionDependencies(
	repoRoot string,
	inputs ExecutionInputs,
	route PreDecisionRoute,
) (PreDecisionDependencyPlan, error) {
	if e == nil || e.plan == nil {
		return PreDecisionDependencyPlan{}, fmt.Errorf("compiled policy evaluator is nil")
	}
	phase, err := preDecisionEvaluationPhase(route)
	if err != nil {
		return PreDecisionDependencyPlan{}, err
	}
	plan := e.planForRoot(repoRoot)
	ruleIndexes := plan.indexesFor(nil, phase)
	if len(ruleIndexes) == 0 {
		// No rule can consume command or claim evidence on this route. Keep
		// every path input so empty policies still reject invalid boundaries.
		inputs = ExecutionInputs{ReadPaths: inputs.ReadPaths, WritePaths: inputs.WritePaths, WriteEpochs: inputs.WriteEpochs}
	}
	normalized, err := normalizeEvaluationInput(repoRoot, inputs)
	if err != nil {
		return PreDecisionDependencyPlan{}, err
	}
	if len(ruleIndexes) == 0 {
		if err := normalized.paths.revalidateRoot(); err != nil {
			return PreDecisionDependencyPlan{}, err
		}
		return PreDecisionDependencyPlan{Dependencies: []PreDecisionDependency{}, Cacheable: true}, nil
	}
	ctx := &evalContext{
		repoRoot:         repoRoot,
		paths:            normalized.paths,
		rawCommands:      normalized.rawCommands,
		currentCommands:  normalized.currentCommands,
		preCommand:       phase == evaluationPreCommand,
		preWrite:         phase == evaluationPreWrite,
		matchers:         plan.pathMatchers,
		templateMatchers: plan.templateMatchers,
		commandCache:     newCommandInvocationCache(plan.commandExpectations),
		contextMemo:      newMatchContextMemo(normalized.inputs.WritePaths),
	}
	collector := newPreDecisionDependencyCollector()
	for _, ruleIndex := range ruleIndexes {
		rule := &plan.rules[ruleIndex]
		scopeMatched, scopeErr := ruleScopeMatchesWithMatchers(ctx.matchers, rule, normalized.inputs)
		if scopeErr != nil {
			return PreDecisionDependencyPlan{}, scopeErr
		}
		if !scopeMatched {
			continue
		}
		if rule.Kind.IsComposite() {
			if err := collectPreDecisionCompositeDependencies(ctx, rule, normalized.inputs, phase, collector); err != nil {
				return PreDecisionDependencyPlan{}, err
			}
			continue
		}
		reachable, triggerErr := ruleTriggerMatches(ctx, rule, normalized.inputs)
		if triggerErr != nil {
			return PreDecisionDependencyPlan{}, triggerErr
		}
		if reachable {
			if err := collector.addRule(*rule, nil); err != nil {
				return PreDecisionDependencyPlan{}, err
			}
		}
	}
	if err := normalized.paths.revalidateRoot(); err != nil {
		return PreDecisionDependencyPlan{}, err
	}
	return collector.plan(), nil
}

func preDecisionEvaluationPhase(route PreDecisionRoute) (evaluationPhase, error) {
	switch route {
	case PreDecisionRouteCommand:
		return evaluationPreCommand, nil
	case PreDecisionRouteWrite:
		return evaluationPreWrite, nil
	default:
		return evaluationComplete, fmt.Errorf("unsupported pre-decision route %d", route)
	}
}

func collectPreDecisionCompositeDependencies(
	ctx *evalContext,
	rule *policy.Rule,
	inputs ExecutionInputs,
	phase evaluationPhase,
	collector *preDecisionDependencyCollector,
) error {
	if phase == evaluationPreCommand {
		reachable, err := compositeRuleTriggerMatches(ctx, rule, inputs)
		if err != nil || !reachable {
			return err
		}
	}
	contexts, err := ctx.collectMatchContexts(inputs.WritePaths, rule.WhenPaths)
	if err != nil || len(contexts) == 0 {
		return err
	}
	for _, context := range contexts {
		for _, check := range rule.Checks {
			if phase == evaluationPreWrite && rule.Kind == policy.KindAllOf && check.Kind != policy.KindDenyWrite {
				continue
			}
			if err := collector.addCheck(check, context.captures); err != nil {
				return err
			}
		}
	}
	return nil
}

type preDecisionDependencyValue struct {
	freshness    map[int]struct{}
	contentBound bool
}

type preDecisionDependencyCollector struct {
	values       map[string]*preDecisionDependencyValue
	cacheable    bool
	usesScript   bool
	overCapacity bool
}

func newPreDecisionDependencyCollector() *preDecisionDependencyCollector {
	return &preDecisionDependencyCollector{
		values:    make(map[string]*preDecisionDependencyValue),
		cacheable: true,
	}
}

func (c *preDecisionDependencyCollector) addRule(rule policy.Rule, captures map[string]string) error {
	switch rule.Kind {
	case policy.KindRequireFreshFile:
		for _, required := range rule.RequiredFiles {
			if err := c.add(required.Path, required.MaxAgeHours, true, captures); err != nil {
				return err
			}
		}
	case policy.KindRequireEvidence:
		for _, evidence := range rule.Evidence {
			if err := c.add(evidence.File, 0, true, captures); err != nil {
				return err
			}
		}
	case policy.KindRequireScript:
		return c.addScript(rule.Script, rule.CacheInputs, captures)
	case policy.KindRequireAssurance:
		c.cacheable = false
		for _, gate := range rule.Assurance {
			if gate.ProofFile != "" {
				if err := c.add(gate.ProofFile, gate.MaxAgeHours, true, captures); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *preDecisionDependencyCollector) addCheck(check policy.Check, captures map[string]string) error {
	switch check.Kind {
	case policy.KindRequireFreshFile:
		return c.add(check.Path, check.MaxAgeHours, true, captures)
	case policy.KindRequireEvidence:
		return c.add(check.File, 0, true, captures)
	case policy.KindRequireScript:
		return c.addScript(check.Script, check.CacheInputs, captures)
	}
	return nil
}

func (c *preDecisionDependencyCollector) addScript(script string, inputs []string, captures map[string]string) error {
	c.usesScript = true
	if len(inputs) == 0 {
		c.cacheable = false
	}
	if err := c.add(script, 0, true, captures); err != nil {
		return err
	}
	for _, input := range inputs {
		if err := c.add(input, 0, true, captures); err != nil {
			return err
		}
	}
	return nil
}

func (c *preDecisionDependencyCollector) add(path string, freshnessHours int, contentBound bool, captures map[string]string) error {
	resolved, err := SubstituteTemplate(path, captures)
	if err != nil {
		c.cacheable = false
		return err
	}
	if strings.TrimSpace(resolved) == "" {
		c.cacheable = false
		return fmt.Errorf("pre-decision dependency path is empty")
	}
	if !runtimePlanRepoRelativePath(resolved) {
		c.cacheable = false
		return fmt.Errorf("pre-decision dependency %q is not a safe repository-relative path", resolved)
	}
	value, exists := c.values[resolved]
	if !exists {
		if len(c.values) >= MaxPreDecisionDependencyPaths {
			c.cacheable = false
			c.overCapacity = true
			return nil
		}
		value = &preDecisionDependencyValue{freshness: make(map[int]struct{})}
		c.values[resolved] = value
	}
	value.contentBound = value.contentBound || contentBound
	if freshnessHours > 0 {
		value.freshness[freshnessHours] = struct{}{}
	}
	return nil
}

func (c *preDecisionDependencyCollector) plan() PreDecisionDependencyPlan {
	plan := PreDecisionDependencyPlan{Cacheable: c.cacheable && !c.overCapacity}
	paths := make([]string, 0, len(c.values))
	for path := range c.values {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	plan.Dependencies = make([]PreDecisionDependency, 0, len(paths))
	for _, path := range paths {
		value := c.values[path]
		freshness := make([]int, 0, len(value.freshness))
		for hours := range value.freshness {
			freshness = append(freshness, hours)
		}
		sort.Ints(freshness)
		plan.Dependencies = append(plan.Dependencies, PreDecisionDependency{
			Path: path, FreshnessHours: freshness, ContentBound: value.contentBound,
		})
	}
	if c.usesScript {
		plan.ScriptEnvironmentIdentity = preDecisionScriptEnvironmentIdentity()
	}
	return plan
}

func preDecisionScriptEnvironmentIdentity() string {
	hash := sha256.New()
	for _, value := range sanitizedEnv() {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
