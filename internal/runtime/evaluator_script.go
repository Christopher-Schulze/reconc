package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

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

func evaluateBatchedRequireScripts(ctx *evalContext, rules []policy.Rule, ruleIndexes []int, defaultMode policy.Mode, inputs ExecutionInputs) (batchedScriptEvaluations, error) {
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
	ruleCount := len(ruleIndexes)
	if ruleIndexes == nil {
		ruleCount = len(rules)
	}
	for index := range ruleCount {
		ruleIndex := index
		if ruleIndexes != nil {
			ruleIndex = ruleIndexes[index]
		}
		rule := &rules[ruleIndex]
		scriptPath, mode, timeoutSec, killTimeoutSec, ok := workflowAuditBatchCandidate(rule)
		if !ok {
			continue
		}
		key := workflowAuditBatchKey{scriptPath: scriptPath, timeoutSec: timeoutSec, killTimeoutSec: killTimeoutSec}
		if _, ok := groups[key]; !ok {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], workflowAuditBatchItem{
			index: index,
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
			contexts, err := ctx.collectMatchContexts(inputs.WritePaths, stringListField(item.rule, "when_paths"))
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
		outcome, err := RunScriptContext(ctx.lifecycleContext(), ctx.repoRoot, key.scriptPath, args, input, key.timeoutSec, key.killTimeoutSec)
		if outcome.Canceled {
			return results, err
		}
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
		"Write activity %s triggered require_script rule %s. %s",
		joinForHumans(triggeredPaths), quote(violation.RuleID), strings.Join(details, "; "),
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
