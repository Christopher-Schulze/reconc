package action

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	safeLabelPattern       = regexpMustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	customPlatformPattern  = regexpMustCompile(`^custom:[a-z0-9][a-z0-9_-]{0,47}$`)
	sha256IdentityPattern  = regexpMustCompile(`^sha256:[0-9a-f]{64}$`)
	hmacIdentityPattern    = regexpMustCompile(`^hmac-sha256:v1:[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?:[0-9a-f]{64}$`)
	gatewayToolNamePattern = regexpMustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
)

// CompilePlan normalizes, validates, and precompiles one canonical action
// plan. The returned plan owns detached slices and immutable matcher programs.
func CompilePlan(input Plan) (*CompiledPlan, error) {
	plan := clonePlan(input)
	if plan.FormatVersion == "" {
		plan.FormatVersion = PlanFormatVersion
	}
	if plan.FormatVersion != PlanFormatVersion {
		return nil, fmt.Errorf("actions.format_version must equal %s", PlanFormatVersion)
	}
	if plan.Tools == nil {
		plan.Tools = []Tool{}
	}
	if plan.Rules == nil {
		plan.Rules = []Rule{}
	}
	if err := normalizeDefaults(&plan.Defaults); err != nil {
		return nil, err
	}
	if len(plan.Tools) > MaxTools {
		return nil, fmt.Errorf("actions.tools contains %d declarations; maximum is %d", len(plan.Tools), MaxTools)
	}
	if len(plan.Rules) > MaxRules {
		return nil, fmt.Errorf("actions.rules contains %d rules; maximum is %d", len(plan.Rules), MaxRules)
	}
	toolByID := make(map[string]int, len(plan.Tools))
	toolByExact := make(map[string]int, len(plan.Tools))
	for index := range plan.Tools {
		if err := normalizeTool(&plan.Tools[index]); err != nil {
			return nil, fmt.Errorf("actions.tools[%d]: %w", index, err)
		}
	}
	sort.Slice(plan.Tools, func(i, j int) bool { return plan.Tools[i].ID < plan.Tools[j].ID })
	for index := range plan.Tools {
		tool := &plan.Tools[index]
		if _, duplicate := toolByID[tool.ID]; duplicate {
			return nil, fmt.Errorf("actions.tools contains duplicate id %q", tool.ID)
		}
		toolByID[tool.ID] = index
		key := ToolIdentityKey(*tool)
		if previous, duplicate := toolByExact[key]; duplicate {
			return nil, fmt.Errorf("actions.tools %q and %q own the same exact tool declaration", plan.Tools[previous].ID, tool.ID)
		}
		toolByExact[key] = index
	}
	compiledRules := make([]CompiledRule, len(plan.Rules))
	totalNodes := 0
	for index := range plan.Rules {
		compiled, nodes, err := normalizeRule(&plan.Rules[index], plan.Defaults, toolByID)
		if err != nil {
			return nil, fmt.Errorf("actions.rules[%d]: %w", index, err)
		}
		totalNodes += nodes
		if totalNodes > MaxCompiledNodes {
			return nil, fmt.Errorf("actions.rules compile to %d predicate nodes; maximum is %d", totalNodes, MaxCompiledNodes)
		}
		compiledRules[index] = CompiledRule{Rule: plan.Rules[index], Condition: compiled}
	}
	sort.Slice(compiledRules, func(i, j int) bool { return compiledRules[i].Rule.ID < compiledRules[j].Rule.ID })
	plan.Rules = make([]Rule, len(compiledRules))
	for index := range compiledRules {
		if index > 0 && compiledRules[index-1].Rule.ID == compiledRules[index].Rule.ID {
			return nil, fmt.Errorf("actions.rules contains duplicate id %q", compiledRules[index].Rule.ID)
		}
		plan.Rules[index] = compiledRules[index].Rule
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encode canonical action plan: %w", err)
	}
	logicalBytes := len(body)
	for _, rule := range compiledRules {
		logicalBytes += compiledConditionLogicalBytes(rule.Condition)
		if logicalBytes > MaxCompiledPlanBytes {
			return nil, fmt.Errorf("compiled action plan requires %d logical bytes; maximum is %d", logicalBytes, MaxCompiledPlanBytes)
		}
	}
	return &CompiledPlan{plan: plan, toolByID: toolByID, toolByExact: toolByExact, rules: compiledRules}, nil
}

func normalizeDefaults(defaults *Defaults) error {
	frozen := FrozenDefaults()
	if defaults.DeclaredTool == "" {
		defaults.DeclaredTool = frozen.DeclaredTool
	}
	if defaults.GatewayUnmatched == "" {
		defaults.GatewayUnmatched = frozen.GatewayUnmatched
	}
	if defaults.HostUnmatched == "" {
		defaults.HostUnmatched = frozen.HostUnmatched
	}
	if defaults.EvaluationError == "" {
		defaults.EvaluationError = frozen.EvaluationError
	}
	if defaults.PostError == "" {
		defaults.PostError = frozen.PostError
	}
	if defaults.ProgressError == "" {
		defaults.ProgressError = frozen.ProgressError
	}
	if defaults.Cache == "" {
		defaults.Cache = frozen.Cache
	}
	if !defaults.DeclaredTool.Valid() {
		return fmt.Errorf("actions.defaults.declared_tool is invalid")
	}
	if defaults.GatewayUnmatched != DecisionBlock {
		return fmt.Errorf("actions.defaults.gateway_unmatched must be block in action contract v1")
	}
	if defaults.HostUnmatched != DecisionAllow && defaults.HostUnmatched != DecisionBlock {
		return fmt.Errorf("actions.defaults.host_unmatched must be allow or block")
	}
	if defaults.EvaluationError != DecisionBlock || defaults.PostError != DecisionBlock || defaults.ProgressError != DecisionBlock {
		return fmt.Errorf("action contract v1 error defaults must be block")
	}
	if !defaults.Cache.Valid() {
		return fmt.Errorf("actions.defaults.cache must be exact or never")
	}
	return nil
}

func normalizeTool(tool *Tool) error {
	if !SafeLabel(tool.ID) {
		return fmt.Errorf("id must be a lower-kebab label of 1 to %d bytes", MaxSafeLabelBytes)
	}
	if !tool.Transport.Valid() {
		return fmt.Errorf("transport must be host_mcp or mcp_stdio")
	}
	if err := validateToolName(tool.Tool, tool.Transport == TransportMCPStdio); err != nil {
		return err
	}
	switch tool.Transport {
	case TransportHostMCP:
		if !ValidPlatform(tool.Platform) {
			return fmt.Errorf("platform is required and invalid for host_mcp")
		}
		if tool.ServerLabel != "" {
			return fmt.Errorf("server_label is forbidden for host_mcp")
		}
		if tool.ServerFingerprint != "" && !sha256IdentityPattern.MatchString(tool.ServerFingerprint) {
			return fmt.Errorf("host_mcp server_fingerprint must be sha256:<64-lowercase-hex>")
		}
	case TransportMCPStdio:
		if tool.Platform != "" {
			return fmt.Errorf("platform is forbidden for mcp_stdio")
		}
		if !SafeLabel(tool.ServerLabel) {
			return fmt.Errorf("server_label is required and must be a safe label for mcp_stdio")
		}
		if tool.ServerFingerprint != "" && !hmacIdentityPattern.MatchString(tool.ServerFingerprint) {
			return fmt.Errorf("mcp_stdio server_fingerprint must be a keyed HMAC identity")
		}
	}
	if !tool.Effect.Kind.Valid() {
		return fmt.Errorf("effect.kind is invalid")
	}
	if err := normalizePointers(&tool.Effect.PathFields, "effect.path_fields", false); err != nil {
		return err
	}
	if tool.Effect.CommandField != "" {
		if _, err := CompilePointer(tool.Effect.CommandField); err != nil {
			return fmt.Errorf("effect.command_field: %w", err)
		}
	}
	switch tool.Effect.Kind {
	case EffectRepositoryRead, EffectRepositoryWrite:
		if len(tool.Effect.PathFields) == 0 || tool.Effect.CommandField != "" {
			return fmt.Errorf("repository effects require path_fields and forbid command_field")
		}
	case EffectCommand:
		if tool.Effect.CommandField == "" || len(tool.Effect.PathFields) != 0 {
			return fmt.Errorf("command effect requires command_field and forbids path_fields")
		}
	case EffectExternal:
		if tool.Effect.CommandField != "" || len(tool.Effect.PathFields) != 0 {
			return fmt.Errorf("external effect forbids path_fields and command_field")
		}
	}
	if !tool.Origin.Valid() {
		return fmt.Errorf("origin must be actions or legacy_mcp")
	}
	if strings.TrimSpace(tool.SourceIdentity) == "" || !utf8.ValidString(tool.SourceIdentity) || len(tool.SourceIdentity) > MaxPointerBytes {
		return fmt.Errorf("source_identity must be a non-empty UTF-8 identity of at most %d bytes", MaxPointerBytes)
	}
	return nil
}

func normalizeRule(rule *Rule, defaults Defaults, toolByID map[string]int) (*CompiledCondition, int, error) {
	if !SafeLabel(rule.ID) {
		return nil, 0, fmt.Errorf("id must be a lower-kebab label of 1 to %d bytes", MaxSafeLabelBytes)
	}
	if !rule.Decision.Valid() {
		return nil, 0, fmt.Errorf("decision is invalid")
	}
	if rule.OnIndeterminate == "" {
		rule.OnIndeterminate = DecisionBlock
	}
	if rule.OnIndeterminate != DecisionBlock && rule.OnIndeterminate != DecisionRequireApproval {
		return nil, 0, fmt.Errorf("on_indeterminate must be block or require_approval")
	}
	if rule.Cache == "" {
		rule.Cache = defaults.Cache
	}
	if !rule.Cache.Valid() {
		return nil, 0, fmt.Errorf("cache must be exact or never")
	}
	if !utf8.ValidString(rule.Message) || len(rule.Message) > MaxRuleMessageBytes {
		return nil, 0, fmt.Errorf("message must be valid UTF-8 and at most %d bytes", MaxRuleMessageBytes)
	}
	if strings.TrimSpace(rule.SourceIdentity) == "" || !utf8.ValidString(rule.SourceIdentity) || len(rule.SourceIdentity) > MaxPointerBytes {
		return nil, 0, fmt.Errorf("source_identity must be a non-empty UTF-8 identity of at most %d bytes", MaxPointerBytes)
	}
	if err := normalizeSelector(&rule.Selector, toolByID); err != nil {
		return nil, 0, err
	}
	if rule.When == nil {
		return nil, 0, nil
	}
	compiled, nodes, err := compileCondition(rule.When, rule.Decision, selectedPhases(rule.Selector.Phases), 1)
	if err != nil {
		return nil, 0, err
	}
	return compiled, nodes, nil
}

func normalizeSelector(selector *Selector, toolByID map[string]int) error {
	if err := normalizeStringList(&selector.ToolIDs, "selector.tool_ids", SafeLabel); err != nil {
		return err
	}
	for _, id := range selector.ToolIDs {
		if _, exists := toolByID[id]; !exists {
			return fmt.Errorf("selector.tool_ids references undeclared tool %q", id)
		}
	}
	if err := normalizeTransports(&selector.Transports); err != nil {
		return err
	}
	if err := normalizePlatforms(&selector.Platforms); err != nil {
		return err
	}
	if err := normalizeStringList(&selector.ServerLabels, "selector.server_labels", SafeLabel); err != nil {
		return err
	}
	if err := normalizeStringList(&selector.ServerFingerprints, "selector.server_fingerprints", ValidIdentity); err != nil {
		return err
	}
	if err := normalizeStringList(&selector.Tools, "selector.tools", func(value string) bool { return validateToolName(value, false) == nil }); err != nil {
		return err
	}
	if err := normalizeStringList(&selector.ToolContractDigests, "selector.tool_contract_digests", sha256IdentityPattern.MatchString); err != nil {
		return err
	}
	if err := normalizePhases(&selector.Phases); err != nil {
		return err
	}
	return nil
}

func normalizeStringList(values *[]string, field string, valid func(string) bool) error {
	if *values != nil && len(*values) == 0 {
		return fmt.Errorf("%s cannot be an empty present list", field)
	}
	if len(*values) > MaxListValues {
		return fmt.Errorf("%s contains %d values; maximum is %d", field, len(*values), MaxListValues)
	}
	for _, value := range *values {
		if !valid(value) {
			return fmt.Errorf("%s contains invalid value %q", field, value)
		}
	}
	sort.Strings(*values)
	for index := 1; index < len(*values); index++ {
		if (*values)[index-1] == (*values)[index] {
			return fmt.Errorf("%s contains duplicate value %q", field, (*values)[index])
		}
	}
	return nil
}

func normalizePointers(values *[]string, field string, allowRoot bool) error {
	return normalizeStringList(values, field, func(value string) bool {
		if value == "" && !allowRoot {
			return false
		}
		_, err := CompilePointer(value)
		return err == nil
	})
}

func normalizeTransports(values *[]Transport) error {
	if *values != nil && len(*values) == 0 {
		return fmt.Errorf("selector.transports cannot be an empty present list")
	}
	if len(*values) > MaxListValues {
		return fmt.Errorf("selector.transports exceeds %d values", MaxListValues)
	}
	sort.Slice(*values, func(i, j int) bool { return (*values)[i] < (*values)[j] })
	for index, value := range *values {
		if !value.Valid() {
			return fmt.Errorf("selector.transports contains invalid value %q", value)
		}
		if index > 0 && (*values)[index-1] == value {
			return fmt.Errorf("selector.transports contains duplicate value %q", value)
		}
	}
	return nil
}

func normalizePlatforms(values *[]Platform) error {
	if *values != nil && len(*values) == 0 {
		return fmt.Errorf("selector.platforms cannot be an empty present list")
	}
	if len(*values) > MaxListValues {
		return fmt.Errorf("selector.platforms exceeds %d values", MaxListValues)
	}
	sort.Slice(*values, func(i, j int) bool { return (*values)[i] < (*values)[j] })
	for index, value := range *values {
		if !ValidPlatform(value) {
			return fmt.Errorf("selector.platforms contains invalid value %q", value)
		}
		if index > 0 && (*values)[index-1] == value {
			return fmt.Errorf("selector.platforms contains duplicate value %q", value)
		}
	}
	return nil
}

func normalizePhases(values *[]Phase) error {
	if *values != nil && len(*values) == 0 {
		return fmt.Errorf("selector.phases cannot be an empty present list")
	}
	if len(*values) > MaxListValues {
		return fmt.Errorf("selector.phases exceeds %d values", MaxListValues)
	}
	sort.Slice(*values, func(i, j int) bool { return (*values)[i] < (*values)[j] })
	for index, value := range *values {
		if !value.Valid() {
			return fmt.Errorf("selector.phases contains invalid value %q", value)
		}
		if index > 0 && (*values)[index-1] == value {
			return fmt.Errorf("selector.phases contains duplicate value %q", value)
		}
	}
	return nil
}

func selectedPhases(phases []Phase) []Phase {
	if len(phases) == 0 {
		return AllPhases()
	}
	return phases
}

func SafeLabel(value string) bool {
	return len(value) <= MaxSafeLabelBytes && safeLabelPattern.MatchString(value)
}

func ValidPlatform(value Platform) bool {
	for _, builtin := range BuiltinPlatforms() {
		if value == builtin {
			return true
		}
	}
	return customPlatformPattern.MatchString(string(value))
}

func ValidIdentity(value string) bool {
	return sha256IdentityPattern.MatchString(value) || hmacIdentityPattern.MatchString(value)
}

func validateToolName(value string, gateway bool) error {
	if value == "" || !utf8.ValidString(value) || len(value) > MaxToolNameBytes {
		return fmt.Errorf("tool must contain 1 to %d valid UTF-8 bytes", MaxToolNameBytes)
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return fmt.Errorf("tool contains a NUL or control character")
		}
	}
	if gateway && !gatewayToolNamePattern.MatchString(value) {
		return fmt.Errorf("mcp_stdio tool must match [A-Za-z0-9_.-]{1,%d}", MaxGatewayToolNameBytes)
	}
	return nil
}

func ToolIdentityKey(tool Tool) string {
	return string(tool.Transport) + "\x00" + string(tool.Platform) + "\x00" + tool.ServerLabel + "\x00" + tool.ServerFingerprint + "\x00" + tool.Tool
}

func (p *CompiledPlan) Plan() Plan {
	if p == nil {
		return Plan{}
	}
	return clonePlan(p.plan)
}

func (p *CompiledPlan) Rules() []CompiledRule {
	if p == nil {
		return nil
	}
	out := make([]CompiledRule, len(p.rules))
	for index := range p.rules {
		out[index] = cloneCompiledRule(p.rules[index])
	}
	return out
}

func cloneCompiledRule(source CompiledRule) CompiledRule {
	out := source
	out.Rule = *cloneRule(&source.Rule)
	out.Condition = cloneCompiledCondition(source.Condition)
	return out
}

func cloneCompiledCondition(source *CompiledCondition) *CompiledCondition {
	if source == nil {
		return nil
	}
	out := &CompiledCondition{Kind: source.Kind}
	out.Children = make([]*CompiledCondition, len(source.Children))
	for index := range source.Children {
		out.Children[index] = cloneCompiledCondition(source.Children[index])
	}
	if source.Predicate != nil {
		predicate := *source.Predicate
		predicate.Predicate = *clonePredicate(&source.Predicate.Predicate)
		predicate.Tokens = cloneSlice(source.Predicate.Tokens)
		if source.Predicate.Regex != nil {
			//lint:ignore SA1019 Copy deliberately isolates the compiled plan from callers that invoke Longest.
			predicate.Regex = source.Predicate.Regex.Copy()
		}
		if source.Predicate.Glob != nil {
			predicate.Glob = source.Predicate.Glob.clone()
		}
		if source.Predicate.URL != nil {
			urlConstraint := *source.Predicate.URL
			urlConstraint.Schemes = cloneSlice(source.Predicate.URL.Schemes)
			urlConstraint.Hosts = cloneSlice(source.Predicate.URL.Hosts)
			urlConstraint.Ports = cloneSlice(source.Predicate.URL.Ports)
			urlConstraint.PathPrefixes = cloneSlice(source.Predicate.URL.PathPrefixes)
			predicate.URL = &urlConstraint
		}
		predicate.CIDRs = cloneSlice(source.Predicate.CIDRs)
		if source.Predicate.Path != nil {
			pathConstraint := *source.Predicate.Path
			predicate.Path = &pathConstraint
		}
		out.Predicate = &predicate
	}
	return out
}

func compiledConditionLogicalBytes(condition *CompiledCondition) int {
	if condition == nil {
		return 0
	}
	total := 1
	for _, child := range condition.Children {
		total += compiledConditionLogicalBytes(child)
	}
	if condition.Predicate == nil {
		return total
	}
	for _, token := range condition.Predicate.Tokens {
		total += len(token)
	}
	if condition.Predicate.Regex != nil {
		total += len(condition.Predicate.Regex.String())
	}
	if condition.Predicate.Glob != nil {
		total += condition.Predicate.Glob.logicalBytes
	}
	for _, prefix := range condition.Predicate.CIDRs {
		total += len(prefix.String())
	}
	return total
}

func clonePlan(input Plan) Plan {
	out := input
	out.Tools = cloneSlice(input.Tools)
	for index := range out.Tools {
		out.Tools[index].Effect.PathFields = cloneSlice(input.Tools[index].Effect.PathFields)
	}
	out.Rules = cloneSlice(input.Rules)
	for index := range out.Rules {
		out.Rules[index] = *cloneRule(&input.Rules[index])
	}
	return out
}

func cloneRule(source *Rule) *Rule {
	if source == nil {
		return nil
	}
	out := *source
	cloneSelector(&out.Selector, source.Selector)
	out.When = cloneCondition(source.When)
	return &out
}

func cloneSelector(target *Selector, source Selector) {
	target.ToolIDs = cloneSlice(source.ToolIDs)
	target.Transports = cloneSlice(source.Transports)
	target.Platforms = cloneSlice(source.Platforms)
	target.ServerLabels = cloneSlice(source.ServerLabels)
	target.ServerFingerprints = cloneSlice(source.ServerFingerprints)
	target.Tools = cloneSlice(source.Tools)
	target.ToolContractDigests = cloneSlice(source.ToolContractDigests)
	target.Phases = cloneSlice(source.Phases)
}

func cloneCondition(source *Condition) *Condition {
	if source == nil {
		return nil
	}
	out := *source
	if source.All != nil {
		out.All = make([]Condition, len(source.All))
		for index := range source.All {
			cloned := cloneCondition(&source.All[index])
			out.All[index] = *cloned
		}
	}
	if source.Any != nil {
		out.Any = make([]Condition, len(source.Any))
		for index := range source.Any {
			cloned := cloneCondition(&source.Any[index])
			out.Any[index] = *cloned
		}
	}
	out.Not = cloneCondition(source.Not)
	if source.Predicate != nil {
		out.Predicate = clonePredicate(source.Predicate)
	}
	return &out
}

func clonePredicate(source *Predicate) *Predicate {
	if source == nil {
		return nil
	}
	out := *source
	if source.Value != nil {
		value := *source.Value
		out.Value = &value
	}
	return &out
}

func cloneSlice[T any](values []T) []T {
	if values == nil {
		return nil
	}
	return append([]T{}, values...)
}
