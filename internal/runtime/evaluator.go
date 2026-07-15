package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/assurance"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
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
// This is the primitive behind `reconc assert` (W27), replacing
// repo-specific assertion subcommands with one generic path.
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

	payload, err := loadFreshLockfile(root)
	if err != nil {
		return nil, err
	}

	defaultMode, err := lockfileDefaultMode(payload)
	if err != nil {
		return nil, err
	}

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
	normalizedWriteEpochs, err := normalizeWriteEpochs(merged.WritePaths, merged.WriteEpochs, root)
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
		WriteEpochs:    normalizedWriteEpochs,
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
	return checkRepoPolicy(startPath, inputs, nil)
}

// CheckRepoPolicyForKinds evaluates only the requested top-level rule kinds
// while keeping lockfile loading, freshness checks, path normalization and
// unsupported-kind validation identical to CheckRepoPolicy.
func CheckRepoPolicyForKinds(startPath string, inputs ExecutionInputs, allowedKinds map[policy.Kind]struct{}) (*CheckReport, error) {
	return checkRepoPolicy(startPath, inputs, func(kind policy.Kind) bool {
		_, ok := allowedKinds[kind]
		return ok
	})
}

func checkRepoPolicy(startPath string, inputs ExecutionInputs, includeKind func(policy.Kind) bool) (*CheckReport, error) {
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

	payload, err := loadFreshLockfile(root)
	if err != nil {
		return nil, err
	}

	defaultMode, err := lockfileDefaultMode(payload)
	if err != nil {
		return nil, err
	}

	normalizedReads, err := normalizePaths(inputs.ReadPaths, root)
	if err != nil {
		return nil, err
	}
	normalizedWrites, err := normalizePaths(inputs.WritePaths, root)
	if err != nil {
		return nil, err
	}
	normalizedWriteEpochs, err := normalizeWriteEpochs(inputs.WritePaths, inputs.WriteEpochs, root)
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
		WriteEpochs:    normalizedWriteEpochs,
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
	rules := make([]map[string]interface{}, 0, len(rulesRaw))
	for _, ruleRaw := range rulesRaw {
		ruleMap, ok := ruleRaw.(map[string]interface{})
		if !ok {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile contains a non-object rule entry"}
		}
		kindStr, _ := ruleMap["kind"].(string)
		kind := policy.Kind(kindStr)
		if !kind.Valid() {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile contains unsupported rule kind: " + kindStr}
		}
		if includeKind != nil && !includeKind(kind) {
			continue
		}
		rules = append(rules, ruleMap)
	}

	batchedScripts, err := evaluateBatchedRequireScripts(ctx, rules, defaultMode, normalizedInputs, includeKind)
	if err != nil {
		return nil, err
	}
	for i, ruleMap := range rules {
		if batchedScripts.handled[i] {
			if v := batchedScripts.violations[i]; v != nil {
				report.Violations = append(report.Violations, *v)
			}
			continue
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
	rule     map[string]interface{}
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

func evaluateBatchedRequireScripts(ctx *evalContext, rules []map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs, includeKind func(policy.Kind) bool) (batchedScriptEvaluations, error) {
	results := batchedScriptEvaluations{
		handled:    map[int]bool{},
		violations: map[int]*Violation{},
	}
	if includeKind != nil && !includeKind(policy.KindRequireScript) {
		return results, nil
	}

	groups := map[workflowAuditBatchKey][]workflowAuditBatchItem{}
	groupOrder := []workflowAuditBatchKey{}
	for i, rule := range rules {
		scriptPath, mode, timeoutSec, killTimeoutSec, ok := workflowAuditBatchCandidate(rule)
		if !ok {
			continue
		}
		contexts, err := collectMatchContexts(inputs.WritePaths, stringListField(rule, "when_paths"))
		if err != nil {
			return results, err
		}
		if len(contexts) == 0 {
			continue
		}

		key := workflowAuditBatchKey{scriptPath: scriptPath, timeoutSec: timeoutSec, killTimeoutSec: killTimeoutSec}
		if _, ok := groups[key]; !ok {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], workflowAuditBatchItem{
			index:    i,
			rule:     rule,
			mode:     mode,
			contexts: contexts,
		})
	}

	for _, key := range groupOrder {
		items := groups[key]
		if len(items) < 2 {
			continue
		}
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
		if err != nil || (outcome.Status != "pass" && outcome.Status != "block") {
			continue
		}
		failuresByMode, ok := parseWorkflowAuditBatchOutput(outcome.Stdout, modes)
		if !ok {
			continue
		}

		for _, item := range items {
			results.handled[item.index] = true
			failures := failuresByMode[item.mode]
			if len(failures) == 0 {
				continue
			}
			triggeredPaths := triggeredPathsForContexts(item.contexts)
			v := buildViolation(item.rule, defaultMode, triggeredPaths, nil, nil, []string{key.scriptPath}, nil, nil)
			details := batchScriptFailureDetails(key.scriptPath, failures)
			v.Explanation = fmt.Sprintf(
				"Write activity %s triggered require_script rule '%s'. %s",
				joinForHumans(triggeredPaths), v.RuleID, strings.Join(details, "; "),
			)
			v.RecommendedAction = batchScriptRecommendedAction(details)
			results.violations[item.index] = v
		}
	}

	return results, nil
}

func workflowAuditBatchCandidate(rule map[string]interface{}) (scriptPath, mode string, timeoutSec, killTimeoutSec int, ok bool) {
	kindStr, _ := rule["kind"].(string)
	if policy.Kind(kindStr) != policy.KindRequireScript {
		return "", "", 0, 0, false
	}
	scriptPath, _ = rule["script"].(string)
	slashScriptPath := filepath.ToSlash(scriptPath)
	if scriptPath == "" || HasTemplateVars(scriptPath) || !strings.HasPrefix(slashScriptPath, "tools/reconc/harness/") || !strings.HasSuffix(slashScriptPath, "/audits/run-workflow-audit") {
		return "", "", 0, 0, false
	}
	rawArgs, _ := rule["args"].([]interface{})
	if len(rawArgs) != 1 {
		return "", "", 0, 0, false
	}
	mode, ok = rawArgs[0].(string)
	if !ok || mode == "" || HasTemplateVars(mode) {
		return "", "", 0, 0, false
	}
	timeoutSec = int(numAsIntDefault(rule["timeout_sec"], 0))
	killTimeoutSec = int(numAsIntDefault(rule["kill_timeout_sec"], 0))
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
	var output workflowAuditBatchOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &output); err != nil {
		return nil, false
	}
	failuresByMode := map[string][]string{}
	for _, result := range output.Results {
		failuresByMode[result.Mode] = result.Failures
	}
	for _, mode := range expectedModes {
		if _, ok := failuresByMode[mode]; !ok {
			return nil, false
		}
	}
	return failuresByMode, true
}

func triggeredPathsForContexts(contexts []matchContext) []string {
	paths := []string{}
	for _, mc := range contexts {
		paths = appendUnique(paths, mc.path)
	}
	return paths
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
// loader so macOS /var <-> /private/var symlink drift and case-variant
// mount aliases don't reject a legitimate lockfile.
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
		if ca == cb {
			return true
		}
		aInfo, aStatErr := os.Stat(ca)
		bInfo, bStatErr := os.Stat(cb)
		return aStatErr == nil && bStatErr == nil && os.SameFile(aInfo, bInfo)
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
		if path.IsAbs(candidate) || filepath.IsAbs(candidate) {
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

func normalizeWriteEpochs(paths []string, epochs map[string]uint64, root string) (map[string]uint64, error) {
	out := make(map[string]uint64, len(epochs))
	for _, raw := range paths {
		normalized, err := normalizePaths([]string{raw}, root)
		if err != nil {
			return nil, err
		}
		if len(normalized) == 0 {
			continue
		}
		epoch := epochs[raw]
		if epoch == 0 {
			epoch = epochs[strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")]
		}
		if epoch > out[normalized[0]] {
			out[normalized[0]] = epoch
		}
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
//     start of the command and after every shell compound boundary
//     (" && ", " || ", " ; ", " | ", " & "). The trailing space in the
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
	cmd = normalizeWhitespace(cmd)
	if cmd == "" {
		return ""
	}
	repoRoot = strings.TrimRight(strings.TrimSpace(repoRoot), "/")
	segments := splitCommandSegments(cmd)
	for i := range segments {
		segments[i].body = normalizeSegmentBody(segments[i].body, repoRoot)
	}
	var out strings.Builder
	for i, s := range segments {
		if i > 0 {
			out.WriteString(s.sep)
		}
		out.WriteString(s.body)
	}
	return normalizeWhitespace(out.String())
}

// commandSegment is one slice of a normalized command between shell
// compound boundaries. The first segment has sep == "".
type commandSegment struct {
	sep  string
	body string
}

// commandSegmentSeparators lists the shell compound boundaries that
// start a new command position. Order matters: longer separators
// must precede shorter overlapping ones so " && " is preferred over
// " & " when both could match. After normalizeWhitespace these
// boundaries always appear in their single-space canonical form.
var commandSegmentSeparators = []string{" && ", " || ", " ; ", " | ", " & "}

// splitCommandSegments splits a whitespace-normalized command into
// segments at every shell compound boundary while preserving the
// separators so the command can be reconstructed verbatim.
func splitCommandSegments(cmd string) []commandSegment {
	segments := []commandSegment{{sep: "", body: cmd}}
	for {
		progress := false
		next := make([]commandSegment, 0, len(segments)+1)
		for _, s := range segments {
			bestIdx := -1
			bestSep := ""
			for _, sep := range commandSegmentSeparators {
				if i := strings.Index(s.body, sep); i >= 0 {
					if bestIdx < 0 || i < bestIdx {
						bestIdx = i
						bestSep = sep
					}
				}
			}
			if bestIdx < 0 {
				next = append(next, s)
				continue
			}
			next = append(next,
				commandSegment{sep: s.sep, body: s.body[:bestIdx]},
				commandSegment{sep: bestSep, body: s.body[bestIdx+len(bestSep):]},
			)
			progress = true
		}
		segments = next
		if !progress {
			return segments
		}
	}
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
	if strings.HasPrefix(body, "rtk ") {
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
			RecommendedAction: "Fix the scope_paths pattern in the rule source, then run `reconc refresh .`.",
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

func evalRequireAssurance(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	triggered, err := matchingPaths(inputs.WritePaths, stringListField(rule, "when_paths"))
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
	successful := []string{}
	reportedSuccessful := []string{}
	for _, result := range inputs.CommandResults {
		if result.Outcome == CommandOutcomeSuccess {
			reportedSuccessful = appendUnique(reportedSuccessful, result.Command)
			successful = appendUnique(successful, result.Command)
			successful = appendUnique(successful, normalizeCommandSemantics(result.Command, ctx.repoRoot))
		}
	}
	for gateIndex := range gates {
		for commandIndex := range gates[gateIndex].Commands {
			gates[gateIndex].Commands[commandIndex] = normalizeCommandSemantics(gates[gateIndex].Commands[commandIndex], ctx.repoRoot)
		}
	}
	findings, err := assurance.Evaluate(ctx.repoRoot, gates, assurance.Inputs{
		ChangedPaths:       inputs.WritePaths,
		SuccessfulCommands: successful,
		Now:                time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	if len(findings) == 0 {
		return nil, nil
	}
	requiredPaths := []string{}
	details := make([]string, 0, len(findings))
	for _, finding := range findings {
		details = append(details, "["+finding.GateID+"] "+finding.Message)
		for _, path := range finding.Paths {
			requiredPaths = appendUnique(requiredPaths, path)
		}
	}
	v := buildViolation(rule, defaultMode, triggered, reportedSuccessful, nil, requiredPaths, nil, nil)
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

func assuranceGatesFromRule(rule map[string]interface{}) ([]policy.AssuranceGate, error) {
	raw, ok := rule["assurance"]
	if !ok || raw == nil {
		return nil, &rerrors.LockfileError{Message: "rule '" + ruleIDOf(rule) + "' missing assurance field in lockfile"}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "marshal assurance gates for rule '" + ruleIDOf(rule) + "'", Cause: err}
	}
	var gates []policy.AssuranceGate
	if err := json.Unmarshal(data, &gates); err != nil {
		return nil, &rerrors.LockfileError{Message: "decode assurance gates for rule '" + ruleIDOf(rule) + "'", Cause: err}
	}
	if len(gates) == 0 {
		return nil, &rerrors.LockfileError{Message: "rule '" + ruleIDOf(rule) + "' has empty assurance field in lockfile"}
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
	coupled, err := matchingPaths(inputs.WritePaths, required)
	if err != nil {
		return nil, err
	}
	if len(coupled) > 0 {
		coupledSet := make(map[string]struct{}, len(coupled))
		for _, path := range coupled {
			coupledSet[path] = struct{}{}
		}
		primary := triggered[:0]
		for _, path := range triggered {
			if _, isCompanion := coupledSet[path]; !isCompanion {
				primary = append(primary, path)
			}
		}
		triggered = primary
	}
	if len(triggered) == 0 {
		return nil, nil
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

func evalForbidCommand(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs) (*Violation, error) {
	required := stringListField(rule, "commands")
	forbidden := matchingCommands(inputs.Commands, required, ctxRepoRoot(ctx))
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

func evalRequireCommand(ctx *evalContext, rule map[string]interface{}, defaultMode policy.Mode, inputs ExecutionInputs, requireSuccess bool) (*Violation, error) {
	triggered, err := matchingPaths(inputs.WritePaths, stringListField(rule, "when_paths"))
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
		matched = matchingCommandResultsSince(inputs.CommandResults, required, CommandOutcomeSuccess, repoRoot, minimumEpoch)
	} else {
		matched = matchingCommands(inputs.Commands, required, repoRoot)
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

func matchingCommands(commands, expected []string, repoRoot string) []string {
	if len(expected) == 0 {
		return nil
	}
	// Normalise both sides of the comparison so whitespace variation
	// can't defeat require_command / forbid_command, and so the literal
	// rule form matches commands that were transparently wrapped by a
	// CLI proxy (e.g. `rtk go test ...`) or anchored to an absolute
	// repo path by the agent runtime (e.g.
	// `cd /Users/.../repo/sub && ...`). normalizeCommandSemantics
	// applies both transformations to expected and recorded sides.
	expectedSet := map[string]struct{}{}
	for _, e := range expected {
		expectedSet[normalizeCommandSemantics(e, repoRoot)] = struct{}{}
	}
	out := []string{}
	for _, c := range commands {
		if _, ok := expectedSet[normalizeCommandSemantics(c, repoRoot)]; ok {
			out = append(out, c)
		}
	}
	return out
}

func matchingCommandResults(results []CommandResult, expected []string, outcome string, repoRoot string) []string {
	return matchingCommandResultsSince(results, expected, outcome, repoRoot, 0)
}

func matchingCommandResultsSince(results []CommandResult, expected []string, outcome string, repoRoot string, minimumEpoch uint64) []string {
	if len(expected) == 0 {
		return nil
	}
	// Normalise both sides of the comparison (whitespace + RTK prefix +
	// absolute repoRoot in cd). See matchingCommands for the rationale.
	expectedSet := map[string]struct{}{}
	for _, e := range expected {
		expectedSet[normalizeCommandSemantics(e, repoRoot)] = struct{}{}
	}
	out := []string{}
	for _, r := range results {
		if r.Outcome != outcome || r.EvidenceEpoch < minimumEpoch {
			continue
		}
		norm := normalizeCommandSemantics(r.Command, repoRoot)
		if _, ok := expectedSet[norm]; ok {
			out = append(out, r.Command)
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
		if stripped := stripTrailingRedirects(norm); stripped != norm {
			if _, ok := expectedSet[stripped]; ok {
				out = append(out, r.Command)
			}
		}
	}
	return out
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
	fields := strings.Fields(cmd)
	for len(fields) > 1 {
		last := fields[len(fields)-1]
		switch {
		case isRedirectStart(last):
			// Self-contained redirect token: ">file", "2>&1", ">>", "<in".
			fields = fields[:len(fields)-1]
		case isRedirectOperatorOnly(fields[len(fields)-2]) && isPlainRedirectTarget(last):
			// Spaced redirect: "> file", "2> err", "< in".
			fields = fields[:len(fields)-2]
		default:
			return strings.Join(fields, " ")
		}
	}
	return strings.Join(fields, " ")
}

// isRedirectStart reports whether tok begins a shell redirection: an optional
// fd number, an optional leading '&', then '>' or '<' (so ">file", "2>&1",
// ">>", "&>log", "<in" qualify, but "a>b", "file", "123" do not).
func isRedirectStart(tok string) bool {
	i := 0
	for i < len(tok) && tok[i] >= '0' && tok[i] <= '9' {
		i++
	}
	if i < len(tok) && tok[i] == '&' {
		i++
	}
	return i < len(tok) && (tok[i] == '>' || tok[i] == '<')
}

// isRedirectOperatorOnly reports whether tok is a bare redirect operator with
// no fused target (">", ">>", "2>", "&>", "<", "2>&1"): only digits, '&', '<',
// '>' characters and at least one '<' or '>'.
func isRedirectOperatorOnly(tok string) bool {
	hasRedir := false
	for _, c := range tok {
		switch {
		case c >= '0' && c <= '9', c == '&':
		case c == '<' || c == '>':
			hasRedir = true
		default:
			return false
		}
	}
	return hasRedir
}

// isPlainRedirectTarget reports whether tok is a plausible redirect target (a
// filename), i.e. it carries no shell metacharacters that would make it part of
// a pipeline or another command.
func isPlainRedirectTarget(tok string) bool {
	if tok == "" {
		return false
	}
	return !strings.ContainsAny(tok, "|&;<>")
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
