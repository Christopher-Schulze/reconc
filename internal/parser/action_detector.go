package parser

import (
	"fmt"
	"math"

	"gopkg.in/yaml.v3"
	"reconc.dev/reconc/internal/action"
)

func parseActionDetectors(node *yaml.Node, sourcePath string) ([]action.DetectorPolicy, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, actionError("actions.detectors must be a list in " + sourcePath)
	}
	policies := make([]action.DetectorPolicy, 0, len(node.Content))
	for index, item := range node.Content {
		context := fmt.Sprintf("%s actions.detectors[%d]", sourcePath, index)
		fields, err := actionMapping(item, context, actionFieldSet(
			"id", "selector", "pack_id", "pack_digest", "fields", "categories",
			"forbidden_terms", "pre_call_decision", "post_result_disposition",
			"progress_disposition", "schema_policy", "allowed_content_types",
			"trusted_annotation_fields", "limits",
		))
		if err != nil {
			return nil, err
		}
		if err := validateOptionalActionStrings(
			fields, context, "pre_call_decision", "post_result_disposition",
			"progress_disposition", "schema_policy",
		); err != nil {
			return nil, err
		}
		id, err := requiredActionString(fields, "id", context)
		if err != nil {
			return nil, err
		}
		packID, err := requiredActionString(fields, "pack_id", context)
		if err != nil {
			return nil, err
		}
		packDigest, err := requiredActionString(fields, "pack_digest", context)
		if err != nil {
			return nil, err
		}
		selectorNode, ok := fields["selector"]
		if !ok {
			return nil, actionError(context + ".selector is required")
		}
		selector, err := parseActionSelector(selectorNode, context)
		if err != nil {
			return nil, err
		}
		detectorFields, err := parseActionDetectorFields(fields["fields"], context)
		if err != nil {
			return nil, err
		}
		categories, err := optionalActionStrings(fields, "categories", context)
		if err != nil {
			return nil, err
		}
		forbiddenTerms, err := optionalActionStrings(fields, "forbidden_terms", context)
		if err != nil {
			return nil, err
		}
		allowedTypes, err := optionalActionStrings(fields, "allowed_content_types", context)
		if err != nil {
			return nil, err
		}
		trustedAnnotations, err := optionalActionStrings(fields, "trusted_annotation_fields", context)
		if err != nil {
			return nil, err
		}
		limits, err := parseActionInspectionLimits(fields["limits"], context)
		if err != nil {
			return nil, err
		}
		policies = append(policies, action.DetectorPolicy{
			ID: id, Selector: selector, PackID: packID, PackDigest: packDigest,
			Fields: detectorFields, Categories: actionDetectorCategories(categories),
			ForbiddenTerms:  forbiddenTerms,
			PreCallDecision: action.Decision(optionalActionString(fields, "pre_call_decision")),
			PostResultDisposition: action.ResultDisposition(
				optionalActionString(fields, "post_result_disposition"),
			),
			ProgressDisposition: action.ProgressDisposition(
				optionalActionString(fields, "progress_disposition"),
			),
			SchemaPolicy:            action.SchemaPolicy(optionalActionString(fields, "schema_policy")),
			AllowedContentTypes:     actionContentTypes(allowedTypes),
			TrustedAnnotationFields: trustedAnnotations, Limits: limits,
			SourceIdentity: sourcePath,
		})
	}
	return policies, nil
}

func parseActionDetectorFields(node *yaml.Node, context string) ([]action.DetectorField, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, actionError(context + ".fields must be a list")
	}
	fields := make([]action.DetectorField, 0, len(node.Content))
	for index, item := range node.Content {
		fieldContext := fmt.Sprintf("%s.fields[%d]", context, index)
		values, err := actionMapping(item, fieldContext, actionFieldSet("source", "pointer"))
		if err != nil {
			return nil, err
		}
		source, err := requiredActionString(values, "source", fieldContext)
		if err != nil {
			return nil, err
		}
		pointer, err := requiredActionStringAllowEmpty(values, "pointer", fieldContext)
		if err != nil {
			return nil, err
		}
		fields = append(fields, action.DetectorField{Source: action.ValueSource(source), Pointer: pointer})
	}
	return fields, nil
}

func parseActionInspectionLimits(node *yaml.Node, context string) (action.InspectionLimits, error) {
	if node == nil {
		return action.InspectionLimits{}, nil
	}
	fields, err := actionMapping(node, context+".limits", actionFieldSet(
		"max_bytes", "max_items", "max_depth", "max_milliseconds",
	))
	if err != nil {
		return action.InspectionLimits{}, err
	}
	read := func(name string) (uint64, error) {
		value, valueErr := optionalActionUint(fields, name, context+".limits", false)
		if valueErr != nil || value == nil {
			return 0, valueErr
		}
		return *value, nil
	}
	maxBytes, err := read("max_bytes")
	if err != nil {
		return action.InspectionLimits{}, err
	}
	maxItems, err := read("max_items")
	if err != nil {
		return action.InspectionLimits{}, err
	}
	maxDepth, err := read("max_depth")
	if err != nil {
		return action.InspectionLimits{}, err
	}
	maxMilliseconds, err := read("max_milliseconds")
	if err != nil {
		return action.InspectionLimits{}, err
	}
	if maxItems > math.MaxUint32 || maxDepth > math.MaxUint32 || maxMilliseconds > math.MaxUint32 {
		return action.InspectionLimits{}, actionError(context + ".limits exceeds 32-bit range")
	}
	return action.InspectionLimits{
		MaxBytes: maxBytes, MaxItems: uint32(maxItems), MaxDepth: uint32(maxDepth),
		MaxMilliseconds: uint32(maxMilliseconds),
	}, nil
}

func actionDetectorCategories(values []string) []action.DetectorCategory {
	if values == nil {
		return nil
	}
	out := make([]action.DetectorCategory, len(values))
	for index := range values {
		out[index] = action.DetectorCategory(values[index])
	}
	return out
}

func actionContentTypes(values []string) []action.ContentType {
	if values == nil {
		return nil
	}
	out := make([]action.ContentType, len(values))
	for index := range values {
		out[index] = action.ContentType(values[index])
	}
	return out
}
