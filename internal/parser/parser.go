// Package parser validates and normalizes raw rule documents from a
// SourceBundle into a strongly-typed ParsedPolicy ready for the
// compiler to serialize.
//
// Validation is strict by design: any unknown rule kind, missing required
// field, or duplicate rule ID from any source tier raises *RuleValidationError
// instead of applying an implicit cross-tier override.
package parser

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"reconc.dev/reconc/internal/action"
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
	DefaultMode policy.Mode       `json:"default_mode"`
	Rules       []policy.Rule     `json:"rules"`
	Actions     *action.Plan      `json:"actions,omitempty"`
	MCP         *policy.MCPPolicy `json:"mcp,omitempty"`
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
// (with both source paths / the rule ID in duplicate diagnostics) so users
// can fix one problem at a time.
func ParseRuleDocuments(bundle *ingest.SourceBundle) (*ParsedPolicy, error) {
	return parseRuleDocumentsWithDecoder(bundle, decodeRuleSourceDocumentBounded)
}

type sourceDocumentDecoder func(source policy.PolicySource) (*parserSourceDocument, error)

func parseRuleDocumentsWithDecoder(bundle *ingest.SourceBundle, decode sourceDocumentDecoder) (*ParsedPolicy, error) {
	if bundle == nil {
		return nil, &rerrors.RuleValidationError{Message: "bundle is nil"}
	}

	defaultMode := DefaultMode
	rules := []policy.Rule{}
	seen := map[string]string{} // rule id -> source path of first sighting
	var mcpPolicy *policy.MCPPolicy
	var actionPolicy *action.Plan

	for _, src := range bundle.Sources {
		// Skip context-only sources; their fenced blocks land as
		// separate inline_block sources we DO process.
		switch src.Kind {
		case policy.SourceClaudeMD, policy.SourceAgentsMD, policy.SourceStartMD, policy.SourceCustomRuntime:
			continue
		}

		document, err := decode(src)
		if err != nil {
			return nil, err
		}
		doc := document.mapping
		if err := validateRuleDocumentBounds(src, doc, len(rules)); err != nil {
			return nil, err
		}
		if err := validateDocumentFields(src, doc); err != nil {
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
			parsedMCP, present, err := parseMCPPolicy(src, doc)
			if err != nil {
				return nil, err
			}
			if present {
				mcpPolicy = parsedMCP
			}
		}
		if src.Kind == policy.SourceCompilerConfig || impactCandidateSource(src) {
			parsedActions, present, err := parseActionPolicy(src, document.root)
			if err != nil {
				return nil, err
			}
			if present {
				actionPolicy, err = mergeActionPolicies(actionPolicy, parsedActions)
				if err != nil {
					return nil, err
				}
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
		Actions:     actionPolicy,
		MCP:         mcpPolicy,
	}, nil
}

func parseMCPPolicy(src policy.PolicySource, doc map[string]interface{}) (*policy.MCPPolicy, bool, error) {
	raw, present := doc["mcp"]
	if !present {
		return nil, false, nil
	}
	mapping, ok := raw.(map[string]interface{})
	if !ok {
		return nil, true, &rerrors.RuleValidationError{Message: "mcp must be a mapping in " + src.Path}
	}
	unclassified := policy.MCPUnclassifiedHost
	if value, exists := mapping["unclassified"]; exists {
		text, ok := value.(string)
		if !ok {
			return nil, true, &rerrors.RuleValidationError{Message: "mcp.unclassified must be a string in " + src.Path}
		}
		unclassified = policy.MCPUnclassifiedMode(text)
	}
	tools := []policy.MCPToolPolicy{}
	if rawTools, exists := mapping["tools"]; exists {
		list, ok := rawTools.([]interface{})
		if !ok {
			return nil, true, &rerrors.RuleValidationError{Message: "mcp.tools must be a list in " + src.Path}
		}
		tools = make([]policy.MCPToolPolicy, 0, len(list))
		for index, rawTool := range list {
			toolMapping, ok := rawTool.(map[string]interface{})
			if !ok {
				return nil, true, &rerrors.RuleValidationError{Message: "mcp.tools[" + strconv.Itoa(index) + "] must be a mapping in " + src.Path}
			}
			tool, err := parseMCPToolPolicy(src, toolMapping, index)
			if err != nil {
				return nil, true, err
			}
			tools = append(tools, tool)
		}
	}
	contract := &policy.MCPPolicy{Unclassified: unclassified, Tools: tools}
	if err := contract.Validate(); err != nil {
		return nil, true, &rerrors.RuleValidationError{Message: src.Path + ": " + err.Error()}
	}
	sort.Slice(contract.Tools, func(i, j int) bool {
		return contract.Tools[i].StableKey() < contract.Tools[j].StableKey()
	})
	return contract, true, nil
}

func parseMCPToolPolicy(src policy.PolicySource, mapping map[string]interface{}, index int) (policy.MCPToolPolicy, error) {
	context := "mcp.tools[" + strconv.Itoa(index) + "]"
	required := func(field string) (string, error) {
		raw, present := mapping[field]
		value, ok := raw.(string)
		if !present || !ok || value == "" {
			return "", &rerrors.RuleValidationError{Message: context + "." + field + " must be a non-empty string in " + src.Path}
		}
		return value, nil
	}
	platform, err := required("platform")
	if err != nil {
		return policy.MCPToolPolicy{}, err
	}
	tool, err := required("tool")
	if err != nil {
		return policy.MCPToolPolicy{}, err
	}
	effect, err := required("effect")
	if err != nil {
		return policy.MCPToolPolicy{}, err
	}
	serverFingerprint := ""
	if raw, present := mapping["server_fingerprint"]; present {
		value, ok := raw.(string)
		if !ok || value == "" {
			return policy.MCPToolPolicy{}, &rerrors.RuleValidationError{Message: context + ".server_fingerprint must be a non-empty string in " + src.Path}
		}
		serverFingerprint = value
	}
	pathFields, err := mcpStringList(mapping, "path_fields", context, src.Path)
	if err != nil {
		return policy.MCPToolPolicy{}, err
	}
	commandField := ""
	if raw, present := mapping["command_field"]; present {
		value, ok := raw.(string)
		if !ok || value == "" {
			return policy.MCPToolPolicy{}, &rerrors.RuleValidationError{Message: context + ".command_field must be a non-empty RFC 6901 JSON Pointer in " + src.Path}
		}
		commandField = value
	}
	return policy.MCPToolPolicy{
		Platform:          policy.MCPPlatform(platform),
		ServerFingerprint: serverFingerprint,
		Tool:              tool,
		Effect:            policy.MCPEffect(effect),
		PathFields:        pathFields,
		CommandField:      commandField,
		SourcePath:        src.Path,
	}, nil
}

func mcpStringList(mapping map[string]interface{}, field, context, sourcePath string) ([]string, error) {
	raw, present := mapping[field]
	if !present {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, &rerrors.RuleValidationError{Message: context + "." + field + " must be a list of non-empty strings in " + sourcePath}
	}
	out := make([]string, 0, len(list))
	for _, rawValue := range list {
		value, ok := rawValue.(string)
		if !ok || value == "" {
			return nil, &rerrors.RuleValidationError{Message: context + "." + field + " must be a list of non-empty strings in " + sourcePath}
		}
		out = append(out, value)
	}
	return out, nil
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
				Message: "each scope must be a YAML mapping in " + src.Path + " (scope #" + strconv.Itoa(i) + ")",
			}
		}
		paths, err := optionalStringList(mapping, "paths", "scope#"+strconv.Itoa(i))
		if err != nil {
			return nil, err
		}
		if len(paths) == 0 {
			return nil, &rerrors.RuleValidationError{
				Message: "scope #" + strconv.Itoa(i) + " in " + src.Path + " requires non-empty 'paths'",
			}
		}
		if err := validateGlobPatterns(paths, "scope #"+strconv.Itoa(i)+" in "+src.Path+" field 'paths'"); err != nil {
			return nil, err
		}
		scopeID, err := optionalString(mapping, "id", "scope#"+strconv.Itoa(i), "", 0)
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
				Message: "scope #" + strconv.Itoa(i) + " 'rules' must be a list in " + src.Path,
			}
		}
		for j, ri := range ruleList {
			rmap, ok := ri.(map[string]interface{})
			if !ok {
				return nil, &rerrors.RuleValidationError{
					Message: "rule #" + strconv.Itoa(j) + " of scope #" + strconv.Itoa(i) + " in " + src.Path + " must be a mapping",
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
	if err := validateRuleMapBounds(src, item, "rules["+strconv.Itoa(index)+"]", id); err != nil {
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
	if kind != policy.KindRequireAssurance {
		if _, present := item["assurance"]; present {
			return policy.Rule{}, &rerrors.RuleValidationError{Message: "rule '" + id + "' field 'assurance' is only valid for kind require_assurance"}
		}
	} else {
		for _, field := range []string{"paths", "before_paths", "commands", "claims", "required_files", "evidence", "script", "args", "timeout_sec", "kill_timeout_sec", "checks"} {
			if _, present := item[field]; present {
				return policy.Rule{}, &rerrors.RuleValidationError{Message: "rule '" + id + "' field '" + field + "' is not valid for kind require_assurance"}
			}
		}
	}
	if kind != policy.KindRequireScript {
		if _, present := item["cache_inputs"]; present {
			return policy.Rule{}, &rerrors.RuleValidationError{Message: "rule '" + id + "' field 'cache_inputs' is only valid for kind require_script"}
		}
	}
	if err := validateRuleKindFields(item, kind, id, src.Path); err != nil {
		return policy.Rule{}, err
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
	for _, globField := range []struct {
		name     string
		patterns []string
	}{
		{"paths", paths},
		{"before_paths", beforePaths},
		{"when_paths", whenPaths},
	} {
		if err := validateGlobPatterns(globField.patterns, "rule '"+id+"' field '"+globField.name+"' in "+src.Path); err != nil {
			return policy.Rule{}, err
		}
	}
	commands, err := optionalStringList(item, "commands", id)
	if err != nil {
		return policy.Rule{}, err
	}
	claims, err := optionalStringList(item, "claims", id)
	if err != nil {
		return policy.Rule{}, err
	}
	commandMatch, err := parseCommandMatch(item, fmt.Sprintf("rule '%s' (kind %s) field 'command_match' in %s", id, kind, src.Path))
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
	cacheInputs, err := optionalContainList(item, "cache_inputs", id, "rule", index)
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
		if err := validateScriptCacheInputs(cacheInputs, "rule '"+id+"' field 'cache_inputs'"); err != nil {
			return policy.Rule{}, err
		}
	} else if len(cacheInputs) > 0 {
		return policy.Rule{}, &rerrors.RuleValidationError{
			Message: "rule '" + id + "' field 'cache_inputs' is only valid for kind require_script",
		}
	}

	// Phase 4C (W26): composite rule sub-checks.
	checks, err := optionalCheckList(item, "checks", id, src.Path)
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
				Message: "rule '" + id + "' (kind not) requires exactly one check, got " + strconv.Itoa(len(checks)),
			}
		}
	}

	assurance, err := optionalAssuranceGateList(item, "assurance", id)
	if err != nil {
		return policy.Rule{}, err
	}
	if kind == policy.KindRequireAssurance {
		if len(assurance) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' (kind require_assurance) requires field 'assurance' (non-empty list)",
			}
		}
		if len(whenPaths) == 0 {
			return policy.Rule{}, &rerrors.RuleValidationError{
				Message: "rule '" + id + "' (kind require_assurance) requires field 'when_paths'",
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
		CommandMatch:         commandMatch,
		RequiredFiles:        requiredFiles,
		Evidence:             evidence,
		Checks:               checks,
		Script:               script,
		Args:                 args,
		TimeoutSec:           timeoutSec,
		KillTimeoutSec:       killTimeoutSec,
		CacheInputs:          cacheInputs,
		Assurance:            assurance,
		SourcePath:           src.Path,
		SourceBlockID:        src.BlockID,
		Deprecated:           deprecated,
		DeprecatedReason:     deprecatedReason,
		DeprecatedSince:      deprecatedSince,
		DeprecatedReplacedBy: deprecatedReplacedBy,
	}, nil
}

// parseCommandMatch validates the optional command_match value. Field-kind
// eligibility is owned by the rule/check field matrix before this function.
func parseCommandMatch(item map[string]interface{}, context string) (policy.CommandMatch, error) {
	raw, present := item["command_match"]
	if !present || raw == nil {
		return "", nil
	}
	value, isString := raw.(string)
	if !isString {
		return "", &rerrors.RuleValidationError{Message: context + " must be a string"}
	}
	match := policy.CommandMatch(strings.TrimSpace(value))
	if !match.Valid() {
		return "", &rerrors.RuleValidationError{Message: context + " must be 'exact' or 'prefix', got: " + value}
	}
	if match == policy.CommandMatchExact {
		// Exact is the default; keep the lockfile free of redundant keys.
		return "", nil
	}
	return match, nil
}

// validateScriptCacheInputs enforces the shape Stop report reuse can bind: a
// literal, repo-relative, duplicate-free path list. A glob or template would
// have to be resolved on the Stop hot path, and a path that leaves the
// repository cannot be bound at all.
func validateScriptCacheInputs(inputs []string, context string) error {
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if !isRepoRelativePath(input) {
			return &rerrors.RuleValidationError{
				Message: context + " must contain repo-relative paths (no absolute, no '..' escapes): " + input,
			}
		}
		if strings.ContainsAny(input, "*?[]{}") {
			return &rerrors.RuleValidationError{
				Message: context + " must name literal files, not globs or template variables: " + input,
			}
		}
		if _, duplicate := seen[input]; duplicate {
			return &rerrors.RuleValidationError{
				Message: context + " lists " + input + " more than once",
			}
		}
		seen[input] = struct{}{}
	}
	return nil
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
	normalized := strings.ReplaceAll(cleaned, `\`, "/")
	if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") {
		// Absolute (POSIX) or Windows-drive prefix
		return false
	}
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

func validateRepoRelativeTemplatePath(value, context string) error {
	probe, err := templates.MaskVariables(value, "reconc-template-value")
	if err != nil {
		return &rerrors.RuleValidationError{Message: context + " has invalid template syntax: " + err.Error()}
	}
	if isRepoRelativePath(probe) {
		return nil
	}
	return &rerrors.RuleValidationError{
		Message: context + " must be a repo-relative path (no absolute path or '..' escape): " + value,
	}
}

// optionalCheckList parses an optional `checks:` list of sub-check
// objects used by composite rule kinds (all_of / any_of / not).
//
// Each entry must specify a `kind` plus the inline fields appropriate
// for that kind. Validation is per-kind so misshapen sub-checks fail
// loudly at compile time.
func optionalCheckList(item map[string]interface{}, key, ruleID string, sourcePaths ...string) ([]policy.Check, error) {
	sourcePath := ""
	if len(sourcePaths) > 0 {
		sourcePath = sourcePaths[0]
	}
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
				Message: "rule '" + ruleID + "' field '" + key + "[" + strconv.Itoa(i) + "]' must be a YAML mapping",
			}
		}
		c, err := parseCheckWithSource(mapping, ruleID, key, sourcePath, i)
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
	return parseCheckWithSource(item, ruleID, listKey, "", index)
}

func parseCheckWithSource(item map[string]interface{}, ruleID, listKey, sourcePath string, index int) (policy.Check, error) {
	kindStr, err := requiredStringField(item, "kind", ruleID, listKey, index)
	if err != nil {
		return policy.Check{}, err
	}
	kind := policy.Kind(kindStr)
	if !kind.Valid() {
		return policy.Check{}, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "].kind' is not a recognized kind: " + kindStr,
		}
	}
	if kind.IsComposite() {
		return policy.Check{}, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "]' nested composite kinds are not supported in v1; flatten the rule",
		}
	}
	if err := validateCheckKindFields(item, kind, ruleID, sourcePath, index); err != nil {
		return policy.Check{}, err
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
		if err := validateRepoRelativeTemplatePath(path, "rule '"+ruleID+"' field '"+listKey+"["+strconv.Itoa(index)+"].path'"); err != nil {
			return policy.Check{}, err
		}
		check.Path = path
		hours, err := optionalInt(item, "max_age_hours", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if hours < 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "].max_age_hours' must be >= 0",
			}
		}
		check.MaxAgeHours = hours

	case policy.KindRequireEvidence:
		file, err := requiredStringField(item, "file", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if err := validateRepoRelativeTemplatePath(file, "rule '"+ruleID+"' field '"+listKey+"["+strconv.Itoa(index)+"].file'"); err != nil {
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
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "].max_line_count' must be >= 0",
			}
		}
		check.MaxLineCount = maxLines
		if !mustExist && len(mustContain) == 0 && mustNotContain == "" && maxLines == 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "]' must specify at least one of: must_exist, must_contain, must_not_contain, max_line_count",
			}
		}

	case policy.KindRequireClaim:
		claims, err := optionalContainList(item, "claims", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if len(claims) == 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "].claims' is required",
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
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "].commands' is required",
			}
		}
		check.Commands = commands
		if sourcePath == "" {
			sourcePath = "<unknown source>"
		}
		context := fmt.Sprintf("rule '%s' check[%d] (kind %s) field 'command_match' in %s", ruleID, index, kind, sourcePath)
		match, err := parseCommandMatch(item, context)
		if err != nil {
			return policy.Check{}, err
		}
		check.CommandMatch = match

	case policy.KindDenyWrite:
		paths, err := optionalContainList(item, "paths", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if len(paths) == 0 {
			return policy.Check{}, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "].paths' is required",
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
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "].script' must be a repo-relative path: " + script,
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
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "].timeout_sec' must be >= 0",
			}
		}
		check.TimeoutSec = timeoutSec
		cacheInputs, err := optionalContainList(item, "cache_inputs", ruleID, listKey, index)
		if err != nil {
			return policy.Check{}, err
		}
		if err := validateScriptCacheInputs(cacheInputs, "rule '"+ruleID+"' field '"+listKey+"["+strconv.Itoa(index)+"].cache_inputs'"); err != nil {
			return policy.Check{}, err
		}
		check.CacheInputs = cacheInputs

	default:
		// Other kinds (require_read, couple_change) are not yet
		// supported as sub-checks. Add when first user needs them.
		return policy.Check{}, &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "].kind' " + kindStr + " is not yet supported as a sub-check",
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
				Message: "rule '" + ruleID + "' field '" + key + "[" + strconv.Itoa(i) + "]' must be a YAML mapping",
			}
		}
		path, err := requiredStringField(mapping, "path", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		if err := validateRepoRelativeTemplatePath(path, "rule '"+ruleID+"' field '"+key+"["+strconv.Itoa(i)+"].path'"); err != nil {
			return nil, err
		}
		ageHours, err := optionalInt(mapping, "max_age_hours", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		if ageHours < 0 {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + key + "[" + strconv.Itoa(i) + "].max_age_hours' must be >= 0",
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
				Message: "rule '" + ruleID + "' field '" + key + "[" + strconv.Itoa(i) + "]' must be a YAML mapping",
			}
		}
		file, err := requiredStringField(mapping, "file", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		if err := validateRepoRelativeTemplatePath(file, "rule '"+ruleID+"' field '"+key+"["+strconv.Itoa(i)+"].file'"); err != nil {
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
				Message: "rule '" + ruleID + "' field '" + key + "[" + strconv.Itoa(i) + "].max_line_count' must be >= 0",
			}
		}
		optional, err := optionalBool(mapping, "optional", ruleID, key, i)
		if err != nil {
			return nil, err
		}
		// Validate at least one assertion is present
		if !mustExist && len(mustContain) == 0 && mustNotContain == "" && maxLines == 0 {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + key + "[" + strconv.Itoa(i) + "]' must specify at least one of: must_exist, must_contain, must_not_contain, max_line_count",
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
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "]." + field + "' is required",
		}
	}
	str, isStr := raw.(string)
	if !isStr || strings.TrimSpace(str) == "" {
		return "", &rerrors.RuleValidationError{
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "]." + field + "' must be a non-empty string",
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
		Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "]." + field + "' must be an integer",
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
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "]." + field + "' must be a boolean",
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
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "]." + field + "' must be a string",
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
			Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "]." + field + "' must be a list of strings",
		}
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, &rerrors.RuleValidationError{
				Message: "rule '" + ruleID + "' field '" + listKey + "[" + strconv.Itoa(index) + "]." + field + "' entries must be non-empty strings",
			}
		}
		out = append(out, strings.TrimSpace(s))
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
			Message: "rule field '" + key + "' is required (" + srcPath + " rule[" + strconv.Itoa(index) + "])",
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
		out = append(out, strings.TrimSpace(str))
	}
	return out, nil
}

// validateGlobPatterns rejects syntactically invalid glob patterns at compile
// time so a malformed pattern (for example an unterminated character class)
// fails the compile instead of producing a lockfile that only the runtime
// evaluator can reject. The probe path never matches; only a pattern syntax
// error is surfaced. Template placeholders such as {task_id} are legal brace
// groups to doublestar and pass validation.
func validateGlobPatterns(patterns []string, context string) error {
	for _, pattern := range patterns {
		if _, err := templates.Variables(pattern); err != nil {
			return &rerrors.RuleValidationError{
				Message: context + " has invalid template syntax in " + strconv.Quote(pattern) + ": " + err.Error(),
			}
		}
		if _, err := doublestar.Match(pattern, "reconc-glob-syntax-probe"); err != nil {
			return &rerrors.RuleValidationError{
				Message: context + " has an invalid glob pattern " + strconv.Quote(pattern) + ": " + err.Error(),
			}
		}
	}
	return nil
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
			Message: "rule #" + strconv.Itoa(index) + " in " + src.Path + ": " + err.Error(),
			Cause:   err,
		}
	}
	return templates.Apply(tmpl, userItem), nil
}
