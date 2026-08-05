package runtime

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
)

// Composite rule evaluation (W26): all_of / any_of / not.
//
// A composite rule has its own when_paths to determine WHEN it
// applies, and a `checks` list that runs against the same write paths
// that triggered the parent. Each sub-check is evaluated with the
// parent's template captures substituted into the check's path / file
// / args / etc.
//
// Decision rules (per write-path context):
//   - all_of: ALL checks must pass (any check failure = composite fails)
//   - any_of: AT LEAST ONE check must pass (all-fail = composite fails)
//   - not:    the single check must FAIL (success = composite fails)
//
// Failures across multiple contexts are aggregated into ONE violation
// per rule, with the explanation listing each failing context + check.

// evalAllOf, evalAnyOf, evalNot are the per-operator dispatch entries.
// They share a common "evaluate every check in every context" workhorse
// and differ only in the fold over results.

func evalAllOf(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	contexts, checks, err := compositeSetup(rule, inputs)
	if err != nil || len(contexts) == 0 || len(checks) == 0 {
		return nil, err
	}
	failures := []string{}
	for _, mc := range contexts {
		for i, c := range checks {
			ok, reason, err := evalCheck(ctx, c, mc.captures, inputs, inputs.WriteEpochs[mc.path])
			if err != nil {
				var evalErr *checkEvalError
				if !errors.As(err, &evalErr) {
					return nil, err
				}
				ok, reason = false, evalErr.reason
			}
			if !ok {
				failures = append(failures, fmt.Sprintf("[%s][check #%d %s] %s", mc.path, i+1, c.Kind, reason))
			}
		}
	}
	if len(failures) == 0 {
		return nil, nil
	}
	return buildCompositeViolation(rule, defaultMode, contexts, "all_of", failures), nil
}

func evalAnyOf(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	contexts, checks, err := compositeSetup(rule, inputs)
	if err != nil || len(contexts) == 0 || len(checks) == 0 {
		return nil, err
	}
	contextFailures := []string{}
	for _, mc := range contexts {
		anyPassed := false
		perCheckReasons := []string{}
		for i, c := range checks {
			ok, reason, err := evalCheck(ctx, c, mc.captures, inputs, inputs.WriteEpochs[mc.path])
			if err != nil {
				var evalErr *checkEvalError
				if !errors.As(err, &evalErr) {
					return nil, err
				}
				ok, reason = false, evalErr.reason
			}
			if ok {
				anyPassed = true
				break
			}
			perCheckReasons = append(perCheckReasons, fmt.Sprintf("check #%d %s: %s", i+1, c.Kind, reason))
		}
		if !anyPassed {
			contextFailures = append(contextFailures, fmt.Sprintf("[%s] %s", mc.path, strings.Join(perCheckReasons, "; ")))
		}
	}
	if len(contextFailures) == 0 {
		return nil, nil
	}
	return buildCompositeViolation(rule, defaultMode, contexts, "any_of", contextFailures), nil
}

func evalNot(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	contexts, checks, err := compositeSetup(rule, inputs)
	if err != nil || len(contexts) == 0 {
		return nil, err
	}
	if len(checks) != 1 {
		return nil, &rerrors.LockfileError{
			Message: "compiled lockfile rule '" + ruleIDOf(rule) + "' (kind not) must have exactly one check",
		}
	}
	failures := []string{}
	for _, mc := range contexts {
		ok, _, err := evalCheck(ctx, checks[0], mc.captures, inputs, inputs.WriteEpochs[mc.path])
		if err != nil {
			var evalErr *checkEvalError
			if !errors.As(err, &evalErr) {
				return nil, err
			}
			// An inner check that could not be evaluated (missing or
			// crashing script) must fail closed: treating it as "did
			// not pass" would make `not` succeed on broken tooling.
			failures = append(failures, fmt.Sprintf("[%s] inner check could not be evaluated (%s); not fails closed", mc.path, evalErr.reason))
			continue
		}
		if ok {
			// `not` fails when the inner check passes.
			failures = append(failures, fmt.Sprintf("[%s] inner check passed (must NOT pass)", mc.path))
		}
	}
	if len(failures) == 0 {
		return nil, nil
	}
	return buildCompositeViolation(rule, defaultMode, contexts, "not", failures), nil
}

// compositeSetup gathers the match contexts and the checks list from
// the rule map. Returns empty slices when when_paths doesn't match,
// signalling "rule does not fire".
func compositeSetup(rule map[string]interface{}, inputs ExecutionInputs) ([]matchContext, []policy.Check, error) {
	patterns := stringListField(rule, "when_paths")
	contexts, err := collectMatchContexts(inputs.WritePaths, patterns)
	if err != nil {
		return nil, nil, err
	}
	checks := checksFromRule(rule)
	return contexts, checks, nil
}

// checksFromRule extracts the typed Check list from a rule map.
func checksFromRule(rule map[string]interface{}) []policy.Check {
	raw, ok := rule["checks"]
	if !ok || raw == nil {
		return nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]policy.Check, 0, len(list))
	for _, entry := range list {
		mapping, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		c, ok := checkFromMap(mapping)
		if ok {
			out = append(out, c)
		}
	}
	return out
}

// checkFromMap converts one lockfile-encoded check map to a typed Check.
func checkFromMap(m map[string]interface{}) (policy.Check, bool) {
	kindStr, _ := m["kind"].(string)
	if kindStr == "" {
		return policy.Check{}, false
	}
	c := policy.Check{Kind: policy.Kind(kindStr)}
	if v, ok := m["path"].(string); ok {
		c.Path = v
	}
	c.MaxAgeHours = int(numAsIntDefault(m["max_age_hours"], 0))
	if v, ok := m["file"].(string); ok {
		c.File = v
	}
	c.MustExist, _ = m["must_exist"].(bool)
	if list, ok := m["must_contain"].([]interface{}); ok {
		for _, x := range list {
			if s, ok := x.(string); ok {
				c.MustContain = append(c.MustContain, s)
			}
		}
	}
	if v, ok := m["must_not_contain"].(string); ok {
		c.MustNotContain = v
	}
	c.MaxLineCount = int(numAsIntDefault(m["max_line_count"], 0))
	if v, ok := m["script"].(string); ok {
		c.Script = v
	}
	if list, ok := m["args"].([]interface{}); ok {
		for _, x := range list {
			if s, ok := x.(string); ok {
				c.Args = append(c.Args, s)
			}
		}
	}
	c.TimeoutSec = int(numAsIntDefault(m["timeout_sec"], 0))
	for _, key := range []struct {
		field string
		dst   *[]string
	}{
		{"paths", &c.Paths},
		{"before_paths", &c.BeforePaths},
		{"when_paths", &c.WhenPaths},
		{"commands", &c.Commands},
		{"claims", &c.Claims},
	} {
		if list, ok := m[key.field].([]interface{}); ok {
			for _, x := range list {
				if s, ok := x.(string); ok {
					*key.dst = append(*key.dst, s)
				}
			}
		}
	}
	if matchMode, ok := m["command_match"].(string); ok {
		c.CommandMatch = policy.CommandMatch(matchMode)
	}
	c.Optional, _ = m["optional"].(bool)
	return c, true
}

// checkEvalError marks a sub-check that could not be evaluated at all
// (missing or crashing script, timeout) as opposed to one that ran and
// failed. all_of/any_of fold it into a normal check failure; `not`
// fails closed on it instead of treating "could not evaluate" as "did
// not pass".
type checkEvalError struct {
	reason string
}

func (e *checkEvalError) Error() string { return e.reason }

// evalCheck evaluates one sub-check given parent template captures.
// minimumEpoch carries the triggering write path's evidence epoch so
// require_command_success sub-checks enforce the same anti-staleness
// ordering as the top-level rule kind.
// Returns (ok, reason, err):
//   - ok=true means the check passed
//   - ok=false + non-empty reason means the check failed, with detail
//   - non-nil err is a hard error (IO, malformed rule, etc.); a
//     *checkEvalError specifically means "could not be evaluated"
//
// Each check kind has its own evaluation path. Template substitution
// applies to path-shaped fields before any file IO.
func evalCheck(ctx *evalContext, c policy.Check, captures map[string]string, inputs ExecutionInputs, minimumEpoch uint64) (bool, string, error) {
	switch c.Kind {
	case policy.KindRequireFreshFile:
		return evalCheckRequireFreshFile(ctx, c, captures)
	case policy.KindRequireEvidence:
		return evalCheckRequireEvidence(ctx, c, captures)
	case policy.KindRequireClaim:
		return evalCheckRequireClaim(c, inputs)
	case policy.KindRequireCommand:
		return evalCheckRequireCommand(ctx, c, inputs, false, 0)
	case policy.KindRequireCommandSuccess:
		return evalCheckRequireCommand(ctx, c, inputs, true, minimumEpoch)
	case policy.KindForbidCommand:
		return evalCheckForbidCommand(ctx, c, inputs)
	case policy.KindDenyWrite:
		return evalCheckDenyWrite(c, inputs)
	case policy.KindRequireScript:
		return evalCheckRequireScript(ctx, c, captures, inputs)
	}
	return false, "unsupported check kind: " + string(c.Kind), nil
}

func evalCheckRequireScript(ctx *evalContext, c policy.Check, captures map[string]string, inputs ExecutionInputs) (bool, string, error) {
	if c.Script == "" {
		return false, "", &checkEvalError{reason: "missing script field in sub-check"}
	}
	args, err := SubstituteTemplateInList(c.Args, captures)
	if err != nil {
		return false, "", &rerrors.RuleValidationError{Message: "script args: " + err.Error()}
	}
	input := ScriptInput{
		RuleID:         "<composite-subcheck>",
		RepoRoot:       ctx.repoRoot,
		Captures:       captures,
		WritePaths:     inputs.WritePaths,
		ReadPaths:      inputs.ReadPaths,
		Commands:       inputs.Commands,
		Claims:         inputs.Claims,
		CommandResults: inputs.CommandResults,
	}
	outcome, err := RunScript(ctx.repoRoot, c.Script, args, input, c.TimeoutSec, 0)
	if err != nil {
		if outcome.TimedOut {
			return false, "", &checkEvalError{reason: fmt.Sprintf("script %s timed out after %.1fs", c.Script, outcome.Duration.Seconds())}
		}
		return false, "", &checkEvalError{reason: fmt.Sprintf("script %s error: %v", c.Script, err)}
	}
	if outcome.Status == "block" {
		detail := strings.TrimSpace(outcome.Stdout)
		if detail == "" {
			detail = strings.TrimSpace(outcome.Stderr)
		}
		if detail == "" {
			detail = "no output"
		}
		return false, fmt.Sprintf("script %s blocked: %s", c.Script, detail), nil
	}
	return true, "", nil
}

func evalCheckRequireFreshFile(ctx *evalContext, c policy.Check, captures map[string]string) (bool, string, error) {
	pathSubst, err := SubstituteTemplate(c.Path, captures)
	if err != nil {
		return false, "", &rerrors.RuleValidationError{Message: "check path: " + err.Error()}
	}
	full, err := resolvePolicyFile(ctx.repoRoot, pathSubst)
	if err != nil {
		return false, "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			if c.Optional {
				return true, "", nil
			}
			return false, "missing file " + pathSubst, nil
		}
		return false, "", &rerrors.LockfileError{Message: "stat " + pathSubst, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return false, "not a regular file: " + pathSubst, nil
	}
	if c.MaxAgeHours > 0 {
		age := time.Since(info.ModTime())
		if age > time.Duration(c.MaxAgeHours)*time.Hour {
			return false, fmt.Sprintf("stale: %s (age > %dh)", pathSubst, c.MaxAgeHours), nil
		}
	}
	return true, "", nil
}

func evalCheckRequireEvidence(ctx *evalContext, c policy.Check, captures map[string]string) (bool, string, error) {
	fileSubst, err := SubstituteTemplate(c.File, captures)
	if err != nil {
		return false, "", &rerrors.RuleValidationError{Message: "check file: " + err.Error()}
	}
	full, err := resolvePolicyFile(ctx.repoRoot, fileSubst)
	if err != nil {
		return false, "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			if c.Optional {
				return true, "", nil
			}
			if c.MustExist {
				return false, "missing file " + fileSubst, nil
			}
			if len(c.MustContain) > 0 || c.MustNotContain != "" || c.MaxLineCount > 0 {
				return false, "missing file " + fileSubst + " (cannot check content)", nil
			}
			return true, "", nil
		}
		return false, "", &rerrors.LockfileError{Message: "stat " + fileSubst, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return false, "not a regular file: " + fileSubst, nil
	}
	needContent := len(c.MustContain) > 0 || c.MustNotContain != "" || c.MaxLineCount > 0
	if !needContent {
		return true, "", nil
	}
	data, err := boundedio.ReadFile(full, maxEvidenceFileBytes)
	if err != nil {
		return false, "", &rerrors.LockfileError{Message: "read " + fileSubst, Cause: err}
	}
	content := string(data)
	for _, sub := range c.MustContain {
		if !strings.Contains(content, sub) {
			return false, fileSubst + ": missing required substring " + quote(sub), nil
		}
	}
	if c.MustNotContain != "" && strings.Contains(content, c.MustNotContain) {
		return false, fileSubst + ": contains forbidden substring " + quote(c.MustNotContain), nil
	}
	if c.MaxLineCount > 0 {
		lines := strings.Count(content, "\n")
		if !strings.HasSuffix(content, "\n") && len(content) > 0 {
			lines++
		}
		if lines > c.MaxLineCount {
			return false, fmt.Sprintf("%s: %d lines > max %d", fileSubst, lines, c.MaxLineCount), nil
		}
	}
	return true, "", nil
}

func evalCheckRequireClaim(c policy.Check, inputs ExecutionInputs) (bool, string, error) {
	matched := matchingClaims(inputs.Claims, c.Claims)
	if len(matched) > 0 {
		return true, "", nil
	}
	return false, "no required claim asserted; expected one of: " + strings.Join(c.Claims, ", "), nil
}

func evalCheckRequireCommand(ctx *evalContext, c policy.Check, inputs ExecutionInputs, requireSuccess bool, minimumEpoch uint64) (bool, string, error) {
	var matched []string
	repoRoot := ctxRepoRoot(ctx)
	if requireSuccess {
		matched = matchingCommandResultsSince(inputs.CommandResults, c.Commands, CommandOutcomeSuccess, repoRoot, minimumEpoch, c.CommandMatch)
	} else {
		matched = matchingCommands(inputs.Commands, c.Commands, repoRoot, c.CommandMatch)
	}
	if len(matched) > 0 {
		return true, "", nil
	}
	verb := "ran"
	if requireSuccess {
		verb = "completed successfully (after the triggering write)"
	}
	return false, fmt.Sprintf("no required command %s; expected one of: %s", verb, strings.Join(c.Commands, ", ")), nil
}

func evalCheckForbidCommand(ctx *evalContext, c policy.Check, inputs ExecutionInputs) (bool, string, error) {
	hit := matchingForbiddenCommands(commandsForShellAnalysis(ctx, inputs.Commands), c.Commands, ctxRepoRoot(ctx), c.CommandMatch)
	if len(hit) == 0 {
		return true, "", nil
	}
	return false, "forbidden command(s) ran: " + strings.Join(hit, ", "), nil
}

func evalCheckDenyWrite(c policy.Check, inputs ExecutionInputs) (bool, string, error) {
	matched, err := matchingPaths(inputs.WritePaths, c.Paths)
	if err != nil {
		return false, "", err
	}
	if len(matched) == 0 {
		return true, "", nil
	}
	return false, "writes to forbidden paths: " + strings.Join(matched, ", "), nil
}

// buildCompositeViolation is the violation builder for composite rules.
// Aggregates triggered paths and failure reasons across all contexts.
func buildCompositeViolation(rule map[string]interface{}, defaultMode policy.Mode, contexts []matchContext, op string, failures []string) *Violation {
	triggered := []string{}
	for _, mc := range contexts {
		triggered = appendUnique(triggered, mc.path)
	}
	v := buildViolation(rule, defaultMode, triggered, nil, nil, nil, nil, nil)
	v.Explanation = fmt.Sprintf(
		"Composite rule '%s' (%s) failed on %d context(s): %s",
		v.RuleID, op, len(contexts), strings.Join(failures, "; "),
	)
	v.RecommendedAction = "Satisfy the listed sub-checks for each affected write path."
	return v
}
