package action

import (
	"fmt"
	"sort"
)

type inspectionExpectation struct {
	packIdentities []string
	fieldCount     int
}

func normalizeInspectionEvidence(
	input *InspectionEvidence,
	expected inspectionExpectation,
	phase Phase,
) (*InspectionEvidence, *RequestError) {
	required := len(expected.packIdentities) > 0
	if input == nil {
		if required {
			return nil, &RequestError{Code: ReasonInspectionIncomplete, Message: "required content inspection evidence is absent"}
		}
		return nil, nil
	}
	if !required || !input.Status.Valid() || !ValidKeyedIdentity(input.Identity) ||
		!input.SchemaStatus.Valid() ||
		(input.SchemaIdentity != "absent" && !ValidSHA256Identity(input.SchemaIdentity)) ||
		input.ScannedBytes > MaxArgumentBytes ||
		input.ScannedItems > MaxJSONItems {
		return nil, &RequestError{Code: ReasonInvalidRequest, Message: "content inspection evidence is invalid"}
	}
	if input.SchemaStatus == InspectionSchemaValid || input.SchemaStatus == InspectionSchemaInvalid {
		if !ValidSHA256Identity(input.SchemaIdentity) {
			return nil, &RequestError{Code: ReasonInvalidRequest, Message: "content inspection schema identity is absent"}
		}
	} else if input.SchemaIdentity != "absent" {
		return nil, &RequestError{Code: ReasonInvalidRequest, Message: "content inspection schema identity is unexpected"}
	}
	out := cloneInspectionEvidence(input)
	if err := normalizeInspectionLists(out, phase); err != nil {
		return nil, &RequestError{Code: ReasonInvalidRequest, Message: err.Error()}
	}
	if !equalStrings(out.PackIdentities, expected.packIdentities) {
		return nil, &RequestError{Code: ReasonInvalidRequest, Message: "content inspection pack identities do not match policy"}
	}
	if err := validateInspectionPhase(out, phase, expected.fieldCount); err != nil {
		return nil, &RequestError{Code: ReasonInvalidRequest, Message: err.Error()}
	}
	switch out.Status {
	case InspectionClean:
		if out.Decision != "" || out.Reason != "" || len(out.RuleIDs) != 0 || len(out.Categories) != 0 {
			return nil, &RequestError{Code: ReasonInvalidRequest, Message: "clean inspection evidence contains a match"}
		}
	case InspectionMatched:
		if out.Decision != DecisionWarn && out.Decision != DecisionBlock &&
			out.Decision != DecisionRequireApproval || len(out.RuleIDs) == 0 ||
			len(out.Categories) == 0 || out.Reason != ReasonRuleMatched &&
			out.Reason != ReasonResultWithheld {
			return nil, &RequestError{Code: ReasonInvalidRequest, Message: "matched inspection evidence is incomplete"}
		}
	case InspectionIncomplete:
		if out.Decision != DecisionBlock || len(out.RuleIDs) != 0 || len(out.Categories) != 0 ||
			!inspectionFailureReason(out.Reason) {
			return nil, &RequestError{Code: ReasonInvalidRequest, Message: "incomplete inspection evidence has an invalid failure"}
		}
	}
	return out, nil
}

func normalizeInspectionLists(evidence *InspectionEvidence, phase Phase) error {
	if len(evidence.RuleIDs) > MaxDetectors || len(evidence.Categories) > MaxDetectorCategories ||
		len(evidence.PackIdentities) > MaxDetectors || len(evidence.Fields) > MaxJSONItems ||
		len(evidence.UnsupportedContent) > MaxJSONItems {
		return fmt.Errorf("content inspection evidence exceeds a count limit")
	}
	if err := normalizeInspectionRuleIDs(&evidence.RuleIDs); err != nil {
		return err
	}
	if err := normalizeInspectionCategories(&evidence.Categories); err != nil {
		return err
	}
	if err := normalizeInspectionPackIdentities(&evidence.PackIdentities); err != nil {
		return err
	}
	if len(evidence.PackIdentities) == 0 {
		return fmt.Errorf("content inspection pack identities are absent")
	}
	sort.Slice(evidence.Fields, func(i, j int) bool {
		if evidence.Fields[i].Source != evidence.Fields[j].Source {
			return evidence.Fields[i].Source < evidence.Fields[j].Source
		}
		return evidence.Fields[i].PointerIdentity < evidence.Fields[j].PointerIdentity
	})
	var scannedBytes uint64
	var scannedItems uint32
	for index, field := range evidence.Fields {
		if field.Source != phaseSource(phase) || !ValidKeyedIdentity(field.PointerIdentity) ||
			!ValidKeyedIdentity(field.ValueIdentity) || field.ByteLength > MaxArgumentBytes ||
			field.ItemCount > MaxJSONItems {
			return fmt.Errorf("content inspection field evidence is invalid")
		}
		if index > 0 && evidence.Fields[index-1].Source == field.Source &&
			evidence.Fields[index-1].PointerIdentity == field.PointerIdentity {
			return fmt.Errorf("content inspection field evidence is duplicated")
		}
		if field.ByteLength > MaxArgumentBytes-scannedBytes || field.ItemCount > MaxJSONItems-scannedItems {
			return fmt.Errorf("content inspection field totals exceed their boundary")
		}
		scannedBytes += field.ByteLength
		scannedItems += field.ItemCount
	}
	if scannedBytes != evidence.ScannedBytes || scannedItems != evidence.ScannedItems {
		return fmt.Errorf("content inspection field totals do not match their evidence")
	}
	sort.Slice(evidence.UnsupportedContent, func(i, j int) bool {
		if evidence.UnsupportedContent[i].ContentType != evidence.UnsupportedContent[j].ContentType {
			return evidence.UnsupportedContent[i].ContentType < evidence.UnsupportedContent[j].ContentType
		}
		return evidence.UnsupportedContent[i].Identity < evidence.UnsupportedContent[j].Identity
	})
	for index, binary := range evidence.UnsupportedContent {
		if !binary.ContentType.Valid() || !ValidKeyedIdentity(binary.Identity) ||
			binary.ByteLength > MaxArgumentBytes {
			return fmt.Errorf("unsupported content evidence is invalid")
		}
		if index > 0 && evidence.UnsupportedContent[index-1].ContentType == binary.ContentType &&
			evidence.UnsupportedContent[index-1].Identity == binary.Identity {
			return fmt.Errorf("unsupported content evidence is duplicated")
		}
	}
	return nil
}

func validateInspectionPhase(evidence *InspectionEvidence, phase Phase, expectedFields int) error {
	if phase == PhasePostResult {
		if evidence.SchemaStatus == InspectionSchemaNotApplicable {
			return fmt.Errorf("post-result inspection schema status is not applicable")
		}
	} else if evidence.SchemaStatus != InspectionSchemaNotApplicable || evidence.SchemaIdentity != "absent" {
		return fmt.Errorf("non-result inspection contains schema evidence")
	}
	if phase != PhasePostResult && len(evidence.UnsupportedContent) > 0 {
		return fmt.Errorf("non-result inspection contains result content evidence")
	}
	if evidence.Status != InspectionIncomplete && len(evidence.Fields) != expectedFields {
		return fmt.Errorf("content inspection field coverage does not match policy")
	}
	if evidence.Status == InspectionIncomplete && len(evidence.Fields) > expectedFields {
		return fmt.Errorf("incomplete content inspection exceeds policy field coverage")
	}
	if evidence.Status == InspectionMatched && !validInspectionMatchOutcome(evidence, phase) {
		return fmt.Errorf("content inspection match outcome is incompatible with its phase")
	}
	if evidence.Status == InspectionIncomplete {
		if evidence.Reason == ReasonSchemaInvalid && phase != PhasePostResult {
			return fmt.Errorf("schema failure is outside the post-result phase")
		}
		if evidence.Reason == ReasonUnsupportedContent &&
			(phase != PhasePostResult || len(evidence.UnsupportedContent) == 0) {
			return fmt.Errorf("unsupported content failure has no result content evidence")
		}
	}
	return nil
}

func validInspectionMatchOutcome(evidence *InspectionEvidence, phase Phase) bool {
	switch phase {
	case PhasePreCall:
		return evidence.Reason == ReasonRuleMatched &&
			(evidence.Decision == DecisionWarn || evidence.Decision == DecisionBlock ||
				evidence.Decision == DecisionRequireApproval)
	case PhasePostResult, PhaseProgress:
		return evidence.Decision == DecisionWarn && evidence.Reason == ReasonRuleMatched ||
			evidence.Decision == DecisionBlock && evidence.Reason == ReasonResultWithheld
	default:
		return false
	}
}

func normalizeInspectionRuleIDs(values *[]string) error {
	sort.Strings(*values)
	for index, value := range *values {
		if !SafeLabel(value) || index > 0 && (*values)[index-1] == value {
			return fmt.Errorf("content inspection rule identities are invalid")
		}
	}
	if *values == nil {
		*values = []string{}
	}
	return nil
}

func normalizeInspectionCategories(values *[]DetectorCategory) error {
	sort.Slice(*values, func(i, j int) bool { return (*values)[i] < (*values)[j] })
	for index, value := range *values {
		if !value.Valid() || index > 0 && (*values)[index-1] == value {
			return fmt.Errorf("content inspection categories are invalid")
		}
	}
	if *values == nil {
		*values = []DetectorCategory{}
	}
	return nil
}

func normalizeInspectionPackIdentities(values *[]string) error {
	sort.Strings(*values)
	for index, value := range *values {
		if !ValidSHA256Identity(value) || index > 0 && (*values)[index-1] == value {
			return fmt.Errorf("content inspection pack identities are invalid")
		}
	}
	if *values == nil {
		*values = []string{}
	}
	return nil
}

func inspectionFailureReason(reason ReasonCode) bool {
	switch reason {
	case ReasonInspectionIncomplete, ReasonUnsupportedContent, ReasonSchemaInvalid,
		ReasonLimitExceeded, ReasonInvalidUTF8, ReasonCancelled, ReasonDeadlineExceeded:
		return true
	default:
		return false
	}
}

func inspectionIdentity(evidence *InspectionEvidence) string {
	if evidence == nil {
		return "absent"
	}
	return evidence.Identity
}

func (e *Evaluator) inspectionExpectation(request Request) inspectionExpectation {
	if e == nil {
		return inspectionExpectation{}
	}
	_, toolID := e.selectTool(request)
	packs := make(map[string]struct{})
	fields := make(map[string]struct{})
	for _, policy := range e.plan.Detectors {
		if selectorMatches(policy.Selector, request, toolID) {
			packs[policy.PackDigest] = struct{}{}
			for _, field := range policy.Fields {
				if field.Source == phaseSource(request.Phase) {
					fields[string(field.Source)+"\x00"+field.Pointer] = struct{}{}
				}
			}
		}
	}
	expected := inspectionExpectation{
		packIdentities: make([]string, 0, len(packs)),
		fieldCount:     len(fields),
	}
	for identity := range packs {
		expected.packIdentities = append(expected.packIdentities, identity)
	}
	sort.Strings(expected.packIdentities)
	return expected
}

func cloneInspectionEvidence(source *InspectionEvidence) *InspectionEvidence {
	if source == nil {
		return nil
	}
	out := *source
	out.RuleIDs = cloneSlice(source.RuleIDs)
	out.Categories = cloneSlice(source.Categories)
	out.PackIdentities = cloneSlice(source.PackIdentities)
	out.Fields = cloneSlice(source.Fields)
	out.UnsupportedContent = cloneSlice(source.UnsupportedContent)
	return &out
}
