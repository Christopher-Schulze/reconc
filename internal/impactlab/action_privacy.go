package impactlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actioninspect"
)

const redactedActionValue = "<redacted>"

var oversizedPrivateNeedles = [][]byte{
	[]byte("api_key"), []byte("api-key"), []byte("apikey"),
	[]byte("access_key"), []byte("access-key"), []byte("accesskey"),
	[]byte("secret"), []byte("token"), []byte("password"), []byte("passwd"),
	[]byte("authorization"), []byte("cookie"), []byte("credential"),
	[]byte("private_key"), []byte("private-key"), []byte("privatekey"),
	[]byte("bearer "), []byte("sk-"), []byte("ghp_"), []byte("gho_"),
	[]byte("ghu_"), []byte("ghs_"), []byte("ghr_"), []byte("glpat-"),
	[]byte("xoxb-"), []byte("xoxa-"), []byte("xoxp-"), []byte("xoxr-"),
	[]byte("xoxs-"), []byte("npm_"), []byte("pypi-"), []byte("akia"),
	[]byte("asia"), []byte("eyj"),
}

func sanitizeActionCase(scanner *actioninspect.TextScanner, id string, kind CaseKind, input ActionCase) (Case, error) {
	if len(input.SelectedValues) != 0 || input.RedactionCount != 0 {
		return Case{}, fmt.Errorf("new action fixtures must supply raw synthetic values; selected-value summaries are exporter-owned")
	}
	if scanner == nil {
		return Case{}, fmt.Errorf("action privacy scanner is unavailable")
	}
	cleaned := cloneActionCase(input)
	cleaned.SelectedValues = []ActionValueSummary{}
	var err error
	cleaned.Request.Arguments, cleaned.SelectedValues, err = sanitizeActionRawValue(
		scanner, cleaned.Request.Arguments, action.SourceArguments, action.ProvenanceAgentSupplied, "", cleaned.SelectedValues,
	)
	if err != nil {
		return Case{}, fmt.Errorf("sanitize action arguments: %w", err)
	}
	cleaned.Request.Result, cleaned.SelectedValues, err = sanitizeActionRawValue(
		scanner, cleaned.Request.Result, action.SourceResult, action.ProvenanceHostObserved, "", cleaned.SelectedValues,
	)
	if err != nil {
		return Case{}, fmt.Errorf("sanitize action result: %w", err)
	}
	cleaned.Request.Progress, cleaned.SelectedValues, err = sanitizeActionRawValue(
		scanner, cleaned.Request.Progress, action.SourceProgress, action.ProvenanceHostObserved, "", cleaned.SelectedValues,
	)
	if err != nil {
		return Case{}, fmt.Errorf("sanitize action progress: %w", err)
	}
	for index := range cleaned.Request.Context {
		entry := &cleaned.Request.Context[index]
		if !entry.Available {
			continue
		}
		base := "/" + escapePointerToken(entry.Name)
		var contextPayload ActionPayload
		contextPayload, cleaned.SelectedValues, err = sanitizeActionRawValue(
			scanner, ActionPayload(entry.Value), action.SourceContext, entry.Provenance, base, cleaned.SelectedValues,
		)
		if err != nil {
			return Case{}, fmt.Errorf("sanitize action context %q: %w", entry.Name, err)
		}
		entry.Value = json.RawMessage(contextPayload)
	}
	sort.Slice(cleaned.Request.Context, func(i, j int) bool { return cleaned.Request.Context[i].Name < cleaned.Request.Context[j].Name })
	sort.Strings(cleaned.State.CredentialLabels)
	sort.Slice(cleaned.State.ResampleDrift, func(i, j int) bool { return cleaned.State.ResampleDrift[i] < cleaned.State.ResampleDrift[j] })
	if cleaned.State.RepositoryEffect != nil {
		sort.Strings(cleaned.State.RepositoryEffect.RuleIDs)
	}
	sort.Slice(cleaned.SelectedValues, func(i, j int) bool {
		return actionValueSummaryKey(cleaned.SelectedValues[i]) < actionValueSummaryKey(cleaned.SelectedValues[j])
	})
	cleaned.RedactionCount = len(cleaned.SelectedValues)
	if _, err := validateActionCaseWithScanner(scanner, kind, cleaned); err != nil {
		return Case{}, err
	}
	return Case{ID: id, Kind: kind, Action: &cleaned}, nil
}

func cloneActionCase(input ActionCase) ActionCase {
	out := input
	out.Request.Arguments = input.Request.Arguments
	out.Request.Result = input.Request.Result
	out.Request.Progress = input.Request.Progress
	out.Request.Context = cloneRawContext(input.Request.Context)
	out.Request.Completeness.Missing = append([]action.MissingEvidence{}, input.Request.Completeness.Missing...)
	out.State.CredentialLabels = append([]string{}, input.State.CredentialLabels...)
	out.State.Budget.Candidates = append([]action.BudgetCandidate{}, input.State.Budget.Candidates...)
	for index := range out.State.Budget.Candidates {
		out.State.Budget.Candidates[index].Scope.CredentialLabels = append(
			[]string{}, input.State.Budget.Candidates[index].Scope.CredentialLabels...,
		)
	}
	out.State.ResampleDrift = append([]ActionIdentityComponent{}, input.State.ResampleDrift...)
	out.State.Inspection = cloneFixtureInspection(input.State.Inspection)
	if input.State.RepositoryEffect != nil {
		effect := *input.State.RepositoryEffect
		effect.RuleIDs = append([]string{}, input.State.RepositoryEffect.RuleIDs...)
		out.State.RepositoryEffect = &effect
	}
	out.Expected.MatchedRuleIDs = append([]string{}, input.Expected.MatchedRuleIDs...)
	out.Expected.Completeness.Missing = append([]action.MissingEvidence{}, input.Expected.Completeness.Missing...)
	if input.Expected.Approval != nil {
		approval := *input.Expected.Approval
		out.Expected.Approval = &approval
	}
	out.SelectedValues = append([]ActionValueSummary{}, input.SelectedValues...)
	return out
}

func cloneFixtureInspection(input *action.InspectionEvidence) *action.InspectionEvidence {
	if input == nil {
		return nil
	}
	out := *input
	out.RuleIDs = append([]string{}, input.RuleIDs...)
	out.Categories = append([]action.DetectorCategory{}, input.Categories...)
	out.PackIdentities = append([]string{}, input.PackIdentities...)
	out.Fields = append([]action.InspectionFieldEvidence{}, input.Fields...)
	out.UnsupportedContent = append([]action.InspectionContentEvidence{}, input.UnsupportedContent...)
	return &out
}

func sanitizeActionRawValue(
	scanner *actioninspect.TextScanner,
	raw ActionPayload,
	source action.ValueSource,
	provenance action.Provenance,
	basePointer string,
	summaries []ActionValueSummary,
) (ActionPayload, []ActionValueSummary, error) {
	if len(raw) == 0 {
		return "", summaries, nil
	}
	if !utf8.ValidString(string(raw)) {
		return "", nil, fmt.Errorf("action fixture payload must be valid UTF-8")
	}
	if len(raw) > action.MaxArgumentBytes {
		summaries = append(summaries, ActionValueSummary{
			Source: source, Pointer: basePointer, Category: "oversized-value",
			ByteLength: len(raw), Provenance: provenance,
		})
		return oversizedActionSurrogate(), summaries, nil
	}
	value, err := action.ParseJSON([]byte(raw))
	if err != nil {
		if sensitiveRawActionText(scanner, json.RawMessage(raw)) {
			return "", nil, fmt.Errorf("malformed action JSON contains secret-shaped content")
		}
		return raw, summaries, nil
	}
	cleaned, summaries, err := sanitizeActionValue(scanner, value, source, provenance, basePointer, "", summaries)
	if err != nil {
		return "", nil, err
	}
	body, err := cleaned.MarshalJSON()
	if err != nil {
		return "", nil, err
	}
	return ActionPayload(body), summaries, nil
}

func sanitizeActionValue(
	scanner *actioninspect.TextScanner,
	value action.Value,
	source action.ValueSource,
	provenance action.Provenance,
	pointer string,
	fieldName string,
	summaries []ActionValueSummary,
) (action.Value, []ActionValueSummary, error) {
	if fieldName != "" && unsafeActionMetadata(fieldName) {
		return action.Value{}, nil, fmt.Errorf("action JSON member name contains private metadata")
	}
	if category, private := privateActionValueCategory(scanner, value, fieldName); private {
		byteLength, itemCount, err := actionValueShape(value)
		if err != nil {
			return action.Value{}, nil, err
		}
		summaries = append(summaries, ActionValueSummary{
			Source: source, Pointer: pointer, Category: category,
			ByteLength: byteLength, ItemCount: itemCount, Provenance: provenance,
		})
		redacted, err := action.String(redactedActionValue)
		if err != nil {
			return action.Value{}, nil, err
		}
		return redacted, summaries, nil
	}
	if items, ok := value.Items(); ok {
		out := make([]action.Value, len(items))
		for index, item := range items {
			child := pointer + "/" + fmt.Sprintf("%d", index)
			cleaned, next, err := sanitizeActionValue(scanner, item, source, provenance, child, "", summaries)
			if err != nil {
				return action.Value{}, nil, err
			}
			out[index], summaries = cleaned, next
		}
		array, err := action.Array(out)
		return array, summaries, err
	}
	if members, ok := value.Members(); ok {
		out := make([]action.Member, len(members))
		for index, member := range members {
			child := pointer + "/" + escapePointerToken(member.Name)
			cleaned, next, err := sanitizeActionValue(scanner, member.Value, source, provenance, child, member.Name, summaries)
			if err != nil {
				return action.Value{}, nil, err
			}
			out[index], summaries = action.Member{Name: member.Name, Value: cleaned}, next
		}
		object, err := action.Object(out)
		return object, summaries, err
	}
	return value, summaries, nil
}

func sensitiveActionScalar(value action.Value) bool {
	text, ok := value.Text()
	if !ok || text == redactedActionValue {
		return false
	}
	return sensitiveTextWithoutOversize(text)
}

func sensitiveTextWithoutOversize(text string) bool {
	_, count := sanitizeSensitiveText(text)
	if len(strings.Join(strings.Fields(text), " ")) > maxValueBytes {
		count--
	}
	return count > 0
}

func privateActionValueCategory(
	scanner *actioninspect.TextScanner,
	value action.Value,
	fieldName string,
) (string, bool) {
	text, textValue := value.Text()
	if textValue && text == redactedActionValue {
		return "", false
	}
	if secretKey.MatchString(fieldName) {
		return "credential", true
	}
	if textValue && len(text) > maxValueBytes {
		if physicalPathString(text) {
			return "physical-path", true
		}
		if oversizedActionPayloadPrivate([]byte(text)) {
			return "credential", true
		}
		return "oversized-value", true
	}
	if sensitiveActionScalar(value) {
		return "credential", true
	}
	if textValue {
		categories, err := scanner.PrivateCategories(context.Background(), text, action.MaxArgumentBytes)
		if err != nil {
			return "inspection-incomplete", true
		}
		if len(categories) > 0 {
			return strings.ReplaceAll(string(categories[0]), "_", "-"), true
		}
	}
	if physicalPathScalar(value) {
		return "physical-path", true
	}
	return "", false
}

func physicalPathScalar(value action.Value) bool {
	text, ok := value.Text()
	if !ok {
		return false
	}
	return physicalPathString(text)
}

func physicalPathString(value string) bool {
	if strings.Contains(strings.ToLower(value), "file:///") {
		return true
	}
	for index := range value {
		if value[index] == '/' && physicalPathBoundary(value, index) &&
			(index+1 == len(value) || value[index+1] != '/') {
			return true
		}
		if index+2 < len(value) && isASCIIAlpha(value[index]) && value[index+1] == ':' &&
			(value[index+2] == '\\' || value[index+2] == '/') && physicalPathBoundary(value, index) {
			return true
		}
	}
	return false
}

func physicalPathBoundary(value string, index int) bool {
	if index == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(value[:index])
	return !unicode.IsLetter(previous) && !unicode.IsDigit(previous) && previous != '/' && previous != '\\'
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func unsafeActionMetadata(value string) bool {
	if !utf8.ValidString(value) || physicalPathString(value) {
		return true
	}
	_, redactions := sanitizeSensitiveText(value)
	return redactions > 0
}

func actionValueShape(value action.Value) (int, int, error) {
	body, err := value.MarshalJSON()
	if err != nil {
		return 0, 0, err
	}
	return len(body), actionValueItems(value), nil
}

func actionValueItems(value action.Value) int {
	if items, ok := value.Items(); ok {
		count := len(items)
		for _, item := range items {
			count += actionValueItems(item)
		}
		return count
	}
	if members, ok := value.Members(); ok {
		count := len(members)
		for _, member := range members {
			count += actionValueItems(member.Value)
		}
		return count
	}
	return 0
}

func validateActionPrivateRequest(scanner *actioninspect.TextScanner, request ActionRequestFixture) error {
	if scanner == nil {
		return fmt.Errorf("action privacy scanner is unavailable")
	}
	values := []ActionPayload{request.Arguments, request.Result, request.Progress}
	for _, raw := range values {
		if len(raw) > action.MaxArgumentBytes && !canonicalOversizedActionSurrogate(raw) {
			return fmt.Errorf("oversized action fixture is not the canonical privacy surrogate")
		}
		if sensitiveRawActionText(scanner, json.RawMessage(raw)) {
			return fmt.Errorf("action fixture contains raw secret-shaped or physical-path content")
		}
	}
	for _, context := range request.Context {
		if len(context.Value) > action.MaxArgumentBytes &&
			!canonicalOversizedActionSurrogate(ActionPayload(context.Value)) {
			return fmt.Errorf("oversized action context %q is not the canonical privacy surrogate", context.Name)
		}
		if sensitiveRawActionText(scanner, context.Value) {
			return fmt.Errorf("action context %q contains raw secret-shaped or physical-path content", context.Name)
		}
	}
	return nil
}

func sensitiveRawActionText(scanner *actioninspect.TextScanner, raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if len(raw) > action.MaxArgumentBytes {
		return oversizedActionPayloadPrivate(raw)
	}
	value, err := action.ParseJSON(raw)
	if err != nil {
		return sensitiveMalformedActionJSON(scanner, raw)
	}
	return actionValueContainsPrivate(scanner, value, "")
}

func oversizedActionPayloadPrivate(raw []byte) bool {
	lower := bytes.ToLower(raw)
	if bytes.ContainsAny(lower, `/\`) {
		return true
	}
	for _, needle := range oversizedPrivateNeedles {
		if bytes.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func oversizedActionSurrogate() ActionPayload {
	body := make([]byte, action.MaxArgumentBytes+2)
	body[0], body[len(body)-1] = '"', '"'
	for index := 1; index < len(body)-1; index++ {
		body[index] = 'x'
	}
	return ActionPayload(body)
}

func canonicalOversizedActionSurrogate(raw ActionPayload) bool {
	if len(raw) != action.MaxArgumentBytes+2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false
	}
	return strings.Trim(string(raw[1:len(raw)-1]), "x") == ""
}

func sensitiveMalformedActionJSON(scanner *actioninspect.TextScanner, raw json.RawMessage) bool {
	text := string(raw)
	if secretKey.MatchString(text) || secretPrefix.MatchString(text) ||
		secretURL.MatchString(text) || secretQuery.MatchString(text) || physicalPathString(text) ||
		privateTextCategory(scanner, text) {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		value, ok := token.(string)
		if ok && (secretKey.MatchString(value) || sensitiveTextWithoutOversize(value) ||
			physicalPathString(value) || privateTextCategory(scanner, value)) {
			return true
		}
	}
}

func actionValueContainsPrivate(scanner *actioninspect.TextScanner, value action.Value, fieldName string) bool {
	if secretKey.MatchString(fieldName) {
		text, redacted := value.Text()
		if !redacted || text != redactedActionValue {
			return true
		}
	}
	if _, private := privateActionValueCategory(scanner, value, fieldName); private {
		return true
	}
	if items, ok := value.Items(); ok {
		for _, item := range items {
			if actionValueContainsPrivate(scanner, item, "") {
				return true
			}
		}
	}
	if members, ok := value.Members(); ok {
		for _, member := range members {
			if actionValueContainsPrivate(scanner, member.Value, member.Name) {
				return true
			}
		}
	}
	return false
}

func privateTextCategory(scanner *actioninspect.TextScanner, text string) bool {
	categories, err := scanner.PrivateCategories(context.Background(), text, action.MaxArgumentBytes)
	return err != nil || len(categories) > 0
}

func escapePointerToken(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}
