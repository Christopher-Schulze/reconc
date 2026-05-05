package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/compiler"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
)

// repoRootKey is a context-only constant used by evidence-evaluating
// rule kinds (require_fresh_file, require_evidence) so they can resolve
// repo-relative paths against the discovered root without re-discovering.
type evalContext struct {
	repoRoot string
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
// This is the primitive behind `reconc assert` (W27) which replaces
// Golem-Office's per-assertion subcommands with one generic one.
func AssertRuleByID(startPath, ruleID string, vars map[string]string, inputs ExecutionInputs) (*CheckReport, error) {
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

	payload, err := loadLockfile(root)
	if err != nil {
		return nil, err
	}
	if err := validateLockfileFreshness(root, payload); err != nil {
		return nil, err
	}

	defaultMode := policy.Mode(payload["default_mode"].(string))

	rulesRaw, _ := payload["rules"].([]interface{})
	var target map[string]interface{}
	for _, r := range rulesRaw {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := m["id"].(string); id == ruleID {
			target = m
			break
		}
	}
	if target == nil {
		return nil, &rerrors.LockfileError{Message: "rule '" + ruleID + "' not found in compiled lockfile"}
	}

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
	mergedWrites := append([]string{}, inputs.WritePaths...)
	for _, p := range synthesized {
		mergedWrites = appendUnique(mergedWrites, p)
	}
	merged := inputs
	merged.WritePaths = mergedWrites

	normalizedReads, err := normalizePaths(merged.ReadPaths, root)
	if err != nil {
		return nil, err
	}
	normalizedWrites, err := normalizePaths(merged.WritePaths, root)
	if err != nil {
		return nil, err
	}
	normalizedResults := normalizeCommandResults(merged.CommandResults)
	commandsForDedupe := append([]string{}, merged.Commands...)
	for _, r := range normalizedResults {
		commandsForDedupe = append(commandsForDedupe, r.Command)
	}
	normalizedCommands := dedupePreservingOrder(normalizeCommands(commandsForDedupe))
	normalizedClaims := normalizeCommands(merged.Claims)

	normalizedInputs := ExecutionInputs{
		ReadPaths:      normalizedReads,
		WritePaths:     normalizedWrites,
		Commands:       normalizedCommands,
		Claims:         normalizedClaims,
		CommandResults: normalizedResults,
	}

	report := NewEmptyReport(root, ingest.LockfilePath, defaultMode, normalizedInputs)
	ctx := &evalContext{repoRoot: root}

	v, err := evaluateRule(ctx, target, defaultMode, normalizedInputs)
	if err != nil {
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

	payload, err := loadLockfile(root)
	if err != nil {
		return nil, err
	}
	if err := validateLockfileFreshness(root, payload); err != nil {
		return nil, err
	}

	defaultMode := policy.Mode(payload["default_mode"].(string))

	normalizedReads, err := normalizePaths(inputs.ReadPaths, root)
	if err != nil {
		return nil, err
	}
	normalizedWrites, err := normalizePaths(inputs.WritePaths, root)
	if err != nil {
		return nil, err
	}
	normalizedResults := normalizeCommandResults(inputs.CommandResults)
	commandsForDedupe := append([]string{}, inputs.Commands...)
	for _, r := range normalizedResults {
		commandsForDedupe = append(commandsForDedupe, r.Command)
	}
	normalizedCommands := dedupePreservingOrder(normalizeCommands(commandsForDedupe))
	normalizedClaims := normalizeCommands(inputs.Claims)

	normalizedInputs := ExecutionInputs{
		ReadPaths:      normalizedReads,
		WritePaths:     normalizedWrites,
		Commands:       normalizedCommands,
		Claims:         normalizedClaims,
		CommandResults: normalizedResults,
	}

	report := NewEmptyReport(root, ingest.LockfilePath, defaultMode, normalizedInputs)

	rulesRaw, ok := payload["rules"].([]interface{})
	if !ok {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain a 'rules' list"}
	}
	ctx := &evalContext{repoRoot: root}
	for _, ruleRaw := range rulesRaw {
		ruleMap, ok := ruleRaw.(map[string]interface{})
		if !ok {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile contains a non-object rule entry"}
		}
		v, err := evaluateRule(ctx, ruleMap, defaultMode, normalizedInputs)
		if err != nil {
			return nil, err
		}
		if v != nil {
			report.Violations = append(report.Violations, *v)
		}
	}

	report.Finalize()
	report.NextAction = nextActionForViolations(report.Violations)
	report.Summary = summarizeReport(report.Decision, report.ViolationCount, report.BlockingViolationCount)
	return &report, nil
}

// --- Lockfile loading + freshness ---

func loadLockfile(root string) (map[string]interface{}, error) {
	lf := filepath.Join(root, ingest.LockfilePath)
	data, err := os.ReadFile(lf)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("compiled lockfile not found at %s; run `reconc compile` before `reconc check`", ingest.LockfilePath)
		}
		return nil, &rerrors.LockfileError{Message: "read lockfile", Cause: err}
	}
	var payload map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile is not valid JSON", Cause: err}
	}
	if payload == nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain a JSON object at the top level"}
	}
	// Route every non-current payload through the migration table
	// before validating the rest of the contract. Today v0.4 keeps
	// format_version at "1", so this is a no-op; when a future
	// release does bump the shape, older lockfiles get either a real
	// migration path or a precise "no migration registered" error
	// instead of the generic version mismatch.
	migrated, _, err := compiler.MigrateLockfile(payload)
	if err != nil {
		return nil, err
	}
	payload = migrated

	// Accept the lockfile's schema URL if it matches either the
	// default upstream URL or the current resolver output (so enterprise
	// deployments with a custom RECONC_SCHEMA_BASE_URL can still read
	// lockfiles written by upstream reconc, and vice versa).
	schemaGot, _ := payload["$schema"].(string)
	if schemaGot != compiler.DefaultLockfileSchema && schemaGot != compiler.LockfileSchema() {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile schema does not match this checker; re-run `reconc compile` to refresh it"}
	}
	// Compare both sides after EvalSymlinks so macOS /var <->
	// /private/var drift doesn't reject legitimate lockfiles.
	// Fall back to the raw path on EvalSymlinks error (path may no
	// longer exist, which produces a different error in the next
	// stage of evaluation).
	storedRoot, _ := payload["repo_root"].(string)
	if !sameCanonicalPath(storedRoot, root) {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile repo_root does not match the discovered repository root; re-run `reconc compile`"}
	}

	defaultMode, _ := payload["default_mode"].(string)
	if !policy.Mode(defaultMode).Valid() {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile has invalid default_mode: " + defaultMode}
	}

	rulesRaw, ok := payload["rules"].([]interface{})
	if !ok {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain a 'rules' list"}
	}
	ruleCountNum, _ := payload["rule_count"].(json.Number)
	expectedCount, err := ruleCountNum.Int64()
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain an integer 'rule_count'"}
	}
	if int(expectedCount) != len(rulesRaw) {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile rule_count does not match the embedded rules; re-run `reconc compile`"}
	}

	return payload, nil
}

func validateLockfileFreshness(root string, payload map[string]interface{}) error {
	bundle, err := ingest.LoadPolicySources(root)
	if err != nil {
		return err
	}
	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		return err
	}

	if string(parsed.DefaultMode) != payload["default_mode"].(string) {
		return &rerrors.LockfileError{Message: "compiled lockfile default_mode does not match the current policy sources; re-run `reconc compile`"}
	}
	if int(numAsInt(payload["rule_count"])) != len(parsed.Rules) {
		return &rerrors.LockfileError{Message: "compiled lockfile rule_count does not match the current policy sources; re-run `reconc compile`"}
	}

	currentDigest := compiler.ComputeSourceDigest(bundle)
	stored, _ := payload["source_digest"].(string)
	if len(stored) != 64 {
		return &rerrors.LockfileError{Message: "compiled lockfile source_digest is missing or invalid; re-run `reconc compile` to refresh it"}
	}
	if stored != currentDigest {
		return &rerrors.LockfileError{Message: "compiled lockfile source_digest does not match the current policy sources; re-run `reconc compile`"}
	}
	return nil
}

func numAsInt(v interface{}) int64 {
	if n, ok := v.(json.Number); ok {
		i, _ := n.Int64()
		return i
	}
	if i, ok := v.(int); ok {
		return int64(i)
	}
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

// --- Path / command normalization ---

// resolveAncestorSymlinks walks up from the given path until it
// finds an existing directory, resolves THAT directory's symlinks,
// then re-appends the unresolved suffix. Used by normalizePaths so
// a --write input for a not-yet-existing file still catches a
// symlinked-parent escape.
//
// Example:
//
//	path:   /repo/escape/secret          (doesn't exist)
//	parent: /repo/escape                 (exists, symlink to /tmp/x)
//	result: /tmp/x/secret
func resolveAncestorSymlinks(path string) string {
	suffix := ""
	current := path
	for {
		if current == "" || current == "/" || current == "." {
			return path
		}
		if _, err := os.Stat(current); err == nil {
			if resolved, err := filepath.EvalSymlinks(current); err == nil {
				if suffix == "" {
					return resolved
				}
				return filepath.Join(resolved, suffix)
			}
			return path
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		base := filepath.Base(current)
		if suffix == "" {
			suffix = base
		} else {
			suffix = filepath.Join(base, suffix)
		}
		current = parent
	}
}

// sameCanonicalPath reports whether two paths refer to the same
// filesystem location after symlink resolution. Used by the lockfile
// loader so macOS /var <-> /private/var symlink drift doesn't reject
// a legitimate lockfile.
//
// EvalSymlinks can fail when a path doesn't exist. In that case we
// fall back to filepath.Clean comparison, which is the best we can
// do without resolving.
func sameCanonicalPath(a, b string) bool {
	if a == b {
		return true
	}
	ca, aerr := filepath.EvalSymlinks(a)
	cb, berr := filepath.EvalSymlinks(b)
	if aerr == nil && berr == nil {
		return ca == cb
	}
	// One or both paths don't resolve; fall back to cleaned strings.
	return filepath.Clean(a) == filepath.Clean(b)
}

func normalizePaths(paths []string, root string) ([]string, error) {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repo root: %w", err)
	}
	// Canonicalise the root via EvalSymlinks so the symlink-resolved
	// input paths have a matching root to compute Rel against.
	// Without this the macOS /var vs /private/var drift makes every
	// post-symlink-resolved input look like it escapes.
	if canon, err := filepath.EvalSymlinks(resolvedRoot); err == nil {
		resolvedRoot = canon
	}
	out := []string{}
	for _, raw := range paths {
		candidate := strings.TrimSpace(raw)
		if candidate == "" {
			continue
		}
		// Runtime evidence may come from Windows shells or cross-platform
		// agents. Policy paths are always repo-relative POSIX, so treat
		// backslashes as separators before boundary checks and matching.
		candidate = strings.ReplaceAll(candidate, "\\", "/")
		var absPath string
		if filepath.IsAbs(candidate) {
			absPath = candidate
		} else {
			absPath = filepath.Join(resolvedRoot, candidate)
		}
		// Resolve symlinks where possible; tolerate non-existent paths
		// (we may be checking writes BEFORE the file is created).
		cleaned := filepath.Clean(absPath)
		// Follow symlinks after Clean so an attacker-controlled symlink
		// (e.g. `src/evil -> /etc/passwd`) cannot escape the repo boundary.
		//
		// Two-phase resolution: first try the full path (works for
		// existing files). If that fails (which is the common case
		// for --write inputs where the target is about to be
		// created), walk up to the closest EXISTING parent, resolve
		// it, and re-join with the unresolved suffix. This catches
		// escape-via-symlinked-parent even when the leaf doesn't
		// exist yet.
		if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
			cleaned = resolved
		} else {
			cleaned = resolveAncestorSymlinks(cleaned)
		}

		rel, err := filepath.Rel(resolvedRoot, cleaned)
		if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
			return nil, &rerrors.RepoBoundaryError{Path: raw, RepoRoot: resolvedRoot}
		}
		// Convert OS-native to POSIX
		posix := filepath.ToSlash(rel)
		if posix == "." {
			continue
		}
		out = append(out, posix)
	}
	return out, nil
}

// normalizeWhitespace collapses every run of whitespace (spaces,
// tabs, newlines) into a single space and trims leading/trailing
// whitespace. Used for command + claim matching so policy-side
// `"go test"` matches agent-reported `"go  test"` / `"go\ttest"`
// Empty / whitespace-only strings become empty.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func normalizeCommands(commands []string) []string {
	out := make([]string, 0, len(commands))
	for _, c := range commands {
		norm := normalizeWhitespace(c)
		if norm != "" {
			out = append(out, norm)
		}
	}
	return out
}

func normalizeCommandResults(results []CommandResult) []CommandResult {
	out := make([]CommandResult, 0, len(results))
	for _, r := range results {
		c := normalizeWhitespace(r.Command)
		if c == "" {
			continue
		}
		out = append(out, CommandResult{Command: c, Outcome: r.Outcome})
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

// ruleScopeMatches reports whether a scope-scoped rule should fire
// for the given inputs.  A global rule (empty scope_paths) always
// matches.  A scoped rule matches when any read or write path falls
// under one of its scope patterns.
//
// Pattern-compile errors are propagated instead of silently treated
// as "no match". A malformed scope_paths value surfaces as a synthetic
// blocking violation so policy authors can't accidentally or
// maliciously neutralise a rule by corrupting its scope.
func ruleScopeMatches(rule map[string]interface{}, inputs ExecutionInputs) (bool, error) {
	scopeRaw, ok := rule["scope_paths"].([]interface{})
	if !ok || len(scopeRaw) == 0 {
		return true, nil // global rule
	}
	patterns := make([]string, 0, len(scopeRaw))
	for _, p := range scopeRaw {
		if s, ok := p.(string); ok {
			patterns = append(patterns, s)
		}
	}
	// Check writes + reads; any single match is enough to put the
	// evaluation "inside" the scope.  Pattern errors abort the scope
	// match and propagate up so the rule evaluator can convert them
	// into a block-severity synthetic violation.
	for _, p := range inputs.WritePaths {
		_, matched, err := MatchAny(patterns, p)
		if err != nil {
			return false, fmt.Errorf("scope_paths pattern error on input %q: %w", p, err)
		}
		if matched {
			return true, nil
		}
	}
	for _, p := range inputs.ReadPaths {
		_, matched, err := MatchAny(patterns, p)
		if err != nil {
			return false, fmt.Errorf("scope_paths pattern error on input %q: %w", p, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func evaluateRule(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	kindStr, _ := rule["kind"].(string)
	kind := policy.Kind(kindStr)
	if !kind.Valid() {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile contains unsupported rule kind: " + kindStr}
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
	matched, err := ruleScopeMatches(rule, inputs)
	if err != nil {
		ruleID, _ := rule["id"].(string)
		kindStr, _ := rule["kind"].(string)
		return &Violation{
			RuleID:            ruleID,
			Kind:              policy.Kind(kindStr),
			Mode:              policy.ModeBlock,
			Message:           "scope pattern failed to compile: " + err.Error(),
			Explanation:       "The rule's scope_paths contains a glob pattern that the matcher could not compile. reconc fails closed here to prevent a malformed scope from silently disabling a rule.",
			RecommendedAction: "Fix the scope_paths pattern in the rule source, then run `reconc compile` again.",
		}, nil
	}
	if !matched {
		return nil, nil
	}

	switch kind {
	case policy.KindDenyWrite:
		return evalDenyWrite(rule, defaultMode, inputs)
	case policy.KindRequireRead:
		return evalRequireRead(rule, defaultMode, inputs)
	case policy.KindCoupleChange:
		return evalCoupleChange(rule, defaultMode, inputs)
	case policy.KindRequireClaim:
		return evalRequireClaim(rule, defaultMode, inputs)
	case policy.KindForbidCommand:
		return evalForbidCommand(rule, defaultMode, inputs)
	case policy.KindRequireCommand:
		return evalRequireCommand(rule, defaultMode, inputs, false)
	case policy.KindRequireCommandSuccess:
		return evalRequireCommand(rule, defaultMode, inputs, true)
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
	}
	return nil, nil
}

// evalRequireScript runs an external script for each match context.
// Each context that produces a "block" or "error" outcome contributes
// to the violation. A "pass" exit (0) clears that context.
func evalRequireScript(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	whenPatterns := stringListField(rule, "when_paths")
	contexts, err := collectMatchContexts(inputs.WritePaths, whenPatterns)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return nil, nil
	}

	scriptPath, _ := rule["script"].(string)
	if scriptPath == "" {
		return nil, &rerrors.LockfileError{Message: "rule '" + ruleIDOf(rule) + "' missing script field in lockfile"}
	}
	rawArgs, _ := rule["args"].([]interface{})
	args := make([]string, 0, len(rawArgs))
	for _, a := range rawArgs {
		if s, ok := a.(string); ok {
			args = append(args, s)
		}
	}
	timeoutSec := int(numAsIntDefault(rule["timeout_sec"], 0))
	killTimeoutSec := int(numAsIntDefault(rule["kill_timeout_sec"], 0))

	failures := []string{}
	triggeredPaths := []string{}
	for _, mc := range contexts {
		triggeredPaths = appendUnique(triggeredPaths, mc.path)
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
		if err != nil {
			// Hard error (script crashed, missing, timeout, etc.).
			detail := err.Error()
			if outcome.TimedOut {
				detail = fmt.Sprintf("timed out after %.1fs", outcome.Duration.Seconds())
			}
			failures = append(failures, fmt.Sprintf("[%s] script %s error: %s", mc.path, scriptPath, detail))
			continue
		}
		if outcome.Status == "block" {
			detail := strings.TrimSpace(outcome.Stdout)
			if detail == "" {
				detail = strings.TrimSpace(outcome.Stderr)
			}
			if detail == "" {
				detail = "no output"
			}
			failures = append(failures, fmt.Sprintf("[%s] script %s blocked: %s", mc.path, scriptPath, detail))
		}
	}
	if len(failures) == 0 {
		return nil, nil
	}
	v := buildViolation(rule, defaultMode, triggeredPaths, nil, nil, []string{scriptPath}, nil, nil)
	v.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_script rule '%s'. %s",
		joinForHumans(triggeredPaths), v.RuleID, strings.Join(failures, "; "),
	)
	v.RecommendedAction = "Inspect the script output above; resolve the reported failure and re-run."
	return v, nil
}

// evalRequireFreshFile fires when when_paths matches AND any required
// file is missing OR older than its max_age_hours.
//
// Template-aware: when when_paths contains {var} placeholders, each
// matched write path produces its own substitution context so a
// single rule can scale across many tasks/modules without enumerating
// every value.
func evalRequireFreshFile(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	whenPatterns := stringListField(rule, "when_paths")
	files := requiredFilesFromRule(rule)
	if len(files) == 0 {
		return nil, nil
	}

	// Collect all (write_path, captures) pairs that match the rule's
	// when_paths. For non-templated patterns captures is empty.
	contexts, err := collectMatchContexts(inputs.WritePaths, whenPatterns)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return nil, nil
	}

	missing := []string{}
	stale := []string{}
	allRequired := map[string]struct{}{}
	triggeredPaths := []string{}
	now := time.Now()

	for _, mc := range contexts {
		triggeredPaths = appendUnique(triggeredPaths, mc.path)
		for _, rf := range files {
			path, err := SubstituteTemplate(rf.Path, mc.captures)
			if err != nil {
				return nil, &rerrors.RuleValidationError{
					Message: "rule '" + ruleIDOf(rule) + "' required_files path: " + err.Error(),
				}
			}
			allRequired[path] = struct{}{}
			fullPath := filepath.Join(ctx.repoRoot, path)
			info, err := os.Stat(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					if rf.Optional {
						continue
					}
					missing = appendUnique(missing, path)
					continue
				}
				return nil, &rerrors.LockfileError{Message: "stat required file " + path, Cause: err}
			}
			if !info.Mode().IsRegular() {
				missing = appendUnique(missing, path)
				continue
			}
			if rf.MaxAgeHours > 0 {
				age := now.Sub(info.ModTime())
				limit := time.Duration(rf.MaxAgeHours) * time.Hour
				if age > limit {
					stale = appendUnique(stale, path)
				}
			}
		}
	}

	if len(missing) == 0 && len(stale) == 0 {
		return nil, nil
	}
	requiredPaths := mapKeysSorted(allRequired)
	v := buildViolation(rule, defaultMode, triggeredPaths, nil, nil, requiredPaths, nil, nil)
	parts := []string{}
	if len(missing) > 0 {
		parts = append(parts, "missing: "+joinForHumans(missing))
	}
	if len(stale) > 0 {
		parts = append(parts, "stale: "+joinForHumans(stale))
	}
	v.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_fresh_file rule '%s' (%s).",
		joinForHumans(triggeredPaths), v.RuleID, strings.Join(parts, "; "),
	)
	v.RecommendedAction = "Regenerate or refresh the listed files: " + joinForHumans(requiredPaths) + "."
	return v, nil
}

// evalRequireEvidence fires when when_paths matches AND any evidence
// check fails. Template-aware: each matched write path supplies its
// own captures for substitution into evidence file paths.
func evalRequireEvidence(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	whenPatterns := stringListField(rule, "when_paths")
	checks := evidenceChecksFromRule(rule)
	if len(checks) == 0 {
		return nil, nil
	}

	contexts, err := collectMatchContexts(inputs.WritePaths, whenPatterns)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return nil, nil
	}

	failures := []string{}
	requiredFiles := map[string]struct{}{}
	triggeredPaths := []string{}

	for _, mc := range contexts {
		triggeredPaths = appendUnique(triggeredPaths, mc.path)
		for _, c := range checks {
			file, err := SubstituteTemplate(c.File, mc.captures)
			if err != nil {
				return nil, &rerrors.RuleValidationError{
					Message: "rule '" + ruleIDOf(rule) + "' evidence file: " + err.Error(),
				}
			}
			requiredFiles[file] = struct{}{}
			fullPath := filepath.Join(ctx.repoRoot, file)
			info, err := os.Stat(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					if c.Optional {
						continue
					}
					if c.MustExist {
						failures = append(failures, file+": file does not exist")
					}
					if !c.MustExist && (len(c.MustContain) > 0 || c.MustNotContain != "" || c.MaxLineCount > 0) {
						failures = append(failures, file+": file does not exist (cannot check content)")
					}
					continue
				}
				return nil, &rerrors.LockfileError{Message: "stat evidence file " + file, Cause: err}
			}
			if !info.Mode().IsRegular() {
				failures = append(failures, file+": not a regular file")
				continue
			}
			needContent := len(c.MustContain) > 0 || c.MustNotContain != "" || c.MaxLineCount > 0
			var content string
			if needContent {
				data, err := os.ReadFile(fullPath)
				if err != nil {
					return nil, &rerrors.LockfileError{Message: "read evidence file " + file, Cause: err}
				}
				content = string(data)
			}
			for _, sub := range c.MustContain {
				if !strings.Contains(content, sub) {
					failures = append(failures, file+": missing required substring "+quote(sub))
				}
			}
			if c.MustNotContain != "" {
				if strings.Contains(content, c.MustNotContain) {
					failures = append(failures, file+": contains forbidden substring "+quote(c.MustNotContain))
				}
			}
			if c.MaxLineCount > 0 {
				lines := strings.Count(content, "\n")
				if !strings.HasSuffix(content, "\n") && len(content) > 0 {
					lines++
				}
				if lines > c.MaxLineCount {
					failures = append(failures, fmt.Sprintf("%s: %d lines > max %d", file, lines, c.MaxLineCount))
				}
			}
		}
	}

	if len(failures) == 0 {
		return nil, nil
	}
	required := mapKeysSorted(requiredFiles)
	v := buildViolation(rule, defaultMode, triggeredPaths, nil, nil, required, nil, nil)
	v.Explanation = fmt.Sprintf(
		"Write activity %s triggered require_evidence rule '%s'. Failures: %s.",
		joinForHumans(triggeredPaths), v.RuleID, strings.Join(failures, "; "),
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

// collectMatchContexts walks every write path against every when_paths
// pattern; for each hit it records (path, captures). Non-templated
// patterns produce empty captures. Multiple write paths matching one
// templated pattern produce one context per write path.
func collectMatchContexts(writes, patterns []string) ([]matchContext, error) {
	out := []matchContext{}
	for _, w := range writes {
		for _, pat := range patterns {
			caps, ok, err := MatchTemplate(pat, w)
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

// appendUnique appends s to slice if not already present. O(n) per
// call; fine for the small slices we operate on.
func appendUnique(slice []string, s string) []string {
	for _, x := range slice {
		if x == s {
			return slice
		}
	}
	return append(slice, s)
}

// mapKeysSorted returns the keys of a string-keyed set in sorted order.
// Used to produce stable required-paths lists in violation reports.
func mapKeysSorted(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// ruleIDOf is a typed accessor for rule["id"] used in error messages.
func ruleIDOf(rule map[string]interface{}) string {
	if id, ok := rule["id"].(string); ok {
		return id
	}
	return "<unknown>"
}

// requiredFilesFromRule extracts the required_files list from a rule
// map (lockfile or YAML payload).
func requiredFilesFromRule(rule map[string]interface{}) []policy.RequiredFile {
	raw, ok := rule["required_files"]
	if !ok || raw == nil {
		return nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]policy.RequiredFile, 0, len(list))
	for _, entry := range list {
		mapping, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		path, _ := mapping["path"].(string)
		if path == "" {
			continue
		}
		ageHours := numAsIntDefault(mapping["max_age_hours"], 0)
		optional, _ := mapping["optional"].(bool)
		out = append(out, policy.RequiredFile{
			Path:        path,
			MaxAgeHours: int(ageHours),
			Optional:    optional,
		})
	}
	return out
}

// evidenceChecksFromRule extracts the evidence list from a rule map.
func evidenceChecksFromRule(rule map[string]interface{}) []policy.EvidenceCheck {
	raw, ok := rule["evidence"]
	if !ok || raw == nil {
		return nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]policy.EvidenceCheck, 0, len(list))
	for _, entry := range list {
		mapping, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		file, _ := mapping["file"].(string)
		if file == "" {
			continue
		}
		mustExist, _ := mapping["must_exist"].(bool)
		var mustContain []string
		if rawList, ok := mapping["must_contain"].([]interface{}); ok {
			for _, v := range rawList {
				if s, ok := v.(string); ok {
					mustContain = append(mustContain, s)
				}
			}
		}
		mustNotContain, _ := mapping["must_not_contain"].(string)
		maxLines := numAsIntDefault(mapping["max_line_count"], 0)
		optional, _ := mapping["optional"].(bool)
		out = append(out, policy.EvidenceCheck{
			File:           file,
			MustExist:      mustExist,
			MustContain:    mustContain,
			MustNotContain: mustNotContain,
			MaxLineCount:   int(maxLines),
			Optional:       optional,
		})
	}
	return out
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

func evalDenyWrite(rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	patterns := stringListField(rule, "paths")
	matched, err := matchingPaths(inputs.WritePaths, patterns)
	if err != nil {
		return nil, err
	}
	if len(matched) == 0 {
		return nil, nil
	}
	return buildViolation(rule, defaultMode, matched, nil, nil, nil, nil, nil), nil
}

func evalRequireRead(rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	triggered, err := matchingPaths(inputs.WritePaths, stringListField(rule, "paths"))
	if err != nil {
		return nil, err
	}
	if len(triggered) == 0 {
		return nil, nil
	}
	required := stringListField(rule, "before_paths")
	matchedReads, err := matchingPaths(inputs.ReadPaths, required)
	if err != nil {
		return nil, err
	}
	if len(matchedReads) > 0 {
		return nil, nil
	}
	return buildViolation(rule, defaultMode, triggered, nil, nil, required, nil, nil), nil
}

func evalCoupleChange(rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	triggered, err := matchingPaths(inputs.WritePaths, stringListField(rule, "paths"))
	if err != nil {
		return nil, err
	}
	if len(triggered) == 0 {
		return nil, nil
	}
	required := stringListField(rule, "when_paths")
	coupled, err := matchingCoupledPaths(inputs.WritePaths, triggered, required)
	if err != nil {
		return nil, err
	}
	if len(coupled) > 0 {
		return nil, nil
	}
	return buildViolation(rule, defaultMode, triggered, nil, nil, required, nil, nil), nil
}

func evalRequireClaim(rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	triggered, err := matchingPaths(inputs.WritePaths, stringListField(rule, "when_paths"))
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

func evalForbidCommand(rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	required := stringListField(rule, "commands")
	forbidden := matchingCommands(inputs.Commands, required)
	if len(forbidden) == 0 {
		return nil, nil
	}
	whenPatterns := stringListField(rule, "when_paths")
	triggered := []string{}
	if len(whenPatterns) > 0 {
		var err error
		triggered, err = matchingPaths(inputs.WritePaths, whenPatterns)
		if err != nil {
			return nil, err
		}
		if len(triggered) == 0 {
			return nil, nil
		}
	}
	return buildViolation(rule, defaultMode, triggered, forbidden, nil, nil, nil, nil), nil
}

func evalRequireCommand(rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs, requireSuccess bool) (*Violation, error) {
	triggered, err := matchingPaths(inputs.WritePaths, stringListField(rule, "when_paths"))
	if err != nil {
		return nil, err
	}
	if len(triggered) == 0 {
		return nil, nil
	}
	required := stringListField(rule, "commands")
	var matched []string
	if requireSuccess {
		matched = matchingCommandResults(inputs.CommandResults, required, CommandOutcomeSuccess)
	} else {
		matched = matchingCommands(inputs.Commands, required)
	}
	if len(matched) > 0 {
		return nil, nil
	}
	return buildViolation(rule, defaultMode, triggered, nil, nil, nil, required, nil), nil
}

// --- Match helpers (operate on already-normalized inputs) ---

func matchingPaths(paths, patterns []string) ([]string, error) {
	if len(patterns) == 0 || len(paths) == 0 {
		return nil, nil
	}
	out := []string{}
	for _, p := range paths {
		_, ok, err := MatchAny(patterns, p)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func matchingCoupledPaths(writes, triggered, required []string) ([]string, error) {
	if len(required) == 0 {
		return nil, nil
	}
	triggeredSet := map[string]struct{}{}
	for _, t := range triggered {
		triggeredSet[t] = struct{}{}
	}
	out := []string{}
	for _, p := range writes {
		if _, dup := triggeredSet[p]; dup {
			continue
		}
		_, ok, err := MatchAny(required, p)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func matchingCommands(commands, expected []string) []string {
	if len(expected) == 0 {
		return nil
	}
	// Normalise both sides of the comparison so whitespace variation
	// can't defeat require_command /
	// forbid_command. The `commands` slice is already normalised by
	// normalizeCommands; `expected` comes straight from the lockfile
	// rule payload and may contain operator-authored double-spaces,
	// tabs, or newlines.
	expectedSet := map[string]struct{}{}
	for _, e := range expected {
		expectedSet[normalizeWhitespace(e)] = struct{}{}
	}
	out := []string{}
	for _, c := range commands {
		if _, ok := expectedSet[normalizeWhitespace(c)]; ok {
			out = append(out, c)
		}
	}
	return out
}

func matchingCommandResults(results []CommandResult, expected []string, outcome string) []string {
	if len(expected) == 0 {
		return nil
	}
	// Normalise both sides of the comparison.
	expectedSet := map[string]struct{}{}
	for _, e := range expected {
		expectedSet[normalizeWhitespace(e)] = struct{}{}
	}
	out := []string{}
	for _, r := range results {
		if r.Outcome != outcome {
			continue
		}
		if _, ok := expectedSet[normalizeWhitespace(r.Command)]; ok {
			out = append(out, r.Command)
		}
	}
	return out
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
	rule map[string]interface{},
	defaultMode policy.Mode,
	matchedPaths, matchedCommands, matchedClaims, requiredPaths, requiredCommands, requiredClaims []string,
) *Violation {
	id, _ := rule["id"].(string)
	kindStr, _ := rule["kind"].(string)
	message, _ := rule["message"].(string)
	srcPath, _ := rule["source_path"].(string)
	srcBlock, _ := rule["source_block_id"].(string)

	mode := defaultMode
	if mStr, ok := rule["mode"].(string); ok && mStr != "" {
		mode = policy.Mode(mStr)
	}

	explanation, recommended := explainViolation(
		id, policy.Kind(kindStr), rule,
		matchedPaths, matchedCommands,
		requiredPaths, requiredCommands, requiredClaims,
	)

	return &Violation{
		RuleID:            id,
		Kind:              policy.Kind(kindStr),
		Mode:              mode,
		Message:           message,
		Explanation:       explanation,
		RecommendedAction: recommended,
		MatchedPaths:      coalesce(matchedPaths),
		MatchedCommands:   coalesce(matchedCommands),
		MatchedClaims:     coalesce(matchedClaims),
		RequiredPaths:     coalesce(requiredPaths),
		RequiredCommands:  coalesce(requiredCommands),
		RequiredClaims:    coalesce(requiredClaims),
		SourcePath:        srcPath,
		SourceBlockID:     srcBlock,
	}
}

func coalesce(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func explainViolation(
	id string, kind policy.Kind, rule map[string]interface{},
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
		if len(requiredPaths) > 0 {
			return fmt.Sprintf("Write activity %s matched deny_write rule '%s'.", pathList, id),
				fmt.Sprintf("Avoid writing paths matching %s.", requiredPathList)
		}
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

func stringListField(rule map[string]interface{}, key string) []string {
	raw, ok := rule[key]
	if !ok || raw == nil {
		return nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
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
