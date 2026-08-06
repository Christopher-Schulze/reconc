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

func validateDocumentFields(src policy.PolicySource, doc map[string]interface{}) error {
	rootFields := fieldSet("default_mode", "rules", "scopes")
	if src.Kind == policy.SourceCompilerConfig {
		addFields(rootFields, "extends", "include", "task_lifecycle", "mcp")
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
	allowed := fieldSet("kind", "optional")
	switch policy.Kind(strings.TrimSpace(stringValue(check["kind"]))) {
	case policy.KindRequireFreshFile:
		addFields(allowed, "path", "max_age_hours")
	case policy.KindRequireEvidence:
		addFields(allowed, "file", "must_exist", "must_contain", "must_not_contain", "max_line_count")
	case policy.KindRequireClaim:
		addFields(allowed, "claims")
	case policy.KindRequireCommand, policy.KindRequireCommandSuccess, policy.KindForbidCommand:
		addFields(allowed, "commands", "command_match")
	case policy.KindDenyWrite:
		addFields(allowed, "paths")
	case policy.KindRequireScript:
		addFields(allowed, "script", "args", "timeout_sec", "cache_inputs")
	default:
		// The existing kind validator owns unknown and unsupported kinds. Use
		// the full known field union so its more precise diagnostic survives.
		addFields(allowed, "path", "max_age_hours", "file", "must_exist", "must_contain", "must_not_contain", "max_line_count", "claims", "commands", "paths", "script", "args", "timeout_sec", "cache_inputs")
	}
	return rejectUnknownFields(check, allowed, context)
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

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}
