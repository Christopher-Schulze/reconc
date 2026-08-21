package parser

import (
	"fmt"
	"sort"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
)

var ruleFields = fieldSet(
	"id", "kind", "mode", "message", "paths", "before_paths", "when_paths",
	"commands", "claims", "command_match", "required_files", "evidence", "checks",
	"script", "args", "timeout_sec", "kill_timeout_sec", "cache_inputs", "assurance", "template",
	"deprecated", "deprecated_reason", "deprecated_since", "deprecated_replaced_by",
)

var ruleKindFields = buildRuleKindFields()

var checkKindFields = buildCheckKindFields()

var checkFields = fieldSet(
	"kind", "optional", "path", "max_age_hours", "file", "must_exist", "must_contain",
	"must_not_contain", "max_line_count", "claims", "commands", "command_match",
	"paths", "script", "args", "timeout_sec", "cache_inputs",
)

func buildRuleKindFields() map[policy.Kind]map[string]struct{} {
	common := []string{"id", "kind", "mode", "message", "template", "deprecated", "deprecated_reason", "deprecated_since", "deprecated_replaced_by"}
	definitions := map[policy.Kind][]string{
		policy.KindDenyWrite:             {"paths", "when_paths"},
		policy.KindRequireRead:           {"paths", "before_paths"},
		policy.KindRequireCommand:        {"when_paths", "commands", "command_match"},
		policy.KindRequireCommandSuccess: {"when_paths", "commands", "command_match"},
		policy.KindForbidCommand:         {"commands", "when_paths", "command_match"},
		policy.KindCoupleChange:          {"paths", "when_paths"},
		policy.KindRequireClaim:          {"when_paths", "claims"},
		policy.KindRequireFreshFile:      {"when_paths", "required_files"},
		policy.KindRequireEvidence:       {"when_paths", "evidence"},
		policy.KindAllOf:                 {"when_paths", "checks"},
		policy.KindAnyOf:                 {"when_paths", "checks"},
		policy.KindNot:                   {"when_paths", "checks"},
		policy.KindRequireScript:         {"when_paths", "script", "args", "timeout_sec", "kill_timeout_sec", "cache_inputs"},
		policy.KindRequireAssurance:      {"when_paths", "assurance"},
	}
	fields := make(map[policy.Kind]map[string]struct{}, len(definitions))
	for kind, specific := range definitions {
		fields[kind] = fieldSet(append(common, specific...)...)
	}
	return fields
}

// RuleKindFields returns the complete authoring-field allowlist for kind.
// The returned slice is sorted and owned by the caller. Runtime lockfile
// validation uses this same matrix so parser and runtime cannot silently
// disagree about fields that a rule may carry.
func RuleKindFields(kind policy.Kind) []string {
	allowed, ok := ruleKindFields[kind]
	if !ok {
		return nil
	}
	fields := make([]string, 0, len(allowed))
	for field := range allowed {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// RuleKindFieldAllowed reports whether field is valid in an authoring rule of
// kind. It is intentionally based on the same immutable matrix as the parser.
func RuleKindFieldAllowed(kind policy.Kind, field string) bool {
	allowed, ok := ruleKindFields[kind]
	if !ok {
		return false
	}
	_, ok = allowed[field]
	return ok
}

func buildCheckKindFields() map[policy.Kind]map[string]struct{} {
	common := []string{"kind", "optional"}
	definitions := map[policy.Kind][]string{
		policy.KindRequireFreshFile:      {"path", "max_age_hours"},
		policy.KindRequireEvidence:       {"file", "must_exist", "must_contain", "must_not_contain", "max_line_count"},
		policy.KindRequireClaim:          {"claims"},
		policy.KindRequireCommand:        {"commands", "command_match"},
		policy.KindRequireCommandSuccess: {"commands", "command_match"},
		policy.KindForbidCommand:         {"commands", "command_match"},
		policy.KindDenyWrite:             {"paths"},
		policy.KindRequireScript:         {"script", "args", "timeout_sec", "cache_inputs"},
	}
	fields := make(map[policy.Kind]map[string]struct{}, len(definitions))
	for kind, specific := range definitions {
		fields[kind] = fieldSet(append(common, specific...)...)
	}
	return fields
}

func validateCheckKindFields(check map[string]interface{}, kind policy.Kind, ruleID, sourcePath string, index int) error {
	allowed, ok := checkKindFields[kind]
	if !ok {
		return nil
	}
	unsupported := make([]string, 0)
	for field := range check {
		if _, allowed := allowed[field]; !allowed {
			unsupported = append(unsupported, field)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return &rerrors.RuleValidationError{Message: fmt.Sprintf("rule '%s' check[%d] (kind %s) in %s contains field(s) not valid for its kind: %s", ruleID, index, kind, sourcePath, strings.Join(unsupported, ", "))}
}

func validateRuleKindFields(rule map[string]interface{}, kind policy.Kind, id, sourcePath string) error {
	allowed, ok := ruleKindFields[kind]
	if !ok {
		return nil
	}
	unsupported := make([]string, 0)
	for field := range rule {
		if _, allowed := allowed[field]; !allowed {
			unsupported = append(unsupported, field)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return &rerrors.RuleValidationError{Message: fmt.Sprintf("rule '%s' (kind %s) in %s contains field(s) not valid for its kind: %s", id, kind, sourcePath, strings.Join(unsupported, ", "))}
}

func validateDocumentFields(src policy.PolicySource, doc map[string]interface{}) error {
	rootFields := fieldSet("default_mode", "rules", "scopes")
	if src.Kind == policy.SourceCompilerConfig {
		addFields(rootFields, "extends", "include", "task_lifecycle", "mcp", "actions")
	}
	if impactCandidateSource(src) {
		addFields(rootFields, "actions")
	}
	if src.Kind == policy.SourcePreset {
		addFields(rootFields, "pack")
	}
	if err := rejectUnknownFields(doc, rootFields, src.Path); err != nil {
		return err
	}
	if raw, ok := doc["rules"].([]interface{}); ok {
		for i, item := range raw {
			if mapping, ok := item.(map[string]interface{}); ok {
				if err := validateRuleFields(mapping, fmt.Sprintf("%s rules[%d]", src.Path, i)); err != nil {
					return err
				}
			}
		}
	}
	if raw, ok := doc["scopes"].([]interface{}); ok {
		for i, item := range raw {
			mapping, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			context := fmt.Sprintf("%s scopes[%d]", src.Path, i)
			if err := rejectUnknownFields(mapping, fieldSet("id", "paths", "rules"), context); err != nil {
				return err
			}
			if rules, ok := mapping["rules"].([]interface{}); ok {
				for j, item := range rules {
					if rule, ok := item.(map[string]interface{}); ok {
						if err := validateRuleFields(rule, fmt.Sprintf("%s rules[%d]", context, j)); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	if task, ok := doc["task_lifecycle"].(map[string]interface{}); ok {
		if err := rejectUnknownFields(task, fieldSet("enabled", "profile", "overview_path", "detail_dir", "done_dir", "done_visible", "completion"), src.Path+" task_lifecycle"); err != nil {
			return err
		}
		if completion, ok := task["completion"].(map[string]interface{}); ok {
			if err := rejectUnknownFields(completion, fieldSet("required_sections", "required_evidence_fields", "require_committed"), src.Path+" task_lifecycle.completion"); err != nil {
				return err
			}
		}
	}
	if mcp, ok := doc["mcp"].(map[string]interface{}); ok {
		if err := rejectUnknownFields(mcp, fieldSet("unclassified", "tools"), src.Path+" mcp"); err != nil {
			return err
		}
		if tools, ok := mcp["tools"].([]interface{}); ok {
			for index, rawTool := range tools {
				if tool, ok := rawTool.(map[string]interface{}); ok {
					if err := rejectUnknownFields(tool, fieldSet("platform", "server_fingerprint", "tool", "effect", "path_fields", "command_field"), fmt.Sprintf("%s mcp.tools[%d]", src.Path, index)); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func validateRuleFields(rule map[string]interface{}, context string) error {
	if err := rejectUnknownFields(rule, ruleFields, context); err != nil {
		return err
	}
	if files, ok := rule["required_files"].([]interface{}); ok {
		for i, item := range files {
			if mapping, ok := item.(map[string]interface{}); ok {
				if err := rejectUnknownFields(mapping, fieldSet("path", "max_age_hours", "optional"), fmt.Sprintf("%s required_files[%d]", context, i)); err != nil {
					return err
				}
			}
		}
	}
	if evidence, ok := rule["evidence"].([]interface{}); ok {
		for i, item := range evidence {
			if mapping, ok := item.(map[string]interface{}); ok {
				if err := rejectUnknownFields(mapping, fieldSet("file", "must_exist", "must_contain", "must_not_contain", "max_line_count", "optional"), fmt.Sprintf("%s evidence[%d]", context, i)); err != nil {
					return err
				}
			}
		}
	}
	if checks, ok := rule["checks"].([]interface{}); ok {
		for i, item := range checks {
			if mapping, ok := item.(map[string]interface{}); ok {
				if err := validateCheckFields(mapping, fmt.Sprintf("%s checks[%d]", context, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateCheckFields(check map[string]interface{}, context string) error {
	// Keep unknown-key diagnostics at document validation time. Known-but-
	// unsupported fields are checked after parseCheck has the rule kind and can
	// therefore report the precise rule ID, kind, source, and check index.
	return rejectUnknownFields(check, checkFields, context)
}

func rejectUnknownFields(mapping map[string]interface{}, allowed map[string]struct{}, context string) error {
	unknown := make([]string, 0)
	for field := range mapping {
		if _, ok := allowed[field]; !ok {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return &rerrors.RuleValidationError{Message: fmt.Sprintf("unknown field(s) in %s: %s", context, strings.Join(unknown, ", "))}
}

func fieldSet(fields ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(fields))
	addFields(out, fields...)
	return out
}

func addFields(set map[string]struct{}, fields ...string) {
	for _, field := range fields {
		set[field] = struct{}{}
	}
}
