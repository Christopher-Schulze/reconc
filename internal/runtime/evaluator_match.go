package runtime

import (
	"strings"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/shellcommand"
)

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
