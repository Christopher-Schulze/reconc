package parser

import (
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
	"reconc.dev/reconc/internal/action"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
)

func parseActionPolicy(source policy.PolicySource) (*action.Plan, bool, error) {
	root, err := decodeActionDocument(source.Content, source.Path)
	if err != nil {
		return nil, false, err
	}
	rootFields, err := actionMapping(root, source.Path, nil)
	if err != nil {
		return nil, false, err
	}
	actionsNode, present := rootFields["actions"]
	if !present {
		return nil, false, nil
	}
	fields, err := actionMapping(actionsNode, source.Path+" actions", actionFieldSet("tools", "rules", "defaults"))
	if err != nil {
		return nil, true, err
	}
	plan := &action.Plan{FormatVersion: action.PlanFormatVersion}
	if node, ok := fields["tools"]; ok {
		plan.Tools, err = parseActionTools(node, source.Path)
		if err != nil {
			return nil, true, err
		}
	}
	if node, ok := fields["rules"]; ok {
		plan.Rules, err = parseActionRules(node, source.Path)
		if err != nil {
			return nil, true, err
		}
	}
	if node, ok := fields["defaults"]; ok {
		plan.Defaults, err = parseActionDefaults(node, source.Path)
		if err != nil {
			return nil, true, err
		}
	}
	return plan, true, nil
}

func parseActionTools(node *yaml.Node, sourcePath string) ([]action.Tool, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, actionError("actions.tools must be a list in " + sourcePath)
	}
	tools := make([]action.Tool, 0, len(node.Content))
	for index, item := range node.Content {
		context := fmt.Sprintf("%s actions.tools[%d]", sourcePath, index)
		fields, err := actionMapping(item, context, actionFieldSet("id", "transport", "platform", "server_label", "server_fingerprint", "tool", "effect"))
		if err != nil {
			return nil, err
		}
		if err := validateOptionalActionStrings(fields, context, "platform", "server_label", "server_fingerprint"); err != nil {
			return nil, err
		}
		id, err := requiredActionString(fields, "id", context)
		if err != nil {
			return nil, err
		}
		transport, err := requiredActionString(fields, "transport", context)
		if err != nil {
			return nil, err
		}
		toolName, err := requiredActionString(fields, "tool", context)
		if err != nil {
			return nil, err
		}
		effectNode, ok := fields["effect"]
		if !ok {
			return nil, actionError(context + ".effect is required")
		}
		effect, err := parseActionEffect(effectNode, context)
		if err != nil {
			return nil, err
		}
		tools = append(tools, action.Tool{
			ID: id, Transport: action.Transport(transport),
			Platform:          action.Platform(optionalActionString(fields, "platform")),
			ServerLabel:       optionalActionString(fields, "server_label"),
			ServerFingerprint: optionalActionString(fields, "server_fingerprint"),
			Tool:              toolName, Effect: effect, Origin: action.OriginActions,
			SourceIdentity: sourcePath,
		})
	}
	return tools, nil
}

func parseActionEffect(node *yaml.Node, context string) (action.Effect, error) {
	fields, err := actionMapping(node, context+".effect", actionFieldSet("kind", "path_fields", "command_field"))
	if err != nil {
		return action.Effect{}, err
	}
	if err := validateOptionalActionStrings(fields, context+".effect", "command_field"); err != nil {
		return action.Effect{}, err
	}
	kind, err := requiredActionString(fields, "kind", context+".effect")
	if err != nil {
		return action.Effect{}, err
	}
	paths, err := optionalActionStrings(fields, "path_fields", context+".effect")
	if err != nil {
		return action.Effect{}, err
	}
	return action.Effect{Kind: action.EffectKind(kind), PathFields: paths, CommandField: optionalActionString(fields, "command_field")}, nil
}

func parseActionRules(node *yaml.Node, sourcePath string) ([]action.Rule, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, actionError("actions.rules must be a list in " + sourcePath)
	}
	rules := make([]action.Rule, 0, len(node.Content))
	for index, item := range node.Content {
		context := fmt.Sprintf("%s actions.rules[%d]", sourcePath, index)
		fields, err := actionMapping(item, context, actionFieldSet("id", "selector", "when", "decision", "on_indeterminate", "cache", "message"))
		if err != nil {
			return nil, err
		}
		if err := validateOptionalActionStrings(fields, context, "on_indeterminate", "cache", "message"); err != nil {
			return nil, err
		}
		id, err := requiredActionString(fields, "id", context)
		if err != nil {
			return nil, err
		}
		decision, err := requiredActionString(fields, "decision", context)
		if err != nil {
			return nil, err
		}
		selector := action.Selector{}
		if selected, ok := fields["selector"]; ok {
			selector, err = parseActionSelector(selected, context)
			if err != nil {
				return nil, err
			}
		}
		var when *action.Condition
		if condition, ok := fields["when"]; ok {
			when, err = parseActionCondition(condition, context+".when", 1)
			if err != nil {
				return nil, err
			}
		}
		rules = append(rules, action.Rule{
			ID: id, Selector: selector, When: when,
			Decision:        action.Decision(decision),
			OnIndeterminate: action.Decision(optionalActionString(fields, "on_indeterminate")),
			Cache:           action.CachePolicy(optionalActionString(fields, "cache")),
			Message:         optionalActionString(fields, "message"), SourceIdentity: sourcePath,
		})
	}
	return rules, nil
}

func parseActionSelector(node *yaml.Node, context string) (action.Selector, error) {
	fields, err := actionMapping(node, context+".selector", actionFieldSet(
		"tool_ids", "transports", "platforms", "server_labels",
		"server_fingerprints", "tools", "tool_contract_digests", "phases",
	))
	if err != nil {
		return action.Selector{}, err
	}
	stringsFor := func(field string) ([]string, error) {
		return optionalActionStrings(fields, field, context+".selector")
	}
	toolIDs, err := stringsFor("tool_ids")
	if err != nil {
		return action.Selector{}, err
	}
	transports, err := stringsFor("transports")
	if err != nil {
		return action.Selector{}, err
	}
	platforms, err := stringsFor("platforms")
	if err != nil {
		return action.Selector{}, err
	}
	serverLabels, err := stringsFor("server_labels")
	if err != nil {
		return action.Selector{}, err
	}
	serverFingerprints, err := stringsFor("server_fingerprints")
	if err != nil {
		return action.Selector{}, err
	}
	tools, err := stringsFor("tools")
	if err != nil {
		return action.Selector{}, err
	}
	toolDigests, err := stringsFor("tool_contract_digests")
	if err != nil {
		return action.Selector{}, err
	}
	phases, err := stringsFor("phases")
	if err != nil {
		return action.Selector{}, err
	}
	return action.Selector{
		ToolIDs: toolIDs, Transports: actionTransports(transports),
		Platforms: actionPlatforms(platforms), ServerLabels: serverLabels,
		ServerFingerprints: serverFingerprints, Tools: tools,
		ToolContractDigests: toolDigests, Phases: actionPhases(phases),
	}, nil
}

func parseActionCondition(node *yaml.Node, context string, depth int) (*action.Condition, error) {
	if depth > action.MaxConditionDepth {
		return nil, actionError(fmt.Sprintf("%s exceeds depth %d", context, action.MaxConditionDepth))
	}
	fields, err := actionMapping(node, context, actionFieldSet("all", "any", "not", "predicate"))
	if err != nil {
		return nil, err
	}
	if len(fields) != 1 {
		return nil, actionError(context + " must contain exactly one of all, any, not, or predicate")
	}
	condition := &action.Condition{}
	if child, ok := fields["not"]; ok {
		condition.Not, err = parseActionCondition(child, context+".not", depth+1)
		return condition, err
	}
	if predicate, ok := fields["predicate"]; ok {
		condition.Predicate, err = parseActionPredicate(predicate, context+".predicate")
		return condition, err
	}
	field := "all"
	children := fields[field]
	if children == nil {
		field = "any"
		children = fields[field]
	}
	if children.Kind != yaml.SequenceNode {
		return nil, actionError(context + "." + field + " must be a list")
	}
	parsed := make([]action.Condition, 0, len(children.Content))
	for index, child := range children.Content {
		item, childErr := parseActionCondition(child, fmt.Sprintf("%s.%s[%d]", context, field, index), depth+1)
		if childErr != nil {
			return nil, childErr
		}
		parsed = append(parsed, *item)
	}
	if field == "all" {
		condition.All = parsed
	} else {
		condition.Any = parsed
	}
	return condition, nil
}

func parseActionPredicate(node *yaml.Node, context string) (*action.Predicate, error) {
	fields, err := actionMapping(node, context, actionFieldSet("source", "pointer", "minimum_provenance", "op", "value"))
	if err != nil {
		return nil, err
	}
	if err := validateOptionalActionStrings(fields, context, "minimum_provenance"); err != nil {
		return nil, err
	}
	source, err := requiredActionString(fields, "source", context)
	if err != nil {
		return nil, err
	}
	pointer, err := requiredActionStringAllowEmpty(fields, "pointer", context)
	if err != nil {
		return nil, err
	}
	op, err := requiredActionString(fields, "op", context)
	if err != nil {
		return nil, err
	}
	predicate := &action.Predicate{
		Source: action.ValueSource(source), Pointer: pointer,
		MinimumProvenance: action.Provenance(optionalActionString(fields, "minimum_provenance")),
		Op:                action.Operator(op),
	}
	if operand, ok := fields["value"]; ok {
		state := actionValueState{}
		value, err := state.parse(operand, context+".value", 0)
		if err != nil {
			return nil, err
		}
		predicate.Value = &value
	}
	return predicate, nil
}

func parseActionDefaults(node *yaml.Node, sourcePath string) (action.Defaults, error) {
	fields, err := actionMapping(node, sourcePath+" actions.defaults", actionFieldSet(
		"declared_tool", "gateway_unmatched", "host_unmatched", "evaluation_error",
		"post_error", "progress_error", "cache",
	))
	if err != nil {
		return action.Defaults{}, err
	}
	if err := validateOptionalActionStrings(fields, sourcePath+" actions.defaults",
		"declared_tool", "gateway_unmatched", "host_unmatched", "evaluation_error", "post_error", "progress_error", "cache"); err != nil {
		return action.Defaults{}, err
	}
	return action.Defaults{
		DeclaredTool:     action.Decision(optionalActionString(fields, "declared_tool")),
		GatewayUnmatched: action.Decision(optionalActionString(fields, "gateway_unmatched")),
		HostUnmatched:    action.Decision(optionalActionString(fields, "host_unmatched")),
		EvaluationError:  action.Decision(optionalActionString(fields, "evaluation_error")),
		PostError:        action.Decision(optionalActionString(fields, "post_error")),
		ProgressError:    action.Decision(optionalActionString(fields, "progress_error")),
		Cache:            action.CachePolicy(optionalActionString(fields, "cache")),
	}, nil
}

func decodeActionDocument(raw, context string) (*yaml.Node, error) {
	if strings.TrimSpace(raw) == "" {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, actionErrorWithCause("invalid yaml in "+context, err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, actionErrorWithCause("invalid trailing yaml in "+context, err)
		}
		return nil, actionError("expected exactly one document in " + context)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, actionError("expected a YAML mapping in " + context)
	}
	return document.Content[0], nil
}

func actionMapping(node *yaml.Node, context string, allowed map[string]struct{}) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return nil, actionError(context + " must be a mapping")
	}
	out := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "" {
			return nil, actionError(context + " contains a non-string or empty key")
		}
		if _, duplicate := out[key.Value]; duplicate {
			return nil, actionError(fmt.Sprintf("%s contains duplicate key %q", context, key.Value))
		}
		if allowed != nil {
			if _, ok := allowed[key.Value]; !ok {
				return nil, actionError(fmt.Sprintf("unknown field in %s: %s", context, key.Value))
			}
		}
		out[key.Value] = node.Content[index+1]
	}
	return out, nil
}

func requiredActionString(fields map[string]*yaml.Node, field, context string) (string, error) {
	value, err := requiredActionStringAllowEmpty(fields, field, context)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", actionError(context + "." + field + " must be non-empty")
	}
	return value, nil
}

func requiredActionStringAllowEmpty(fields map[string]*yaml.Node, field, context string) (string, error) {
	node, ok := fields[field]
	if !ok {
		return "", actionError(context + "." + field + " is required")
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", actionError(context + "." + field + " must be a string")
	}
	return node.Value, nil
}

func optionalActionString(fields map[string]*yaml.Node, field string) string {
	node, ok := fields[field]
	if !ok || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return ""
	}
	return node.Value
}

func validateOptionalActionStrings(fields map[string]*yaml.Node, context string, names ...string) error {
	for _, name := range names {
		node, present := fields[name]
		if present && (node.Kind != yaml.ScalarNode || node.Tag != "!!str") {
			return actionError(context + "." + name + " must be a string")
		}
	}
	return nil
}

func optionalActionStrings(fields map[string]*yaml.Node, field, context string) ([]string, error) {
	node, ok := fields[field]
	if !ok {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, actionError(context + "." + field + " must be a string list")
	}
	out := make([]string, 0, len(node.Content))
	for index, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, actionError(fmt.Sprintf("%s.%s[%d] must be a string", context, field, index))
		}
		out = append(out, item.Value)
	}
	return out, nil
}

type actionValueState struct{ items int }

func (s *actionValueState) parse(node *yaml.Node, context string, depth int) (action.Value, error) {
	if depth > action.MaxJSONDepth {
		return action.Value{}, actionError(fmt.Sprintf("%s exceeds %d levels", context, action.MaxJSONDepth))
	}
	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			if node.Value != "null" {
				return action.Value{}, actionError(context + " uses ambiguous YAML null syntax")
			}
			return action.Null(), nil
		case "!!bool":
			if node.Value == "true" {
				return action.Boolean(true), nil
			}
			if node.Value == "false" {
				return action.Boolean(false), nil
			}
			return action.Value{}, actionError(context + " uses ambiguous YAML boolean syntax")
		case "!!int", "!!float":
			decimal, err := action.ParseDecimal(node.Value)
			if err != nil {
				return action.Value{}, actionErrorWithCause(context+" contains an ambiguous or invalid number", err)
			}
			return action.Number(decimal), nil
		case "!!str":
			value, err := action.String(node.Value)
			if err != nil {
				return action.Value{}, actionErrorWithCause(context+" contains an invalid string", err)
			}
			return value, nil
		default:
			return action.Value{}, actionError(context + " contains a non-JSON YAML scalar")
		}
	case yaml.SequenceNode:
		values := make([]action.Value, 0, len(node.Content))
		for index, child := range node.Content {
			s.items++
			if s.items > action.MaxJSONItems {
				return action.Value{}, actionError(fmt.Sprintf("%s exceeds %d items", context, action.MaxJSONItems))
			}
			value, err := s.parse(child, fmt.Sprintf("%s[%d]", context, index), depth+1)
			if err != nil {
				return action.Value{}, err
			}
			values = append(values, value)
		}
		value, err := action.Array(values)
		if err != nil {
			return action.Value{}, actionErrorWithCause(context, err)
		}
		return value, nil
	case yaml.MappingNode:
		fields, err := actionMapping(node, context, nil)
		if err != nil {
			return action.Value{}, err
		}
		members := make([]action.Member, 0, len(fields))
		for index := 0; index < len(node.Content); index += 2 {
			name := node.Content[index].Value
			s.items++
			if s.items > action.MaxJSONItems {
				return action.Value{}, actionError(fmt.Sprintf("%s exceeds %d items", context, action.MaxJSONItems))
			}
			value, err := s.parse(fields[name], context+"."+name, depth+1)
			if err != nil {
				return action.Value{}, err
			}
			members = append(members, action.Member{Name: name, Value: value})
		}
		value, err := action.Object(members)
		if err != nil {
			return action.Value{}, actionErrorWithCause(context, err)
		}
		return value, nil
	default:
		return action.Value{}, actionError(context + " contains an alias or unsupported YAML node")
	}
}

func actionTransports(values []string) []action.Transport {
	if values == nil {
		return nil
	}
	out := make([]action.Transport, len(values))
	for index := range values {
		out[index] = action.Transport(values[index])
	}
	return out
}

func actionPlatforms(values []string) []action.Platform {
	if values == nil {
		return nil
	}
	out := make([]action.Platform, len(values))
	for index := range values {
		out[index] = action.Platform(values[index])
	}
	return out
}

func actionPhases(values []string) []action.Phase {
	if values == nil {
		return nil
	}
	out := make([]action.Phase, len(values))
	for index := range values {
		out[index] = action.Phase(values[index])
	}
	return out
}

func actionFieldSet(fields ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		out[field] = struct{}{}
	}
	return out
}

func actionError(message string) error {
	return &rerrors.RuleValidationError{Message: message}
}

func actionErrorWithCause(message string, cause error) error {
	return &rerrors.RuleValidationError{Message: message, Cause: cause}
}
