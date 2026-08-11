package actioninspect

import (
	"encoding/json"
	"fmt"
	"sort"

	"reconc.dev/reconc/internal/action"
)

const (
	WithholdFormatVersion = "1"
	MaxWithholdRuleIDs    = 64
)

type withheldText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type withheldStructuredContent struct {
	FormatVersion        string                    `json:"format_version"`
	Outcome              string                    `json:"outcome"`
	CorrelationID        string                    `json:"correlation_id"`
	Categories           []action.DetectorCategory `json:"categories"`
	RuleIDs              []string                  `json:"rule_ids"`
	OmittedRuleIDs       int                       `json:"omitted_rule_ids"`
	DownstreamSideEffect string                    `json:"downstream_side_effect"`
	Delivery             string                    `json:"delivery"`
}

type withheldMeta struct {
	Reconc withheldStructuredContent `json:"reconc"`
}

type withheldToolResult struct {
	ResultType string         `json:"resultType"`
	Content    []withheldText `json:"content"`
	Meta       withheldMeta   `json:"_meta"`
	IsError    bool           `json:"isError"`
}

func WithheldMCPResult(callID string, evidence *action.InspectionEvidence) ([]byte, error) {
	if !validCallID(callID) || evidence == nil || evidence.Status != action.InspectionMatched ||
		evidence.Decision != action.DecisionBlock || evidence.Reason != action.ReasonResultWithheld ||
		len(evidence.RuleIDs) == 0 || len(evidence.RuleIDs) > action.MaxDetectors ||
		len(evidence.Categories) == 0 || len(evidence.Categories) > action.MaxDetectorCategories {
		return nil, fmt.Errorf("withheld result evidence is invalid")
	}
	ruleIDs := append([]string(nil), evidence.RuleIDs...)
	categories := append([]action.DetectorCategory(nil), evidence.Categories...)
	sort.Strings(ruleIDs)
	sort.Slice(categories, func(i, j int) bool { return categories[i] < categories[j] })
	for index, ruleID := range ruleIDs {
		if !action.SafeLabel(ruleID) {
			return nil, fmt.Errorf("withheld result rule identity is invalid")
		}
		if index > 0 && ruleIDs[index-1] == ruleID {
			return nil, fmt.Errorf("withheld result rule identity is duplicated")
		}
	}
	for index, category := range categories {
		if !category.Valid() {
			return nil, fmt.Errorf("withheld result category is invalid")
		}
		if index > 0 && categories[index-1] == category {
			return nil, fmt.Errorf("withheld result category is duplicated")
		}
	}
	omitted := 0
	if len(ruleIDs) > MaxWithholdRuleIDs {
		omitted = len(ruleIDs) - MaxWithholdRuleIDs
		ruleIDs = ruleIDs[:MaxWithholdRuleIDs]
	}
	result := withheldToolResult{
		ResultType: "complete",
		Content:    []withheldText{{Type: "text", Text: "Reconc withheld the downstream tool result."}},
		Meta: withheldMeta{
			Reconc: withheldStructuredContent{
				FormatVersion: WithholdFormatVersion, Outcome: "withheld", CorrelationID: callID,
				Categories: categories, RuleIDs: ruleIDs, OmittedRuleIDs: omitted,
				DownstreamSideEffect: "already_executed_or_unknown", Delivery: "original_content_withheld",
			},
		},
		IsError: true,
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode withheld result: %w", err)
	}
	return body, nil
}

func validCallID(value string) bool {
	if len(value) != 30 || value[:4] != "act_" {
		return false
	}
	for _, character := range value[4:] {
		if !((character >= 'a' && character <= 'z') || (character >= '2' && character <= '7')) {
			return false
		}
	}
	return true
}
