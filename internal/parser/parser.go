// Package parser validates and normalizes raw rule documents from a
// SourceBundle into a strongly-typed ParsedPolicy ready for the
// compiler to serialize.
//
// Validation is strict by design: any unknown rule kind, missing
// required field, or duplicate rule id raises *RuleValidationError
// instead of being silently dropped.
package parser

import (
	"strings"

	"gopkg.in/yaml.v3"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/templates"
)

// requiredFieldsByKind specifies which slice fields must be populated per rule
// kind.
//
// Phase 4A (W22) introduces require_fresh_file and require_evidence
// which use OBJECT-list fields (required_files, evidence) rather than
// string-list fields. Their requirements are validated in
// validateEvidenceFields below.
var requiredFieldsByKind = map[policy.Kind][]string{
	policy.KindDenyWrite:             {"paths"},
	policy.KindRequireRead:           {"paths", "before_paths"},
	policy.KindRequireCommand:        {"commands", "when_paths"},
	policy.KindRequireCommandSuccess: {"commands", "when_paths"},
	policy.KindForbidCommand:         {"commands"},
	policy.KindCoupleChange:          {"paths", "when_paths"},
	policy.KindRequireClaim:          {"claims", "when_paths"},
}

// DefaultMode is the fallback default_mode when no compiler config
// declares one.
const DefaultMode = policy.ModeWarn

// ParsedPolicy is the validated, deduplicated result of parsing a
// SourceBundle. Order of rules matches their order in the source
// precedence chain so the compiler digest is stable.
type ParsedPolicy struct {
	DefaultMode policy.Mode   `json:"default_mode"`
	Rules       []policy.Rule `json:"rules"`
}

// ParseRuleDocuments walks the source bundle, validates every rule
// document found in non-context sources, and merges them into a
// single ParsedPolicy.
//
// Sources of kind claude_md / agents_md / start_md are skipped (they
// carry prose, not rule documents). Their inline_block siblings are
// the rule-bearing sources from those files.
//
// Returns *RuleValidationError on the first validation failure
// (with the offending source path / rule id in the message) so users
// can fix one problem at a time.
func ParseRuleDocuments(bundle *ingest.SourceBundle) (*ParsedPolicy, error) {
	if bundle == nil {
		return nil, &rerrors.RuleValidationError{Message: "bundle is nil"}
	}

	defaultMode := DefaultMode
	rules := []policy.Rule{}
	seen := map[string]string{} // rule id -> source path of first sighting

	for _, src := range bundle.Sources {
		// Skip context-only sources; their fenced blocks land as
		// separate inline_block sources we DO process.
		switch src.Kind {
		case policy.SourceClaudeMD, policy.SourceAgentsMD, policy.SourceStartMD:
			continue
		}

		doc, err := decodeYAMLMapping(src.Content, src.Path)
		if err != nil {
			return nil, err
		}

		// Capture default_mode from the compiler config (only).
		if src.Kind == policy.SourceCompilerConfig {
			if dmRaw, ok := doc["default_mode"]; ok && dmRaw != nil {
				dmStr, isStr := dmRaw.(string)
				if !isStr {
					return nil, &rerrors.RuleValidationError{
						Message: "default_mode must be a string in " + src.Path,
					}
				}
				dm := policy.Mode(strings.TrimSpace(dmStr))
				if !dm.Valid() {
					return nil, &rerrors.RuleValidationError{
						Message: "invalid default_mode: " + dmStr,
					}
				}
				defaultMode = dm
			}
		}

		coerced, err := coerceRules(src, doc)
		if err != nil {
			return nil, err
		}
		for _, r := range coerced {
			if firstPath, dup := seen[r.ID]; dup {
				return nil, &rerrors.RuleValidationError{
					Message: "duplicate rule id: " + r.ID + " (first defined in " + firstPath + ", redefined in " + src.Path + ")",
				}
			}
			seen[r.ID] = src.Path
			rules = append(rules, r)
		}

		// W17: scoped rules. Each scope wraps a list of rules with a
		// path filter. We expand them into normal rules carrying
		// ScopePaths/ScopeID so the runtime can pre-filter by scope
		// without a new evaluator code path.
		scoped, err := coerceScopes(src, doc)
		if err != nil {
			return nil, err
		}
		for _, r := range scoped {
			if firstPath, dup := seen[r.ID]; dup {
				return nil, &rerrors.RuleValidationError{
					Message: "duplicate rule id: " + r.ID + " (first defined in " + firstPath + ", redefined in scope of " + src.Path + ")",
				}
			}
			seen[r.ID] = src.Path
			rules = append(rules, r)
		}
	}

	return &ParsedPolicy{
		DefaultMode: defaultMode,
		Rules:       rules,
	}, nil
}

// coerceScopes pulls the optional `scopes:` slice out of a parsed YAML
// mapping. Each scope is a {paths: [...], id?: string, rules: [...]}
// block; rules within get expanded into top-level rules carrying the
// scope's paths as ScopePaths and (optional) id as ScopeID.
//
// Scopes are the W17 monorepo-support feature: lets one .reconc.yml
// hold per-subtree rules without users writing per-rule path filters.
func coerceScopes(src policy.PolicySource, doc map[string]interface{}) ([]policy.Rule, error) {
	rawScopes, ok := doc["scopes"]
	if !ok || rawScopes == nil {
		return nil, nil
	}
	list, ok := rawScopes.([]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{
			Message: "scopes must be a list in " + src.Path,
		}
	}
	out := []policy.Rule{}
	for i, item := range list {
		mapping, ok := item.(map[string]interface{})
		if !ok {
			return nil, &rerrors.RuleValidationError{
				Message: "each scope must be a YAML mapping in " + src.Path + " (scope #" + itoa(i) + ")",
			}
		}
		paths, err := optionalStringList(mapping, "paths", "scope#"+itoa(i))
		if err != nil {
			return nil, err
		}
		if len(paths) == 0 {
			return nil, &rerrors.RuleValidationError{
				Message: "scope #" + itoa(i) + " in " + src.Path + " requires non-empty 'paths'",
			}
		}
		scopeID, err := optionalString(mapping, "id", "scope#"+itoa(i), "", 0)
		if err != nil {
			return nil, err
		}
		rawScopeRules, ok := mapping["rules"]
		if !ok || rawScopeRules == nil {
			// A scope with no rules is legal -- maybe the user is
			// preparing to add some. Skip it silently.
			continue
		}
		ruleList, ok := rawScopeRules.([]interface{})
		if !ok {
			return nil, &rerrors.RuleValidationError{
				Message: "scope #" + itoa(i) + " 'rules' must be a list in " + src.Path,
			}
		}
		for j, ri := range ruleList {
			rmap, ok := ri.(map[string]interface{})
			if !ok {
				return nil, &rerrors.RuleValidationError{
					Message: "rule #" + itoa(j) + " of scope #" + itoa(i) + " in " + src.Path + " must be a mapping",
				}
			}
			rule, err := validateRuleItem(rmap, src, j)
			if err != nil {
				return nil, err
			}
			rule.ScopePaths = append([]string(nil), paths...)
			rule.ScopeID = scopeID
			out = append(out, rule)
		}
	}
	return out, nil
}

// coerceRules pulls the `rules:` slice out of a parsed YAML mapping
// and validates each entry, returning fully-typed Rule values with
// source provenance attached.
func coerceRules(src policy.PolicySource, doc map[string]interface{}) ([]policy.Rule, error) {
	rawRules, ok := doc["rules"]
	if !ok || rawRules == nil {
		return nil, nil
	}
	list, ok := rawRules.([]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{
			Message: "rules must be a list in " + src.Path,
		}
	}
	out := make([]policy.Rule, 0, len(list))
	for i, item := range list {
		mapping, ok := item.(map[string]interface{})
		if !ok {
			return nil, &rerrors.RuleValidationError{
				Message: "each rule must be a YAML mapping in " + src.Path,
			}
		}
		rule, err := validateRuleItem(mapping, src, i)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

// validateRuleItem checks a single rule mapping against the schema and
// returns the typed Rule on success.
func validateRuleItem(item map[string]interface{}, src policy.PolicySource, index int) (policy.Rule, error) {
	// Template expansion (W18): if the rule references a template, merge
	// the template's fields as defaults before schema validation. User
	// fields always win. The template: field itself is consumed here.
	if tmplName, ok := item["template"].(string); ok && strings.TrimSpace(tmplName) != "" {
		expanded, err := expandTemplate(item, tmplName, src, index)
		if err != nil {
			return policy.Rule{}, err
		}
		item = expanded
	}

	id, err := requiredString(item, "id", src.Path, index)
	if err != nil {
		return policy.Rule{}, err
	}

	kindStr, err := requiredString(item, "kind", src.Path, index)
	if err != nil {
		return policy.Rule{}, &rerrors.RuleValidationError{
			Message: "rule '" + id + "' kind is required",
			Cause:   err,
		}
	}
	kind := policy.Kind(strings.TrimSpace(kindStr))
	if !kind.Valid() {
		return policy.Rule{}, &rerrors.RuleValidationError{
			Message: "unknown rule kind: " + string(kind) + " (rule '" + id + "')",
		}
	}

	mode := policy.Mode("")
	if mRaw, ok := item["mode"]; ok && mRaw != nil {
		mStr, isStr := mRaw.(string)
		if !isStr {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' mode must be a string",
			}
		}
		mode = policy.Mode(strings.TrimSpace(mStr))
		if !mode.Valid() {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "invalid rule mode: " + mStr + " (rule '" + id + "')",
			}
		}
	}

	message, err := requiredString(item, "message", src.Path, index)
	if err != nil {
		return policy.Rule{}, &rerrors.RuleValidationError{
			Message: "rule '" + id + "' message is required",
			Cause:   err,
		}
	}

	paths, err := optionalStringList(item, "paths", id)
	if err != nil {
		return policy.Rule{}, err
	}
	beforePaths, err := optionalStringList(item, "before_paths", id)
	if err != nil {
		return policy.Rule{}, err
	}
	whenPaths, err := optionalStringList(item, "when_paths", id)
	if err != nil {
		return policy.Rule{}, err
	}
	commands, err := optionalStringList(item, "commands", id)
	if err != nil {
		return policy.Rule{}, err
	}
	claims, err := optionalStringList(item, "claims", id)
	if err != nil {
		return policy.Rule{}, err
	}

	// Verify required fields per kind are populated and non-empty.
	fieldValues := map[string][]string{
		"paths":        paths,
		"before_paths": beforePaths,
		"when_paths":   whenPaths,
		"commands":     commands,
		"claims":       claims,
	}
	for _, required := range requiredFieldsByKind[kind] {
		if len(fieldValues[required]) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' requires field '" + required + "'",
			}
		}
	}

	// Phase 4A (W22): parse and validate evidence-shaped rule kinds.
	requiredFiles, err := optionalRequiredFileList(item, "required_files", id)
	if err != nil {
		return policy.Rule{}, err
	}
	evidence, err := optionalEvidenceCheckList(item, "evidence", id)
	if err != nil {
		return policy.Rule{}, err
	}

	if kind == policy.KindRequireFreshFile {
		if len(requiredFiles) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' requires field 'required_files' (non-empty list)",
			}
		}
		if len(whenPaths) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' requires field 'when_paths'",
			}
		}
	}
	if kind == policy.KindRequireEvidence {
		if len(evidence) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' requires field 'evidence' (non-empty list)",
			}
		}
		if len(whenPaths) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' requires field 'when_paths'",
			}
		}
	}

	// Phase 4D (W21+W28): require_script fields.
	script, err := optionalString(item, "script", id, "rule", index)
	if err != nil {
		return policy.Rule{}, err
	}
	args, err := optionalContainList(item, "args", id, "rule", index)
	if err != nil {
		return policy.Rule{}, err
	}
	timeoutSec, err := optionalInt(item, "timeout_sec", id, "rule", index)
	if err != nil {
		return policy.Rule{}, err
	}
	killTimeoutSec, err := optionalInt(item, "kill_timeout_sec", id, "rule", index)
	if err != nil {
		return policy.Rule{}, err
	}
	if kind == policy.KindRequireScript {
		if script == "" {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' requires field 'script' (relative path to executable)",
			}
		}
		if !isRepoRelativePath(script) {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' field 'script' must be a repo-relative path (no absolute, no '..' escapes): " + script,
			}
		}
		if len(whenPaths) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' (kind require_script) requires field 'when_paths'",
			}
		}
		if timeoutSec < 0 || killTimeoutSec < 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' timeout_sec / kill_timeout_sec must be >= 0",
			}
		}
	}

	// Phase 4C (W26): composite rule sub-checks.
	checks, err := optionalCheckList(item, "checks", id)
	if err != nil {
		return policy.Rule{}, err
	}
	if kind.IsComposite() {
		if len(checks) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' (kind " + string(kind) + ") requires field 'checks' (non-empty list)",
			}
		}
		if len(whenPaths) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' (kind " + string(kind) + ") requires field 'when_paths'",
			}
		}
		if kind == policy.KindNot && len(checks) != 1 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' (kind not) requires exactly one check, got " + itoa(len(checks)),
			}
		}
	}

	// Optional deprecation fields (W31). All optional; zero values
	// mean "not deprecated". Accepted on every rule kind so the
	// lifecycle is uniform.
	deprecated := false
	if raw, ok := item["deprecated"]; ok && raw != nil {
		b, isBool := raw.(bool)
		if !isBool {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' deprecated must be a boolean",
			}
		}
		deprecated = b
	}
	deprecatedReason, err := optionalString(item, "deprecated_reason", id, "", 0)
	if err != nil {
		return policy.Rule{}, err
	}
	deprecatedSince, err := optionalString(item, "deprecated_since", id, "", 0)
	if err != nil {
		return policy.Rule{}, err
	}
	deprecatedReplacedBy, err := optionalString(item, "deprecated_replaced_by", id, "", 0)
	if err != nil {
		return policy.Rule{}, err
	}

	return policy.Rule{
		ID:                   id,
		Kind:                 kind,
		Mode:                 mode,
		Message:              message,
		Paths:                paths,
		BeforePaths:          beforePaths,
		WhenPaths:            whenPaths,
		Commands:             commands,
		Claims:               claims,
		RequiredFiles:        requiredFiles,
		Evidence:             evidence,
		Checks:               checks,
		Script:               script,
		Args:                 args,
		TimeoutSec:           timeoutSec,
		KillTimeoutSec:       killTimeoutSec,
		SourcePath:           src.Path,
		SourceBlockID:        src.BlockID,
		Deprecated:           deprecated,
		DeprecatedReason:     deprecatedReason,
		DeprecatedSince:      deprecatedSince,
		DeprecatedReplacedBy: deprecatedReplacedBy,
	}, nil
}

// isRepoRelativePath reports whether p is safe to interpret as
// repo-relative (no absolute path, no parent-escape via "..").
//
// This is the same check we already apply to include patterns; reused
// here for require_script paths so a malicious or buggy rule cannot
// execute arbitrary binaries outside the repo.
func isRepoRelativePath(p string) bool {
	cleaned := strings.TrimSpace(p)
	if cleaned == "" {
		return false
	}
	if strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, ":") {
		// Absolute (POSIX) or Windows-drive prefix
		return false
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// optionalCheckList parses an optional `checks:` list of sub-check
// objects used by composite rule kinds (all_of / any_of / not).
//
// Each entry must specify a `kind` plus the inline fields appropriate
// for that kind. Validation is per-kind so misshapen sub-checks fail
// loudly at compile time.
func optionalCheckList(item map[string]interface{}, key, ruleID string) ([]policy.Check, error) {
	raw, ok := item[key]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + key + "' must be a list of check mappings",
		}
	}
	out := make([]policy.Check, 0, len(list))
	for i, entry := range list {
		mapping, ok := entry.(map[string]interface{})
		if !ok {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + key + "[" + itoa(i) + "]' must be a YAML mapping",
			}
		}
		c, err := parseCheck(mapping, ruleID, key, i)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// parseCheck validates one sub-check mapping per its kind. Inline
// fields (path/file/script for require_fresh_file/evidence/script)
// are required where the kind expects them.
func parseCheck(item map[string]interface{}, ruleID, listKey string, index int) (policy.Check, error) {
	kindStr, err := requiredStringField(item, "kind", ruleID, listKey, index)
	if err != nil {
		return policy.Check{}, err
	}
	kind := policy.Kind(kindStr)
	if !kind.Valid() {
		return policy.Check{}, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "].kind' is not a recognized kind: " + kindStr,
		}
	}
	if kind.IsComposite() {
		return policy.Check{}, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "]' nested composite kinds are not supported in v1; flatten the rule",
		}
	}

	check := policy.Check{Kind: kind}

	// Common optional flag.
	if optional, err := optionalBool(item, "optional", ruleID, listKey, index); err != nil {
		return policy.Check{}, err
	} else {
		check.Optional = optional
	}

	switch kind {
	case policy.KindRequireFreshFile:
		path, err := requiredStringField(item, "path", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		check.Path = path
		hours, err := optionalInt(item, "max_age_hours", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if hours < 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "].max_age_hours' must be >= 0",
			}
		}
		check.MaxAgeHours = hours

	case policy.KindRequireEvidence:
		file, err := requiredStringField(item, "file", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		check.File = file
		mustExist, err := optionalBool(item, "must_exist", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		check.MustExist = mustExist
		mustContain, err := optionalContainList(item, "must_contain", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		check.MustContain = mustContain
		mustNotContain, err := optionalString(item, "must_not_contain", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		check.MustNotContain = mustNotContain
		maxLines, err := optionalInt(item, "max_line_count", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if maxLines < 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "].max_line_count' must be >= 0",
			}
		}
		check.MaxLineCount = maxLines
		if !mustExist && len(mustContain) == 0 && mustNotContain == "" && maxLines == 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "]' must specify at least one of: must_exist, must_contain, must_not_contain, max_line_count",
			}
		}

	case policy.KindRequireClaim:
		claims, err := optionalContainList(item, "claims", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if len(claims) == 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "].claims' is required",
			}
		}
		check.Claims = claims

	case policy.KindRequireCommand, policy.KindRequireCommandSuccess, policy.KindForbidCommand:
		commands, err := optionalContainList(item, "commands", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if len(commands) == 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "].commands' is required",
			}
		}
		check.Commands = commands

	case policy.KindDenyWrite:
		paths, err := optionalContainList(item, "paths", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if len(paths) == 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "].paths' is required",
			}
		}
		check.Paths = paths

	case policy.KindRequireScript:
		script, err := requiredStringField(item, "script", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if !isRepoRelativePath(script) {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "].script' must be a repo-relative path: " + script,
			}
		}
		check.Script = script
		args, err := optionalContainList(item, "args", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		check.Args = args
		timeoutSec, err := optionalInt(item, "timeout_sec", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if timeoutSec < 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "].timeout_sec' must be >= 0",
			}
		}
		check.TimeoutSec = timeoutSec

	default:
		// Other kinds (require_read, couple_change) are not yet
		// supported as sub-checks. Add when first user needs them.
		return policy.Check{}, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "].kind' " + kindStr + " is not yet supported as a sub-check",
		}
	}

	return check, nil
}

// optionalRequiredFileList parses an optional `required_files:` list
// of {path, max_age_hours, optional} mappings used by require_fresh_file.
func optionalRequiredFileList(item map[string]interface{}, key, ruleID string) ([]policy.RequiredFile, error) {
	raw, ok := item[key]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + key + "' must be a list of {path, max_age_hours, optional} mappings",
		}
	}
	out := make([]policy.RequiredFile, 0, len(list))
	for i, entry := range list {
		mapping, ok := entry.(map[string]interface{})
		if !ok {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + key + "[" + itoa(i) + "]' must be a YAML mapping",
			}
		}
		path, err := requiredStringField(mapping, "path", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		ageHours, err := optionalInt(mapping, "max_age_hours", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		if ageHours < 0 {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + key + "[" + itoa(i) + "].max_age_hours' must be >= 0",
			}
		}
		optional, err := optionalBool(mapping, "optional", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		out = append(out, policy.RequiredFile{
			Path:        path,
			MaxAgeHours: ageHours,
			Optional:    optional,
		})
	}
	return out, nil
}

// optionalEvidenceCheckList parses an optional `evidence:` list of
// EvidenceCheck mappings used by require_evidence.
func optionalEvidenceCheckList(item map[string]interface{}, key, ruleID string) ([]policy.EvidenceCheck, error) {
	raw, ok := item[key]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + key + "' must be a list of evidence check mappings",
		}
	}
	out := make([]policy.EvidenceCheck, 0, len(list))
	for i, entry := range list {
		mapping, ok := entry.(map[string]interface{})
		if !ok {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + key + "[" + itoa(i) + "]' must be a YAML mapping",
			}
		}
		file, err := requiredStringField(mapping, "file", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		mustExist, err := optionalBool(mapping, "must_exist", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		mustContain, err := optionalContainList(mapping, "must_contain", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		mustNotContain, err := optionalString(mapping, "must_not_contain", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		maxLines, err := optionalInt(mapping, "max_line_count", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		if maxLines < 0 {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + key + "[" + itoa(i) + "].max_line_count' must be >= 0",
			}
		}
		optional, err := optionalBool(mapping, "optional", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		// Validate at least one assertion is present
		if !mustExist && len(mustContain) == 0 && mustNotContain == "" && maxLines == 0 {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + key + "[" + itoa(i) + "]' must specify at least one of: must_exist, must_contain, must_not_contain, max_line_count",
			}
		}
		out = append(out, policy.EvidenceCheck{
			File:           file,
			MustExist:      mustExist,
			MustContain:    mustContain,
			MustNotContain: mustNotContain,
			MaxLineCount:   maxLines,
			Optional:       optional,
		})
	}
	return out, nil
}

// requiredStringField is the field-validation helper for nested objects
// inside lists (required_files[i].path, evidence[i].file).
func requiredStringField(item map[string]interface{}, field, ruleID, listKey string, index int) (string, error) {
	raw, ok := item[field]
	if !ok || raw == nil {
		return "", &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "]." + field + "' is required",
		}
	}
	str, isStr := raw.(string)
	if !isStr || strings.TrimSpace(str) == "" {
		return "", &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "]." + field + "' must be a non-empty string",
		}
	}
	return strings.TrimSpace(str), nil
}

// optionalInt accepts int, int64, json.Number, or yaml.v3-decoded ints.
// Returns 0 when the field is absent or null.
func optionalInt(item map[string]interface{}, field, ruleID, listKey string, index int) (int, error) {
	raw, ok := item[field]
	if !ok || raw == nil {
		return 0, nil
	}
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		// yaml.v3 may decode small ints as float; tolerate if exact.
		if float64(int(v)) == v {
			return int(v), nil
		}
	}
	return 0, &rerrors.RuleValidationError{
		Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "]." + field + "' must be an integer",
	}
}

// optionalBool returns false when the field is absent or null.
func optionalBool(item map[string]interface{}, field, ruleID, listKey string, index int) (bool, error) {
	raw, ok := item[field]
	if !ok || raw == nil {
		return false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "]." + field + "' must be a boolean",
		}
	}
	return b, nil
}

// optionalString returns "" when the field is absent or null.
func optionalString(item map[string]interface{}, field, ruleID, listKey string, index int) (string, error) {
	raw, ok := item[field]
	if !ok || raw == nil {
		return "", nil
	}
	str, isStr := raw.(string)
	if !isStr {
		return "", &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "]." + field + "' must be a string",
		}
	}
	return str, nil
}

// optionalContainList returns nil when the field is absent. Each entry
// must be a non-empty string; empty list is treated as absent.
func optionalContainList(item map[string]interface{}, field, ruleID, listKey string, index int) ([]string, error) {
	raw, ok := item[field]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "]." + field + "' must be a list of strings",
		}
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok || s == "" {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + itoa(index) + "]." + field + "' entries must be non-empty strings",
			}
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// requiredString returns a non-empty trimmed string for the given key
// or a RuleValidationError describing the failure.
func requiredString(item map[string]interface{}, key, srcPath string, index int) (string, error) {
	raw, ok := item[key]
	if !ok || raw == nil {
		return "", &rerrors.RuleValidationError{
			Message: "rule field '" + key + "' is required (" + srcPath + " rule[" + itoa(index) + "])",
		}
	}
	str, isStr := raw.(string)
	if !isStr {
		return "", &rerrors.RuleValidationError{
			Message: "rule field '" + key + "' must be a string",
		}
	}
	cleaned := strings.TrimSpace(str)
	if cleaned == "" {
		return "", &rerrors.RuleValidationError{
			Message: "rule field '" + key + "' must be non-empty",
		}
	}
	return cleaned, nil
}

// optionalStringList returns nil when the key is absent, or a
// fully-validated []string. Empty entries and non-string elements
// trigger RuleValidationError.
func optionalStringList(item map[string]interface{}, key, ruleID string) ([]string, error) {
	raw, ok := item[key]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + key + "' must be a list of non-empty strings",
		}
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		str, isStr := v.(string)
		if !isStr || strings.TrimSpace(str) == "" {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + key + "' must be a list of non-empty strings",
			}
		}
		out = append(out, str)
	}
	return out, nil
}

// decodeYAMLMapping is a parser-local copy that returns the right
// error type. The ingest layer has its own (PolicySourceError) version.
func decodeYAMLMapping(raw, context string) (map[string]interface{}, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]interface{}{}, nil
	}
	var doc interface{}
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, &rerrors.RuleValidationError{
			Message: "invalid yaml in " + context,
			Cause:   err,
		}
	}
	if doc == nil {
		return map[string]interface{}{}, nil
	}
	mapping, ok := doc.(map[string]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{
			Message: "expected a YAML mapping in " + context,
		}
	}
	return mapping, nil
}

// expandTemplate resolves a template name and merges its body into
// userItem. User-supplied fields always win over template defaults.
// Returns a new map; does not mutate userItem.
//
// Any error from templates.Resolve is wrapped in a RuleValidationError
// so the rule's source context surfaces correctly in CLI output.
func expandTemplate(userItem map[string]interface{}, name string, src policy.PolicySource, index int) (map[string]interface{}, error) {
	tmpl, err := templates.Resolve(name)
	if err != nil {
		return nil, &rerrors.RuleValidationError{
			Message: "rule #" + itoa(index) + " in " + src.Path + ": " + err.Error(),
			Cause:   err,
		}
	}
	return templates.Apply(tmpl, userItem), nil
}

// itoa is a tiny stdlib-free integer-to-string for index labels in
// error messages. Avoids importing strconv just for this.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
