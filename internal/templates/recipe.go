package templates

import (
	"fmt"
	"strings"
)

// RecipeMetadata documents the evidence contract of a built-in or user
// template. It is intentionally descriptive: enforcement remains owned by
// the expanded policy rule and its existing runtime primitive.
type RecipeMetadata struct {
	InputPaths         []string        `json:"input_paths"`
	CWD                string          `json:"cwd"`
	CommandIdentity    string          `json:"command_identity"`
	EvidenceIdentity   string          `json:"evidence_identity"`
	Applicability      string          `json:"applicability"`
	Limitations        []string        `json:"limitations"`
	Remediation        string          `json:"remediation"`
	RequiredRuleFields []string        `json:"required_rule_fields"`
	Examples           []RecipeExample `json:"examples"`
}

// RecipeExample is an executable, project-owned command example shown with a
// recipe. Expect is the Reconc disposition the command is intended to produce.
type RecipeExample struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	CWD     string `json:"cwd"`
	Expect  string `json:"expect"`
}

// recipeFromBody decodes the optional template-only `recipe` mapping. Keeping
// this metadata outside policy.Rule lets templates describe a contract without
// inventing a second runtime rule kind or leaking unknown fields into parsing.
func recipeFromBody(body map[string]interface{}, contextPath string) (*RecipeMetadata, error) {
	raw, ok := body["recipe"]
	if !ok || raw == nil {
		return nil, nil
	}
	mapping, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("template %s recipe must be a mapping", contextPath)
	}
	allowed := map[string]bool{
		"input_paths": true, "cwd": true, "command_identity": true,
		"evidence_identity": true, "applicability": true, "limitations": true,
		"remediation": true, "required_rule_fields": true, "examples": true,
	}
	for key := range mapping {
		if !allowed[key] {
			return nil, fmt.Errorf("template %s recipe field %q is not supported", contextPath, key)
		}
	}
	metadata := &RecipeMetadata{}
	var err error
	if metadata.InputPaths, err = recipeStrings(mapping, "input_paths", contextPath, true); err != nil {
		return nil, err
	}
	metadata.CWD, err = recipeString(mapping, "cwd", contextPath, true)
	if err != nil {
		return nil, err
	}
	if metadata.CWD == "" {
		metadata.CWD = "."
	}
	if recipePathEscapes(metadata.CWD) {
		return nil, fmt.Errorf("template %s recipe cwd must stay repository-relative", contextPath)
	}
	if metadata.CommandIdentity, err = recipeString(mapping, "command_identity", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.EvidenceIdentity, err = recipeString(mapping, "evidence_identity", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.Applicability, err = recipeString(mapping, "applicability", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.Limitations, err = recipeStrings(mapping, "limitations", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.Remediation, err = recipeString(mapping, "remediation", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.RequiredRuleFields, err = recipeStrings(mapping, "required_rule_fields", contextPath, true); err != nil {
		return nil, err
	}
	for index, field := range metadata.RequiredRuleFields {
		if field != "script" && field != "when_paths" && field != "args" && field != "cache_inputs" {
			return nil, fmt.Errorf("template %s recipe required_rule_fields[%d] names unsupported field %q", contextPath, index, field)
		}
	}
	metadata.Examples, err = recipeExamples(mapping, contextPath)
	if err != nil {
		return nil, err
	}
	return metadata, nil
}

// ValidateRequiredRuleFields enforces the rule-field contract declared by a
// recipe after template defaults and user overrides have been merged. The
// ordinary parser still owns the detailed type and value validation; this
// boundary guarantees that a recipe cannot silently become an unconfigured
// generic require_script rule.
func ValidateRequiredRuleFields(tmpl *Template, merged map[string]interface{}, contextPath string) error {
	if tmpl == nil || tmpl.Recipe == nil {
		return nil
	}
	for _, field := range tmpl.Recipe.RequiredRuleFields {
		value, ok := merged[field]
		if !ok || value == nil {
			return fmt.Errorf("template %s requires field '%s'", contextPath, field)
		}
		switch field {
		case "script":
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("template %s requires field '%s' to be a non-empty string", contextPath, field)
			}
		case "when_paths", "args", "cache_inputs":
			if !nonEmptyStringList(value) {
				return fmt.Errorf("template %s requires field '%s' to be a non-empty list of strings", contextPath, field)
			}
		}
	}
	return nil
}

func nonEmptyStringList(value interface{}) bool {
	switch values := value.(type) {
	case []interface{}:
		if len(values) == 0 {
			return false
		}
		for _, value := range values {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return false
			}
		}
		return true
	case []string:
		if len(values) == 0 {
			return false
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func recipeString(mapping map[string]interface{}, key, contextPath string, required bool) (string, error) {
	raw, ok := mapping[key]
	if !ok || raw == nil {
		if required {
			return "", fmt.Errorf("template %s recipe field %q is required", contextPath, key)
		}
		return "", nil
	}
	value, ok := raw.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("template %s recipe field %q must be a non-empty string", contextPath, key)
	}
	return strings.TrimSpace(value), nil
}

func recipeStrings(mapping map[string]interface{}, key, contextPath string, required bool) ([]string, error) {
	raw, ok := mapping[key]
	if !ok || raw == nil {
		if required {
			return nil, fmt.Errorf("template %s recipe field %q is required", contextPath, key)
		}
		return nil, nil
	}
	values, ok := raw.([]interface{})
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("template %s recipe field %q must be a non-empty list of strings", contextPath, key)
	}
	out := make([]string, len(values))
	for index, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("template %s recipe field %q[%d] must be a non-empty string", contextPath, key, index)
		}
		value = strings.TrimSpace(value)
		if key == "input_paths" && recipePathEscapes(value) {
			return nil, fmt.Errorf("template %s recipe input path %q must stay repository-relative", contextPath, value)
		}
		out[index] = value
	}
	return out, nil
}

func recipePathEscapes(value string) bool {
	normalized := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") {
		return true
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func recipeExamples(mapping map[string]interface{}, contextPath string) ([]RecipeExample, error) {
	raw, ok := mapping["examples"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("template %s recipe field %q is required", contextPath, "examples")
	}
	values, ok := raw.([]interface{})
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("template %s recipe field %q must be a non-empty list", contextPath, "examples")
	}
	out := make([]RecipeExample, len(values))
	for index, rawValue := range values {
		entry, ok := rawValue.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("template %s recipe examples[%d] must be a mapping", contextPath, index)
		}
		for key := range entry {
			if key != "name" && key != "command" && key != "cwd" && key != "expect" {
				return nil, fmt.Errorf("template %s recipe examples[%d] field %q is not supported", contextPath, index, key)
			}
		}
		name, err := recipeString(entry, "name", contextPath, true)
		if err != nil {
			return nil, err
		}
		command, err := recipeString(entry, "command", contextPath, true)
		if err != nil {
			return nil, err
		}
		cwd, err := recipeString(entry, "cwd", contextPath, false)
		if err != nil {
			return nil, err
		}
		if cwd == "" {
			cwd = "."
		}
		if recipePathEscapes(cwd) {
			return nil, fmt.Errorf("template %s recipe examples[%d].cwd must stay repository-relative", contextPath, index)
		}
		expect, err := recipeString(entry, "expect", contextPath, true)
		if err != nil {
			return nil, err
		}
		if expect != "pass" && expect != "block" {
			return nil, fmt.Errorf("template %s recipe examples[%d].expect must be pass or block", contextPath, index)
		}
		out[index] = RecipeExample{Name: name, Command: command, CWD: cwd, Expect: expect}
	}
	return out, nil
}
