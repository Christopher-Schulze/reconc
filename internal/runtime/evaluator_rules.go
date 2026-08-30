package runtime

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/assurance"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
)

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
	violation, err := evaluateRuleUnbounded(ctx, rule, defaultMode, inputs)
	return boundViolationText(violation), err
}

func evaluateRuleUnbounded(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
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
	gates, err := ctx.commandCache.assuranceGatesFor(rule, ctx.repoRoot)
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
	details := newViolationTextCollector(maxViolationAggregateBytes, "; ", "failures")
	for _, finding := range findings {
		details.add("[" + finding.GateID + "] " + finding.Message)
		for _, path := range finding.Paths {
			requiredPaths.add(path)
		}
	}
	v := buildViolation(rule, defaultMode, triggered, reportedSuccessful.values(), nil, requiredPaths.values(), nil, nil)
	v.Explanation = "Native assurance failed: " + details.text()
	v.RecommendedAction = truncateViolationText(findings[0].Remediation, MaxViolationTextBytes)
	if len(findings) > 1 {
		suffix := fmt.Sprintf(" Then resolve the remaining %d assurance finding(s).", len(findings)-1)
		v.RecommendedAction = truncateViolationText(findings[0].Remediation, MaxViolationTextBytes-len(suffix)) + suffix
	}
	return v, nil
}

// evalRequireScript runs an external script for each match context.
// Each context that produces a "block" or "error" outcome contributes
// to the violation. A "pass" exit (0) clears that context.
func evalRequireScript(ctx *evalContext, rule *policy.Rule, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	whenPatterns := stringListField(rule, "when_paths")
	contexts, err := ctx.collectMatchContexts(inputs.WritePaths, whenPatterns)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return nil, nil
	}

	scriptPath := rule.Script
	if scriptPath == "" {
		return nil, &rerrors.LockfileError{Message: "rule " + quote(ruleIDOf(rule)) + " missing script field in lockfile"}
	}
	args := rule.Args
	timeoutSec := rule.TimeoutSec
	killTimeoutSec := rule.KillTimeoutSec

	failures := newViolationTextCollector(maxViolationAggregateBytes, "; ", "failures")
	triggeredPaths := newStableStringCollector([]string{})
	for _, mc := range contexts {
		triggeredPaths.add(mc.path)
		// Substitute captures into args.
		substArgs, err := SubstituteTemplateInList(args, mc.captures)
		if err != nil {
			return nil, &rerrors.RuleValidationError{Message: "script args: " + err.Error()}
		}
		captures := mc.captures
		if captures == nil {
			captures = map[string]string{}
		}
		input := ScriptInput{
			RuleID:         ruleIDOf(rule),
			RepoRoot:       ctx.repoRoot,
			Captures:       captures,
			WritePaths:     inputs.WritePaths,
			ReadPaths:      inputs.ReadPaths,
			Commands:       inputs.Commands,
			Claims:         inputs.Claims,
			CommandResults: inputs.CommandResults,
		}
		outcome, err := RunScriptContext(ctx.lifecycleContext(), ctx.repoRoot, scriptPath, substArgs, input, timeoutSec, killTimeoutSec)
		if outcome.Canceled {
			return nil, err
		}
		evaluation := classifyScriptOutcome(outcome, err, timeoutSec)
		switch evaluation.disposition {
		case scriptOutcomePass:
			continue
		case scriptOutcomeBlock:
			failures.add(fmt.Sprintf("[%s] script %s blocked: %s", mc.path, scriptPath, evaluation.detail))
		case scriptOutcomeError:
			failures.add(fmt.Sprintf("[%s] script %s error: %s", mc.path, scriptPath, evaluation.detail))
		}
	}
	if failures.count() == 0 {
		return nil, nil
	}
	v := buildViolation(rule, defaultMode, triggeredPaths.values(), nil, nil, []string{scriptPath}, nil, nil)
	v.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_script rule %s. %s",
		joinForHumans(triggeredPaths.values()), quote(v.RuleID), failures.text(),
	)
	v.RecommendedAction = scriptRecommendedAction(failures.text())
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
	contexts, err := ctx.collectMatchContexts(inputs.WritePaths, whenPatterns)
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
					Message: "rule " + quote(ruleIDOf(rule)) + " required_files path: " + err.Error(),
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
	requiredPaths := sortedKeys(allRequired)
	v := buildViolation(rule, defaultMode, triggeredPaths.values(), nil, nil, requiredPaths, nil, nil)
	parts := []string{}
	if len(missing.values()) > 0 {
		parts = append(parts, "missing: "+joinForHumans(missing.values()))
	}
	if len(stale.values()) > 0 {
		parts = append(parts, "stale: "+joinForHumans(stale.values()))
	}
	v.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_fresh_file rule %s (%s).",
		joinForHumans(triggeredPaths.values()), quote(v.RuleID), strings.Join(parts, "; "),
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

	contexts, err := ctx.collectMatchContexts(inputs.WritePaths, whenPatterns)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return nil, nil
	}

	failures := newViolationTextCollector(maxViolationAggregateBytes, "; ", "failures")
	requiredFiles := map[string]struct{}{}
	triggeredPaths := newStableStringCollector([]string{})

	for _, mc := range contexts {
		triggeredPaths.add(mc.path)
		for _, c := range checks {
			file, err := SubstituteTemplate(c.File, mc.captures)
			if err != nil {
				return nil, &rerrors.RuleValidationError{
					Message: "rule " + quote(ruleIDOf(rule)) + " evidence file: " + err.Error(),
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
			for _, reason := range match.reasons {
				failures.add(reason)
			}
		}
	}

	if failures.count() == 0 {
		return nil, nil
	}
	required := sortedKeys(requiredFiles)
	v := buildViolation(rule, defaultMode, triggeredPaths.values(), nil, nil, required, nil, nil)
	v.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_evidence rule %s. Failures: %s.",
		joinForHumans(triggeredPaths.values()), quote(v.RuleID), failures.text(),
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
			break // one match per write path is enough
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
	if len(initial) == 0 {
		return stableStringCollector{items: items}
	}
	seen := make(map[string]struct{}, len(initial))
	for _, value := range initial {
		seen[value] = struct{}{}
	}
	return stableStringCollector{items: items, seen: seen}
}

func (c *stableStringCollector) add(value string) {
	if c.seen == nil {
		c.seen = make(map[string]struct{}, len(c.items)+1)
		for _, existing := range c.items {
			c.seen[existing] = struct{}{}
		}
	}
	if _, exists := c.seen[value]; exists {
		return
	}
	c.seen[value] = struct{}{}
	c.items = append(c.items, value)
}

func (c *stableStringCollector) values() []string {
	return c.items
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

// quote returns one escaped diagnostic token without allowing untrusted values
// to break the surrounding delimiter or inject control characters.
func quote(s string) string {
	return strconv.Quote(s)
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
	required := stringListField(rule, "commands")
	forbidden := matchingForbiddenCommandsWithCache(ctx.commandCache, commandsForShellAnalysis(ctx, inputs.Commands), required, ctxRepoRoot(ctx), ruleCommandMatchMode(rule))
	if len(forbidden) == 0 {
		return nil, nil
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

// compositeRuleTriggerMatches evaluates only the parent path trigger and the
// current-command prevention trigger. It deliberately does not execute or
// otherwise evaluate any composite sub-check.
func compositeRuleTriggerMatches(ctx *evalContext, rule *policy.Rule, inputs ExecutionInputs) (bool, error) {
	if runtimeRuleContainsForbidCommand(rule) && !compositeForbiddenCommandMatches(ctx, rule) {
		return false, nil
	}
	contexts, err := ctx.collectMatchContexts(inputs.WritePaths, rule.WhenPaths)
	if err != nil {
		return false, err
	}
	return len(contexts) > 0, nil
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
