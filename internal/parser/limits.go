package parser

import (
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
)

// Parser resource limits are intentionally stricter than the source byte
// ceiling. They bound the retained typed graph before matching or lockfile
// serialization can amplify work.
const (
	maxParserRules             = 4096
	maxParserChecksPerRule     = 256
	maxParserListItems         = 256
	maxParserPatternBytes      = 1024
	maxParserCommandBytes      = 16 << 10
	maxParserMessageBytes      = 64 << 10
	maxParserScalarBytes       = 4 << 20
	maxParserYAMLDepth         = 32
	maxParserYAMLNodes         = 131072
	maxParserYAMLExpandedNodes = 262144
	maxParserYAMLAliases       = 1024
)

type yamlBounds struct {
	nodes         int
	expandedNodes int
	scalarBytes   int
	aliases       int
	activeAliases map[*yaml.Node]bool
}

type parserSourceDocument struct {
	root    *yaml.Node
	mapping map[string]interface{}
}

func decodeRuleSourceDocumentBounded(source policy.PolicySource) (*parserSourceDocument, error) {
	if source.Kind == policy.SourceCompilerConfig || impactCandidateSource(source) {
		return decodeYAMLDocumentBytesBounded(source.Content, source.Path)
	}
	return decodeYAMLDocumentBounded(source.Content, source.Path)
}

func decodeYAMLDocumentBounded(raw, context string) (*parserSourceDocument, error) {
	return decodeYAMLDocumentBytesBounded(strings.TrimSpace(raw), context)
}

func decodeYAMLDocumentBytesBounded(raw, context string) (*parserSourceDocument, error) {
	if strings.TrimSpace(raw) == "" {
		return &parserSourceDocument{
			root:    &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"},
			mapping: map[string]interface{}{},
		}, nil
	}
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, &rerrors.RuleValidationError{Message: "invalid yaml in " + context, Cause: err}
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, &rerrors.RuleValidationError{Message: "YAML input must contain one document in " + context}
		}
		return nil, &rerrors.RuleValidationError{Message: "invalid trailing yaml in " + context, Cause: err}
	}
	bound := yamlBounds{activeAliases: make(map[*yaml.Node]bool)}
	if err := walkYAMLNode(&document, 0, &bound, context); err != nil {
		return nil, err
	}
	var decoded interface{}
	if err := document.Decode(&decoded); err != nil {
		return nil, &rerrors.RuleValidationError{Message: "invalid yaml in " + context, Cause: err}
	}
	if decoded == nil {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if len(document.Content) == 1 {
			root = document.Content[0]
		}
		return &parserSourceDocument{
			root:    root,
			mapping: map[string]interface{}{},
		}, nil
	}
	mapping, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{Message: "expected a YAML mapping in " + context}
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, &rerrors.RuleValidationError{Message: "expected a YAML mapping in " + context}
	}
	return &parserSourceDocument{root: document.Content[0], mapping: mapping}, nil
}

func decodeYAMLMappingBounded(raw, context string) (map[string]interface{}, error) {
	document, err := decodeYAMLDocumentBounded(raw, context)
	if err != nil {
		return nil, err
	}
	return document.mapping, nil
}

func walkYAMLNode(node *yaml.Node, depth int, bound *yamlBounds, context string) error {
	if node == nil {
		return nil
	}
	if depth > maxParserYAMLDepth {
		return parserLimitError(context, "yaml nesting depth", depth, maxParserYAMLDepth, "levels")
	}
	bound.nodes++
	if bound.nodes > maxParserYAMLNodes {
		return parserLimitError(context, "yaml nodes", bound.nodes, maxParserYAMLNodes, "nodes")
	}
	bound.expandedNodes++
	if bound.expandedNodes > maxParserYAMLExpandedNodes {
		return parserLimitError(context, "expanded yaml nodes", bound.expandedNodes, maxParserYAMLExpandedNodes, "nodes")
	}
	if node.Kind == yaml.AliasNode {
		bound.aliases++
		if bound.aliases > maxParserYAMLAliases {
			return parserLimitError(context, "yaml aliases", bound.aliases, maxParserYAMLAliases, "aliases")
		}
		if bound.activeAliases[node.Alias] {
			return &rerrors.RuleValidationError{Message: "recursive yaml alias in " + context}
		}
		bound.activeAliases[node.Alias] = true
		if err := walkYAMLNode(node.Alias, depth+1, bound, context); err != nil {
			return err
		}
		delete(bound.activeAliases, node.Alias)
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		bound.scalarBytes += len(node.Value)
		if bound.scalarBytes > maxParserScalarBytes {
			return parserLimitError(context, "decoded scalar bytes", bound.scalarBytes, maxParserScalarBytes, "bytes")
		}
	}
	for _, child := range node.Content {
		if err := walkYAMLNode(child, depth+1, bound, context); err != nil {
			return err
		}
	}
	return nil
}

func validateRuleDocumentBounds(src policy.PolicySource, doc map[string]interface{}, existingRules int) error {
	if err := validateScopeBounds(src, doc["scopes"]); err != nil {
		return err
	}
	ruleCount := existingRules
	if rawRules, ok := doc["rules"].([]interface{}); ok {
		ruleCount += len(rawRules)
		if err := validateRuleListBounds(src, rawRules, "rules", ""); err != nil {
			return err
		}
	}
	if rawScopes, ok := doc["scopes"].([]interface{}); ok {
		for scopeIndex, rawScope := range rawScopes {
			scope, ok := rawScope.(map[string]interface{})
			if !ok {
				continue
			}
			if rawRules, ok := scope["rules"].([]interface{}); ok {
				ruleCount += len(rawRules)
				if err := validateRuleListBounds(src, rawRules, fmt.Sprintf("scopes[%d].rules", scopeIndex), ""); err != nil {
					return err
				}
			}
		}
	}
	if ruleCount > maxParserRules {
		return parserLimitError(sourceLocation(src), "rules", ruleCount, maxParserRules, "rules")
	}
	return nil
}

func validateRuleListBounds(src policy.PolicySource, list []interface{}, field, prefix string) error {
	for index, rawRule := range list {
		rule, ok := rawRule.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := rule["id"].(string)
		if err := validateRuleMapBounds(src, rule, field+"["+itoa(index)+"]", id); err != nil {
			return err
		}
	}
	return nil
}

func validateRuleMapBounds(src policy.PolicySource, rule map[string]interface{}, location, ruleID string) error {
	if err := validateScalarLimit(src, location, ruleID, "id", rule["id"], maxParserMessageBytes); err != nil {
		return err
	}
	if err := validateScalarLimit(src, location, ruleID, "message", rule["message"], maxParserMessageBytes); err != nil {
		return err
	}
	for _, field := range []string{"template", "kind", "mode", "deprecated_reason", "deprecated_since", "deprecated_replaced_by"} {
		if err := validateScalarLimit(src, location, ruleID, field, rule[field], maxParserMessageBytes); err != nil {
			return err
		}
	}
	if err := validateScalarLimit(src, location, ruleID, "script", rule["script"], maxParserPatternBytes); err != nil {
		return err
	}
	for _, field := range []string{"paths", "before_paths", "when_paths", "scope_paths"} {
		if err := validateStringListLimit(src, location, ruleID, field, rule[field], maxParserPatternBytes); err != nil {
			return err
		}
	}
	for _, field := range []string{"commands", "args", "claims", "cache_inputs"} {
		if err := validateStringListLimit(src, location, ruleID, field, rule[field], maxParserCommandBytes); err != nil {
			return err
		}
	}
	if err := validateListLength(src, location, ruleID, "required_files", rule["required_files"], maxParserListItems); err != nil {
		return err
	}
	if err := validateListLength(src, location, ruleID, "evidence", rule["evidence"], maxParserListItems); err != nil {
		return err
	}
	if err := validateListLength(src, location, ruleID, "checks", rule["checks"], maxParserChecksPerRule); err != nil {
		return err
	}
	if err := validateRequiredFileBounds(src, location, ruleID, rule["required_files"]); err != nil {
		return err
	}
	if err := validateEvidenceBounds(src, location, ruleID, rule["evidence"]); err != nil {
		return err
	}
	if err := validateAssuranceBounds(src, location, ruleID, rule["assurance"]); err != nil {
		return err
	}
	if checks, ok := rule["checks"].([]interface{}); ok {
		for index, rawCheck := range checks {
			check, ok := rawCheck.(map[string]interface{})
			if !ok {
				continue
			}
			for _, field := range []string{"kind", "path", "file"} {
				if err := validateScalarLimit(src, location, ruleID, fmt.Sprintf("checks[%d].%s", index, field), check[field], maxParserPatternBytes); err != nil {
					return err
				}
			}
			if err := validateScalarLimit(src, location, ruleID, fmt.Sprintf("checks[%d].script", index), check["script"], maxParserPatternBytes); err != nil {
				return err
			}
			for _, field := range []string{"must_contain", "must_not_contain"} {
				if err := validateScalarOrStringListLimit(src, location, ruleID, fmt.Sprintf("checks[%d].%s", index, field), check[field], maxParserMessageBytes); err != nil {
					return err
				}
			}
			for _, field := range []string{"commands", "args", "claims", "cache_inputs"} {
				if err := validateStringListLimit(src, location, ruleID, fmt.Sprintf("checks[%d].%s", index, field), check[field], maxParserCommandBytes); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateScopeBounds(src policy.PolicySource, raw interface{}) error {
	scopes, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	if len(scopes) > maxParserListItems {
		return parserLimitError(sourceLocation(src), "scopes", len(scopes), maxParserListItems, "items")
	}
	for index, rawScope := range scopes {
		scope, ok := rawScope.(map[string]interface{})
		if !ok {
			continue
		}
		location := fmt.Sprintf("%s scope[%d]", sourceLocation(src), index)
		if err := validateTextValue(location, "id", scope["id"], maxParserMessageBytes); err != nil {
			return err
		}
		if err := validateStringListLimitAt(location, "paths", scope["paths"], maxParserPatternBytes); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredFileBounds(src policy.PolicySource, location, ruleID string, raw interface{}) error {
	if err := validateListLength(src, location, ruleID, "required_files", raw, maxParserListItems); err != nil {
		return err
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	for index, item := range items {
		mapping, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if err := validateScalarLimit(src, location, ruleID, fmt.Sprintf("required_files[%d].path", index), mapping["path"], maxParserPatternBytes); err != nil {
			return err
		}
	}
	return nil
}

func validateEvidenceBounds(src policy.PolicySource, location, ruleID string, raw interface{}) error {
	if err := validateListLength(src, location, ruleID, "evidence", raw, maxParserListItems); err != nil {
		return err
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	for index, item := range items {
		mapping, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if err := validateScalarLimit(src, location, ruleID, fmt.Sprintf("evidence[%d].file", index), mapping["file"], maxParserPatternBytes); err != nil {
			return err
		}
		for _, field := range []string{"must_contain", "must_not_contain"} {
			if err := validateScalarOrStringListLimit(src, location, ruleID, fmt.Sprintf("evidence[%d].%s", index, field), mapping[field], maxParserMessageBytes); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAssuranceBounds(src policy.PolicySource, location, ruleID string, raw interface{}) error {
	if err := validateListLength(src, location, ruleID, "assurance", raw, maxParserListItems); err != nil {
		return err
	}
	gates, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	for gateIndex, rawGate := range gates {
		gate, ok := rawGate.(map[string]interface{})
		if !ok {
			continue
		}
		gateLocation := fmt.Sprintf("%s rule %q field assurance[%d]", sourceLocation(src), ruleID, gateIndex)
		for _, field := range []string{"id", "type", "package_manager", "command_policy", "proof_file"} {
			if err := validateTextValue(gateLocation, field, gate[field], maxParserMessageBytes); err != nil {
				return err
			}
		}
		for _, field := range []string{"applicable_if", "scan_paths", "exclude_paths", "allowed_root_entries", "required_root_entries", "forbidden_root_entries", "reserved_dirs", "allowed_extensions", "manifest_paths", "manifest_markers", "dependency_sections", "allowed_version_prefixes", "site_patterns", "guard_markers"} {
			if err := validateStringListLimitAt(gateLocation, field, gate[field], maxParserPatternBytes); err != nil {
				return err
			}
		}
		if err := validateStringListLimitAt(gateLocation, "commands", gate["commands"], maxParserCommandBytes); err != nil {
			return err
		}
		if exemptions, ok := gate["exemptions"].([]interface{}); ok {
			if len(exemptions) > maxParserListItems {
				return parserLimitError(gateLocation, "exemptions", len(exemptions), maxParserListItems, "items")
			}
			for exemptionIndex, rawExemption := range exemptions {
				exemption, ok := rawExemption.(map[string]interface{})
				if !ok {
					continue
				}
				for _, field := range []string{"path", "reason"} {
					if err := validateTextValue(fmt.Sprintf("%s exemptions[%d]", gateLocation, exemptionIndex), field, exemption[field], maxParserMessageBytes); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func validateScalarLimit(src policy.PolicySource, location, ruleID, field string, raw interface{}, maximum int) error {
	return validateTextValue(fmt.Sprintf("%s rule %q", sourceLocation(src), ruleID), field, raw, maximum)
}

func validateTextValue(context, field string, raw interface{}, maximum int) error {
	value, ok := raw.(string)
	if !ok {
		return nil
	}
	if len(value) > maximum {
		return parserLimitError(context+" field "+field, "string bytes", len(value), maximum, "bytes")
	}
	return nil
}

func validateStringListLimit(src policy.PolicySource, location, ruleID, field string, raw interface{}, itemMaximum int) error {
	return validateStringListLimitAt(fmt.Sprintf("%s rule %q", sourceLocation(src), ruleID), field, raw, itemMaximum)
}

func validateStringListLimitAt(context, field string, raw interface{}, itemMaximum int) error {
	values, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	if len(values) > maxParserListItems {
		return parserLimitError(fmt.Sprintf("%s field %s", context, field), "items", len(values), maxParserListItems, "items")
	}
	for index, value := range values {
		text, ok := value.(string)
		if !ok || len(text) <= itemMaximum {
			continue
		}
		return parserLimitError(fmt.Sprintf("%s field %s[%d]", context, field, index), "string bytes", len(text), itemMaximum, "bytes")
	}
	return nil
}

func validateScalarOrStringListLimit(src policy.PolicySource, location, ruleID, field string, raw interface{}, maximum int) error {
	if text, ok := raw.(string); ok {
		return validateScalarLimit(src, location, ruleID, field, text, maximum)
	}
	return validateStringListLimitAt(fmt.Sprintf("%s rule %q", sourceLocation(src), ruleID), field, raw, maximum)
}

func validateListLength(src policy.PolicySource, location, ruleID, field string, raw interface{}, maximum int) error {
	values, ok := raw.([]interface{})
	if !ok || len(values) <= maximum {
		return nil
	}
	return parserLimitError(fmt.Sprintf("%s rule %q field %s", sourceLocation(src), ruleID, field), "items", len(values), maximum, "items")
}

func sourceLocation(src policy.PolicySource) string {
	if src.BlockID == "" {
		return src.Path
	}
	return src.Path + " block " + src.BlockID
}

func parserLimitError(context, field string, actual, maximum int, unit string) error {
	return &rerrors.RuleValidationError{Message: fmt.Sprintf("%s %s actual=%d %s exceeds maximum=%d %s", context, field, actual, unit, maximum, unit)}
}
