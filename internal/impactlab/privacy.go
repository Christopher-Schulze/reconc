package impactlab

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/runtime"
)

var secretKey = regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?key|secret|token|password|passwd|authorization|cookie|credential|private[_-]?key)`)
var secretPrefix = regexp.MustCompile(`(?i)(?:sk-[a-z0-9_-]{8,}|gh[pousr]_[a-z0-9]{8,}|glpat-[a-z0-9_-]{8,}|xox[baprs]-[a-z0-9_-]{8,}|npm_[a-z0-9_-]{8,}|pypi-[a-z0-9_-]{8,}|(?:AKIA|ASIA)[A-Z0-9]{12,}|eyJ[a-z0-9_-]{8,}\.[a-z0-9_-]{8,}\.[a-z0-9_-]{8,})`)
var secretURL = regexp.MustCompile(`(?i)(://)[^/@[:space:]]+:[^/@[:space:]]+@`)
var secretQuery = regexp.MustCompile(`(?i)([?&](?:[^=&#]*(?:api[_-]?key|access[_-]?key|secret|token|password|passwd|authorization|cookie|credential|private[_-]?key)[^=&#]*)=)[^&#[:space:]]+`)

func sanitizeCase(repoRoot string, scanner *actioninspect.TextScanner, replayCase Case) (Case, error) {
	if !validCaseID(replayCase.ID) {
		return Case{}, fmt.Errorf("impact case id must be 1..%d bytes using letters, digits, dot, dash, or underscore", maxCaseIDBytes)
	}
	switch replayCase.Kind {
	case CaseRepository:
		if replayCase.Repository == nil || replayCase.Action != nil {
			return Case{}, fmt.Errorf("repository case must contain only repository evidence")
		}
		return sanitizeRepositoryCase(repoRoot, replayCase.ID, *replayCase.Repository)
	case CaseActionPre, CaseActionPost:
		if replayCase.Action == nil || replayCase.Repository != nil {
			return Case{}, fmt.Errorf("action case must contain only action evidence")
		}
		return sanitizeActionCase(scanner, replayCase.ID, replayCase.Kind, *replayCase.Action)
	default:
		return Case{}, fmt.Errorf("impact case %q has unsupported kind %q", replayCase.ID, replayCase.Kind)
	}
}

func sanitizeRepositoryCase(repoRoot, id string, replayCase RepositoryCase) (Case, error) {
	normalized, err := runtime.NormalizeReplayInputs(repoRoot, replayCase.Inputs)
	if err != nil {
		return Case{}, err
	}
	cleaned := RepositoryCase{Inputs: normalized, RedactedEventClasses: []EventClass{}}
	cleaned.Inputs.ReadPaths = uniqueStrings(cleaned.Inputs.ReadPaths)
	cleaned.Inputs.WritePaths = uniqueStrings(cleaned.Inputs.WritePaths)
	cleaned.Inputs.Commands, cleaned.RedactionCount = sanitizeValues(cleaned.Inputs.Commands)
	if cleaned.RedactionCount > 0 {
		cleaned.RedactedEventClasses = append(cleaned.RedactedEventClasses, EventClassCommand)
	}
	resultRedactions := sanitizeCommandResults(cleaned.Inputs.CommandResults)
	cleaned.RedactionCount += resultRedactions
	if resultRedactions > 0 {
		cleaned.RedactedEventClasses = append(cleaned.RedactedEventClasses, EventClassCommandOutcome)
	}
	cleaned.Inputs.Claims, resultRedactions = sanitizeValues(cleaned.Inputs.Claims)
	cleaned.RedactionCount += resultRedactions
	if resultRedactions > 0 {
		cleaned.RedactedEventClasses = append(cleaned.RedactedEventClasses, EventClassClaim)
	}
	cleaned.RedactedEventClasses = canonicalEventClasses(cleaned.RedactedEventClasses)
	if _, err := validateRepositoryCase(cleaned); err != nil {
		return Case{}, err
	}
	return Case{ID: id, Kind: CaseRepository, Repository: &cleaned}, nil
}

func sanitizeValues(values []string) ([]string, int) {
	out := make([]string, 0, len(values))
	redactions := 0
	for _, value := range values {
		cleaned, count := sanitizeSensitiveText(value)
		out = append(out, cleaned)
		redactions += count
	}
	return uniqueStrings(out), redactions
}

func sanitizeCommandResults(results []runtime.CommandResult) int {
	redactions := 0
	for index := range results {
		cleaned, count := sanitizeSensitiveText(results[index].Command)
		results[index].Command = cleaned
		redactions += count
	}
	return redactions
}

func sanitizeSensitiveText(value string) (string, int) {
	tokens, tokenLimitReached := splitShellTextWords(value)
	redactions := 0
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		trimmed := strings.Trim(token, `"'()`)
		switch {
		case sensitiveAssignment(trimmed) && !strings.HasSuffix(trimmed, "=<redacted>") &&
			!strings.HasSuffix(trimmed, ":<redacted>"):
			separator := strings.IndexAny(trimmed, "=:")
			if separator >= 0 {
				tokens[index] = trimmed[:separator+1] + "<redacted>"
			} else {
				tokens[index] = "<redacted>"
			}
			redactions++
		case sensitiveFlag(trimmed) && index+1 < len(tokens):
			next := index + 1
			if strings.EqualFold(strings.Trim(tokens[next], `"'()`), "bearer") && next+1 < len(tokens) {
				next++
			}
			if tokens[next] != "<redacted>" {
				tokens[next] = "<redacted>"
				redactions++
			}
			index = next
		default:
			if quotedBearerValue(trimmed) {
				tokens[index] = "<redacted>"
				redactions++
				continue
			}
			replaced := secretPrefix.ReplaceAllString(token, "<redacted>")
			replaced = secretURL.ReplaceAllString(replaced, "$1<redacted>@")
			replaced = secretQuery.ReplaceAllString(replaced, "$1<redacted>")
			if strings.EqualFold(trimmed, "bearer") && index+1 < len(tokens) && tokens[index+1] != "<redacted>" {
				tokens[index+1] = "<redacted>"
				redactions++
				index++
			}
			if replaced != token {
				redactions++
				tokens[index] = replaced
			}
		}
	}
	joined := strings.Join(tokens, " ")
	if tokenLimitReached || len(joined) > maxValueBytes {
		redactions++
	}
	return boundText(joined), redactions
}

func splitShellTextWords(value string) ([]string, bool) {
	const maxWords = maxValueBytes + 1
	words := make([]string, 0, min(16, maxWords))
	for offset := 0; offset < len(value); {
		for offset < len(value) {
			character, size := utf8.DecodeRuneInString(value[offset:])
			if !unicode.IsSpace(character) {
				break
			}
			offset += size
		}
		if offset == len(value) {
			return words, false
		}
		if len(words) == maxWords {
			return words, true
		}
		start := offset
		var quote rune
		escaped := false
		for offset < len(value) {
			character, size := utf8.DecodeRuneInString(value[offset:])
			if escaped {
				escaped = false
				offset += size
				continue
			}
			if character == '\\' && quote != '\'' {
				escaped = true
				offset += size
				continue
			}
			if quote == 0 {
				if unicode.IsSpace(character) {
					break
				}
				if character == '\'' || character == '"' {
					quote = character
				}
			} else if character == quote {
				quote = 0
			}
			offset += size
		}
		words = append(words, value[start:offset])
	}
	return words, false
}

func quotedBearerValue(value string) bool {
	if len(value) <= len("bearer") || !strings.EqualFold(value[:len("bearer")], "bearer") {
		return false
	}
	character, _ := utf8.DecodeRuneInString(value[len("bearer"):])
	return unicode.IsSpace(character)
}

func sensitiveAssignment(value string) bool {
	separator := strings.IndexAny(value, "=:")
	return separator > 0 && separator < len(value)-1 && secretKey.MatchString(strings.TrimLeft(value[:separator], "-"))
}

func sensitiveFlag(value string) bool {
	key := strings.TrimLeft(value, "-")
	if strings.Contains(key, "=") || strings.Count(key, ":") > 1 ||
		(strings.Contains(key, ":") && !strings.HasSuffix(key, ":")) {
		return false
	}
	key = strings.TrimSuffix(key, ":")
	return key != "" && secretKey.MatchString(key)
}

func sanitizeActions(repoRoot string, actions []string) ([]string, int) {
	out := make([]string, 0, len(actions))
	redactions := 0
	for _, action := range actions {
		action = strings.ReplaceAll(action, repoRoot, ".")
		cleaned, count := sanitizeSensitiveText(action)
		tokens := strings.Fields(cleaned)
		for index, token := range tokens {
			candidate := strings.Trim(token, `"'()[]{}<>,;:`)
			windowsPath := len(candidate) >= 3 && candidate[1] == ':' &&
				(candidate[2] == '\\' || candidate[2] == '/')
			if strings.HasPrefix(candidate, "/") || windowsPath {
				tokens[index] = strings.Replace(token, candidate, "<path>", 1)
				count++
			}
		}
		out = append(out, strings.Join(tokens, " "))
		redactions += count
	}
	return out, redactions
}

func validatePrivateInputs(inputs runtime.ExecutionInputs) error {
	for _, values := range [][]string{inputs.ReadPaths, inputs.WritePaths} {
		for _, value := range values {
			if !validReplayPath(value) {
				return fmt.Errorf("replay path %q must be canonical and repository-relative", value)
			}
		}
	}
	for fieldIndex, values := range [][]string{inputs.Commands, inputs.Claims} {
		for valueIndex, value := range values {
			if !validReplayText(value) {
				field := "command"
				if fieldIndex == 1 {
					field = "claim"
				}
				return fmt.Errorf("replay %s[%d] is empty, oversized, invalid, or contains a secret", field, valueIndex)
			}
		}
	}
	for index, result := range inputs.CommandResults {
		if !validReplayText(result.Command) ||
			(result.Outcome != runtime.CommandOutcomeSuccess && result.Outcome != runtime.CommandOutcomeFailure) {
			return fmt.Errorf("replay command result[%d] is invalid or contains a secret", index)
		}
	}
	return nil
}

func validReplayPath(value string) bool {
	if value == "" || len(value) > maxValueBytes || !utf8.ValidString(value) ||
		strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || hasUnsafeControl(value) {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != "." && cleaned != ".." &&
		!strings.HasPrefix(cleaned, "../") && !strings.Contains(strings.Split(cleaned, "/")[0], ":")
}

func validReplayText(value string) bool {
	if value == "" || len(value) > maxValueBytes || !utf8.ValidString(value) || hasUnsafeControl(value) {
		return false
	}
	_, redactions := sanitizeSensitiveText(value)
	return redactions == 0
}

func boundText(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) && character != '\t' {
			return ' '
		}
		return character
	}, strings.ToValidUTF8(value, "�"))
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= maxValueBytes {
		return value
	}
	value = value[:maxValueBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func hasUnsafeControl(value string) bool {
	return strings.IndexFunc(value, func(character rune) bool {
		return unicode.IsControl(character) && character != '\t'
	}) >= 0
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func canonicalEventClasses(values []EventClass) []EventClass {
	set := map[EventClass]struct{}{}
	for _, value := range values {
		if eventClassValid(value) {
			set[value] = struct{}{}
		}
	}
	out := []EventClass{}
	for _, candidate := range AllEventClasses() {
		if _, ok := set[candidate]; ok {
			out = append(out, candidate)
		}
	}
	return out
}

func eventClassValid(value EventClass) bool {
	for _, candidate := range AllEventClasses() {
		if value == candidate {
			return true
		}
	}
	return false
}

func eventClassIntersection(left, right []EventClass) []EventClass {
	rightSet := map[EventClass]struct{}{}
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	out := []EventClass{}
	for _, value := range canonicalEventClasses(left) {
		if _, ok := rightSet[value]; ok {
			out = append(out, value)
		}
	}
	return out
}

func equalEventClasses(left, right []EventClass) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func buildCompleteness(cases []Case, complete []EventClass, required ActionDimensions) Completeness {
	observedSet, redactedSet := map[EventClass]struct{}{}, map[EventClass]struct{}{}
	redactions, actionRedactions := 0, 0
	hasActions := false
	for _, replayCase := range cases {
		if replayCase.Repository != nil {
			addObservedClasses(observedSet, replayCase.Repository.Inputs)
			for _, class := range replayCase.Repository.RedactedEventClasses {
				redactedSet[class] = struct{}{}
			}
			redactions += replayCase.Repository.RedactionCount
		}
		if replayCase.Action != nil {
			hasActions = true
			redactions += replayCase.Action.RedactionCount
			actionRedactions += replayCase.Action.RedactionCount
		}
	}
	complete = canonicalEventClasses(complete)
	for class := range redactedSet {
		complete = removeEventClass(complete, class)
	}
	result := completenessFromSets(observedSet, redactedSet, complete, redactions)
	result.Action = buildActionCoverage(cases, required, actionRedactions)
	if (hasActions || !actionDimensionsEmpty(required)) && !result.Action.Complete {
		result.CompleteReplay = false
	}
	return result
}

func addObservedClasses(set map[EventClass]struct{}, inputs runtime.ExecutionInputs) {
	if len(inputs.ReadPaths) > 0 {
		set[EventClassRead] = struct{}{}
	}
	if len(inputs.WritePaths) > 0 {
		set[EventClassWrite] = struct{}{}
	}
	if len(inputs.Commands) > 0 {
		set[EventClassCommand] = struct{}{}
	}
	if len(inputs.CommandResults) > 0 {
		set[EventClassCommandOutcome] = struct{}{}
	}
	if len(inputs.Claims) > 0 {
		set[EventClassClaim] = struct{}{}
	}
}

func completenessFromSets(observed, redacted map[EventClass]struct{}, complete []EventClass, redactions int) Completeness {
	result := Completeness{
		ObservedEventClasses: eventClassesFromSet(observed),
		CompleteEventClasses: canonicalEventClasses(complete),
		RedactedEventClasses: eventClassesFromSet(redacted),
		RedactionCount:       redactions,
		Action: ActionCoverage{
			Observed: emptyActionDimensions(), Required: emptyActionDimensions(),
			Missing: emptyActionDimensions(),
		},
	}
	result.MissingEventClasses = eventClassDifference(AllEventClasses(), result.CompleteEventClasses)
	result.CompleteReplay = len(result.MissingEventClasses) == 0 && redactions == 0
	return result
}

func eventClassesFromSet(set map[EventClass]struct{}) []EventClass {
	values := make([]EventClass, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Slice(values, func(left, right int) bool {
		return eventClassIndex(values[left]) < eventClassIndex(values[right])
	})
	return values
}

func eventClassDifference(left, right []EventClass) []EventClass {
	rightSet := map[EventClass]struct{}{}
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	out := []EventClass{}
	for _, value := range left {
		if _, ok := rightSet[value]; !ok {
			out = append(out, value)
		}
	}
	return out
}

func removeEventClass(values []EventClass, removed EventClass) []EventClass {
	out := values[:0]
	for _, value := range values {
		if value != removed {
			out = append(out, value)
		}
	}
	return out
}

func eventClassIndex(value EventClass) int {
	for index, candidate := range AllEventClasses() {
		if value == candidate {
			return index
		}
	}
	return len(AllEventClasses())
}
