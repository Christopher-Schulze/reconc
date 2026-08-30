package action

import (
	"encoding/json"
	"fmt"
	"math"
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
	if plan.Budgets == nil {
		plan.Budgets = []Budget{}
	}
	if plan.Approvals == nil {
		plan.Approvals = []ApprovalDisclosure{}
	}
	if plan.Detectors == nil {
		plan.Detectors = []DetectorPolicy{}
	}
	if plan.Ledger == nil {
		plan.Ledger = &LedgerPolicy{}
	}
	if err := normalizeLedgerPolicy(plan.Ledger, plan.Tools); err != nil {
		return nil, err
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
	if len(plan.Budgets) > MaxBudgets {
		return nil, fmt.Errorf("actions.budgets contains %d declarations; maximum is %d", len(plan.Budgets), MaxBudgets)
	}
	if len(plan.Approvals) > MaxApprovalDisclosures {
		return nil, fmt.Errorf("actions.approvals contains %d declarations; maximum is %d", len(plan.Approvals), MaxApprovalDisclosures)
	}
	if len(plan.Detectors) > MaxDetectors {
		return nil, fmt.Errorf("actions.detectors contains %d declarations; maximum is %d", len(plan.Detectors), MaxDetectors)
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
		for previous := 0; previous < index; previous++ {
			if toolDeclarationsOverlap(plan.Tools[previous], *tool) {
				return nil, fmt.Errorf("actions.tools %q and %q own overlapping fingerprint-bound tool declarations", plan.Tools[previous].ID, tool.ID)
			}
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
	for index := range plan.Budgets {
		if err := normalizeBudget(&plan.Budgets[index], plan.Tools, toolByID); err != nil {
			return nil, fmt.Errorf("actions.budgets[%d]: %w", index, err)
		}
	}
	sort.Slice(plan.Budgets, func(i, j int) bool { return plan.Budgets[i].ID < plan.Budgets[j].ID })
	for index := 1; index < len(plan.Budgets); index++ {
		if plan.Budgets[index-1].ID == plan.Budgets[index].ID {
			return nil, fmt.Errorf("actions.budgets contains duplicate id %q", plan.Budgets[index].ID)
		}
	}
	for index := range plan.Approvals {
		if err := normalizeApprovalDisclosure(&plan.Approvals[index], plan.Tools, toolByID); err != nil {
			return nil, fmt.Errorf("actions.approvals[%d]: %w", index, err)
		}
	}
	sort.Slice(plan.Approvals, func(i, j int) bool { return plan.Approvals[i].ID < plan.Approvals[j].ID })
	for index := 1; index < len(plan.Approvals); index++ {
		if plan.Approvals[index-1].ID == plan.Approvals[index].ID {
			return nil, fmt.Errorf("actions.approvals contains duplicate id %q", plan.Approvals[index].ID)
		}
	}
	compiledDetectors := make([]CompiledDetectorPolicy, len(plan.Detectors))
	for index := range plan.Detectors {
		compiled, err := normalizeDetectorPolicy(&plan.Detectors[index], plan.Tools, toolByID)
		if err != nil {
			return nil, fmt.Errorf("actions.detectors[%d]: %w", index, err)
		}
		compiledDetectors[index] = compiled
	}
	sort.Slice(compiledDetectors, func(i, j int) bool {
		return compiledDetectors[i].Policy.ID < compiledDetectors[j].Policy.ID
	})
	plan.Detectors = make([]DetectorPolicy, len(compiledDetectors))
	for index := range compiledDetectors {
		if index > 0 && compiledDetectors[index-1].Policy.ID == compiledDetectors[index].Policy.ID {
			return nil, fmt.Errorf("actions.detectors contains duplicate id %q", compiledDetectors[index].Policy.ID)
		}
		plan.Detectors[index] = compiledDetectors[index].Policy
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encode canonical action plan: %w", err)
	}
	logicalBytes, err := extendCompiledPlanBytes(0, len(body))
	if err != nil {
		return nil, err
	}
	for _, rule := range compiledRules {
		logicalBytes, err = extendCompiledPlanBytes(logicalBytes, compiledConditionLogicalBytes(rule.Condition))
		if err != nil {
			return nil, err
		}
	}
	return &CompiledPlan{
		plan: plan, toolByID: toolByID, toolByExact: toolByExact,
		rules: compiledRules, budgets: cloneSlice(plan.Budgets),
		approvals: cloneSlice(plan.Approvals), detectors: compiledDetectors,
	}, nil
}

func extendCompiledPlanBytes(current, additional int) (int, error) {
	if current < 0 || additional < 0 || current > MaxCompiledPlanBytes ||
		additional > MaxCompiledPlanBytes-current {
		return 0, fmt.Errorf("compiled action plan exceeds %d logical bytes", MaxCompiledPlanBytes)
	}
	return current + additional, nil
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
	if tool.LedgerNameSafe && tool.Transport != TransportMCPStdio {
		return fmt.Errorf("ledger_name_safe is valid only for mcp_stdio")
	}
	if !tool.Effect.Kind.Valid() {
		return fmt.Errorf("effect.kind is invalid")
	}
	if tool.CostUnits != nil && *tool.CostUnits > math.MaxInt64 {
		return fmt.Errorf("cost_units must be between 0 and %d", int64(math.MaxInt64))
	}
	if tool.MaxResultBytes > MaxArgumentBytes {
		return fmt.Errorf("max_result_bytes must be omitted (zero in Go) or between 1 and %d", MaxArgumentBytes)
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

func normalizeLedgerPolicy(policy *LedgerPolicy, tools []Tool) error {
	if policy.Mode == "" {
		policy.Mode = LedgerRequired
	}
	if !policy.Mode.Valid() {
		return fmt.Errorf("actions.ledger.mode must be required, best_effort, or off")
	}
	if policy.ToolIdentity == "" {
		policy.ToolIdentity = LedgerDeclarationID
	}
	if !policy.ToolIdentity.Valid() {
		return fmt.Errorf("actions.ledger.tool_identity must be declaration_id, exact_name, or keyed_name")
	}
	if policy.SelectedFields == nil {
		policy.SelectedFields = []LedgerField{}
	}
	if len(policy.SelectedFields) > MaxLedgerFields {
		return fmt.Errorf("actions.ledger.selected_fields contains %d declarations; maximum is %d", len(policy.SelectedFields), MaxLedgerFields)
	}
	for index, field := range policy.SelectedFields {
		if field.Source != SourceArguments && field.Source != SourceResult {
			return fmt.Errorf("actions.ledger.selected_fields[%d].source must be arguments or result", index)
		}
		if _, err := CompilePointer(field.Pointer); err != nil {
			return fmt.Errorf("actions.ledger.selected_fields[%d].pointer: %w", index, err)
		}
	}
	sort.Slice(policy.SelectedFields, func(i, j int) bool {
		if policy.SelectedFields[i].Source != policy.SelectedFields[j].Source {
			return policy.SelectedFields[i].Source < policy.SelectedFields[j].Source
		}
		return policy.SelectedFields[i].Pointer < policy.SelectedFields[j].Pointer
	})
	for index := 1; index < len(policy.SelectedFields); index++ {
		if policy.SelectedFields[index-1] == policy.SelectedFields[index] {
			return fmt.Errorf("actions.ledger.selected_fields contains duplicate %s pointer %q", policy.SelectedFields[index].Source, policy.SelectedFields[index].Pointer)
		}
	}
	if policy.ToolIdentity == LedgerExactName {
		gatewayTools := 0
		for _, tool := range tools {
			if tool.Transport != TransportMCPStdio {
				continue
			}
			gatewayTools++
			if !tool.LedgerNameSafe {
				return fmt.Errorf("actions.ledger.tool_identity exact_name requires ledger_name_safe on mcp_stdio tool %q", tool.ID)
			}
		}
		if gatewayTools == 0 {
			return fmt.Errorf("actions.ledger.tool_identity exact_name requires at least one mcp_stdio tool")
		}
	}
	return nil
}

func normalizeBudget(budget *Budget, tools []Tool, toolByID map[string]int) error {
	if !SafeLabel(budget.ID) {
		return fmt.Errorf("id must be a lower-kebab label of 1 to %d bytes", MaxSafeLabelBytes)
	}
	if selectorEmpty(budget.Selector) {
		return fmt.Errorf("selector must contain at least one exact constraint")
	}
	if err := normalizeSelector(&budget.Selector, toolByID); err != nil {
		return err
	}
	if len(budget.Selector.Phases) > 0 &&
		(len(budget.Selector.Phases) != 1 || budget.Selector.Phases[0] != PhasePreCall) {
		return fmt.Errorf("selector.phases may contain only pre_call for a dispatch budget")
	}
	if budget.Limits.Empty() {
		return fmt.Errorf("limits must contain at least one dimension")
	}
	if err := validateBudgetLimits(budget.Limits); err != nil {
		return err
	}
	if !budget.Reset.Valid() {
		return fmt.Errorf("reset must be never, operator_run, operator_session, or fixed_window")
	}
	if budget.Reset == BudgetResetFixedWindow {
		if budget.WindowSeconds == 0 || budget.WindowSeconds > 86400 {
			return fmt.Errorf("fixed_window requires window_seconds between 1 and 86400")
		}
	} else if budget.WindowSeconds != 0 {
		return fmt.Errorf("window_seconds is valid only with fixed_window")
	}
	if budget.Limits.RateWindow != 0 && budget.Reset != BudgetResetFixedWindow {
		return fmt.Errorf("rate_window requires fixed_window reset")
	}
	if budget.OnExhaustion != DecisionBlock {
		return fmt.Errorf("on_exhaustion must be block")
	}
	if strings.TrimSpace(budget.SourceIdentity) == "" ||
		!utf8.ValidString(budget.SourceIdentity) || len(budget.SourceIdentity) > MaxPointerBytes {
		return fmt.Errorf("source_identity must be a non-empty UTF-8 identity of at most %d bytes", MaxPointerBytes)
	}
	matched := 0
	for _, tool := range tools {
		if !selectorCanMatchTool(budget.Selector, tool) {
			continue
		}
		matched++
		if tool.Transport != TransportMCPStdio {
			return fmt.Errorf("budget selects host_mcp tool %q, which cannot be reserved before host dispatch", tool.ID)
		}
		if budget.Limits.ResultBytes != 0 && tool.MaxResultBytes == 0 {
			return fmt.Errorf("result_bytes requires max_result_bytes on selected tool %q", tool.ID)
		}
		if budget.Limits.CostUnits != 0 && tool.CostUnits == nil {
			return fmt.Errorf("cost_units requires cost_units on selected tool %q", tool.ID)
		}
	}
	if matched == 0 {
		return fmt.Errorf("selector cannot match any declared tool")
	}
	return nil
}

func normalizeApprovalDisclosure(
	disclosure *ApprovalDisclosure,
	tools []Tool,
	toolByID map[string]int,
) error {
	if !SafeLabel(disclosure.ID) {
		return fmt.Errorf("id must be a lower-kebab label of 1 to %d bytes", MaxSafeLabelBytes)
	}
	if selectorEmpty(disclosure.Selector) {
		return fmt.Errorf("selector must contain at least one exact constraint")
	}
	if err := normalizeSelector(&disclosure.Selector, toolByID); err != nil {
		return err
	}
	for _, phase := range disclosure.Selector.Phases {
		if phase != PhasePreCall && phase != PhasePostResult {
			return fmt.Errorf("selector.phases may contain only pre_call or post_result")
		}
	}
	if len(disclosure.SelectedArguments) == 0 {
		return fmt.Errorf("selected_arguments must contain at least one JSON pointer")
	}
	if err := normalizePointers(&disclosure.SelectedArguments, "selected_arguments", true); err != nil {
		return err
	}
	if strings.TrimSpace(disclosure.SourceIdentity) == "" ||
		!utf8.ValidString(disclosure.SourceIdentity) || len(disclosure.SourceIdentity) > MaxPointerBytes {
		return fmt.Errorf("source_identity must be a non-empty UTF-8 identity of at most %d bytes", MaxPointerBytes)
	}
	matched := 0
	for _, tool := range tools {
		if !selectorCanMatchTool(disclosure.Selector, tool) {
			continue
		}
		matched++
		if tool.Transport != TransportMCPStdio {
			return fmt.Errorf("approval disclosure selects host_mcp tool %q without a dispatch gateway", tool.ID)
		}
	}
	if matched == 0 {
		return fmt.Errorf("selector cannot match any declared tool")
	}
	return nil
}

func validateBudgetLimits(limits BudgetLimits) error {
	values := []struct {
		name  string
		value uint64
	}{
		{"call_count", limits.CallCount}, {"denied_count", limits.DeniedCount},
		{"approval_count", limits.ApprovalCount}, {"argument_bytes", limits.ArgumentBytes},
		{"result_bytes", limits.ResultBytes}, {"cost_units", limits.CostUnits},
		{"concurrent", limits.Concurrent}, {"rate_window", limits.RateWindow},
	}
	for _, value := range values {
		if value.value > math.MaxInt64 {
			return fmt.Errorf("limits.%s exceeds %d", value.name, int64(math.MaxInt64))
		}
	}
	if limits.Concurrent > MaxConcurrentCalls {
		return fmt.Errorf("limits.concurrent exceeds gateway maximum %d", MaxConcurrentCalls)
	}
	return nil
}

func selectorEmpty(selector Selector) bool {
	return len(selector.ToolIDs) == 0 && len(selector.Transports) == 0 &&
		len(selector.Platforms) == 0 && len(selector.ServerLabels) == 0 &&
		len(selector.ServerFingerprints) == 0 && len(selector.Tools) == 0 &&
		len(selector.ToolContractDigests) == 0 && len(selector.Phases) == 0
}

func selectorCanMatchTool(selector Selector, tool Tool) bool {
	// ToolContractDigests is an exact runtime constraint over the discovered
	// MCP contract. Tool declarations intentionally contain no contract digest,
	// so compile-time admission proves only declaration-owned dimensions.
	return stringListed(selector.ToolIDs, tool.ID) &&
		transportListed(selector.Transports, tool.Transport) &&
		platformListed(selector.Platforms, tool.Platform) &&
		stringListed(selector.ServerLabels, tool.ServerLabel) &&
		stringListed(selector.ServerFingerprints, tool.ServerFingerprint) &&
		stringListed(selector.Tools, tool.Tool)
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

func ValidKeyedIdentity(value string) bool {
	return hmacIdentityPattern.MatchString(value)
}

func KeyedIdentityKeyID(value string) (string, bool) {
	const prefix = "hmac-sha256:v1:"
	if !ValidKeyedIdentity(value) {
		return "", false
	}
	remainder := strings.TrimPrefix(value, prefix)
	separator := strings.IndexByte(remainder, ':')
	if separator <= 0 {
		return "", false
	}
	return remainder[:separator], true
}

func ValidSHA256Identity(value string) bool {
	return sha256IdentityPattern.MatchString(value)
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

func toolDeclarationsOverlap(left, right Tool) bool {
	return left.Transport == right.Transport && left.Platform == right.Platform &&
		left.ServerLabel == right.ServerLabel && left.Tool == right.Tool &&
		(left.ServerFingerprint == "" || right.ServerFingerprint == "" ||
			left.ServerFingerprint == right.ServerFingerprint)
}

func lookupToolIndex(index map[string]int, tool Tool) (int, bool) {
	if matched, ok := index[ToolIdentityKey(tool)]; ok {
		return matched, true
	}
	if tool.ServerFingerprint == "" {
		return 0, false
	}
	tool.ServerFingerprint = ""
	matched, ok := index[ToolIdentityKey(tool)]
	return matched, ok
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

// Budgets returns canonical immutable budget declarations in stable ID order.
func (p *CompiledPlan) Budgets() []Budget {
	if p == nil {
		return nil
	}
	out := cloneSlice(p.budgets)
	for index := range out {
		cloneSelector(&out[index].Selector, p.budgets[index].Selector)
	}
	return out
}

// Detectors returns canonical detector declarations in stable ID order.
func (p *CompiledPlan) Detectors() []DetectorPolicy {
	if p == nil {
		return nil
	}
	out := make([]DetectorPolicy, len(p.detectors))
	for index := range p.detectors {
		out[index] = cloneDetectorPolicy(p.detectors[index].Policy)
	}
	return out
}

// Tool returns one defensively copied canonical declaration by stable ID.
func (p *CompiledPlan) Tool(id string) (Tool, bool) {
	if p == nil {
		return Tool{}, false
	}
	index, ok := p.toolByID[id]
	if !ok {
		return Tool{}, false
	}
	tool := p.plan.Tools[index]
	tool.Effect.PathFields = cloneSlice(tool.Effect.PathFields)
	if tool.CostUnits != nil {
		value := *tool.CostUnits
		tool.CostUnits = &value
	}
	return tool, true
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
		if input.Tools[index].CostUnits != nil {
			value := *input.Tools[index].CostUnits
			out.Tools[index].CostUnits = &value
		}
	}
	out.Rules = cloneSlice(input.Rules)
	for index := range out.Rules {
		out.Rules[index] = *cloneRule(&input.Rules[index])
	}
	out.Budgets = cloneSlice(input.Budgets)
	for index := range out.Budgets {
		cloneSelector(&out.Budgets[index].Selector, input.Budgets[index].Selector)
	}
	out.Approvals = cloneSlice(input.Approvals)
	for index := range out.Approvals {
		cloneSelector(&out.Approvals[index].Selector, input.Approvals[index].Selector)
		out.Approvals[index].SelectedArguments = cloneSlice(input.Approvals[index].SelectedArguments)
	}
	out.Detectors = make([]DetectorPolicy, len(input.Detectors))
	for index := range input.Detectors {
		out.Detectors[index] = cloneDetectorPolicy(input.Detectors[index])
	}
	if input.Ledger != nil {
		ledger := *input.Ledger
		ledger.SelectedFields = cloneSlice(input.Ledger.SelectedFields)
		out.Ledger = &ledger
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
