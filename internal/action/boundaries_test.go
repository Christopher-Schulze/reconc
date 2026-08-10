package action

import (
	"strconv"
	"strings"
	"testing"
)

func TestCompilePlanExactToolAndRuleCountBoundaries(t *testing.T) {
	t.Parallel()
	tools := make([]Tool, MaxTools)
	for index := range tools {
		tools[index] = hostTool("tool-"+strconv.Itoa(index), "tool-"+strconv.Itoa(index), EffectExternal, nil, "")
	}
	if _, err := CompilePlan(Plan{Tools: tools}); err != nil {
		t.Fatalf("exact tool boundary rejected: %v", err)
	}
	tools = append(tools, hostTool("tool-overflow", "tool-overflow", EffectExternal, nil, ""))
	if _, err := CompilePlan(Plan{Tools: tools}); err == nil {
		t.Fatal("tool count above exact boundary accepted")
	}

	rules := make([]Rule, MaxRules)
	for index := range rules {
		rules[index] = Rule{ID: "rule-" + strconv.Itoa(index), Decision: DecisionBlock, SourceIdentity: ".reconc.yml"}
	}
	if _, err := CompilePlan(Plan{Rules: rules}); err != nil {
		t.Fatalf("exact rule boundary rejected: %v", err)
	}
	rules = append(rules, Rule{ID: "rule-overflow", Decision: DecisionBlock, SourceIdentity: ".reconc.yml"})
	if _, err := CompilePlan(Plan{Rules: rules}); err == nil {
		t.Fatal("rule count above exact boundary accepted")
	}
}

func TestCompilePlanExactSelectorAndConditionBoundaries(t *testing.T) {
	t.Parallel()
	values := make([]string, MaxListValues)
	for index := range values {
		values[index] = "tool-" + strconv.Itoa(index)
	}
	boundaryRule := Rule{
		ID: "selector-boundary", Selector: Selector{Tools: values},
		Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
	}
	if _, err := CompilePlan(Plan{Rules: []Rule{boundaryRule}}); err != nil {
		t.Fatalf("exact selector boundary rejected: %v", err)
	}
	boundaryRule.Selector.Tools = append(boundaryRule.Selector.Tools, "tool-overflow")
	if _, err := CompilePlan(Plan{Rules: []Rule{boundaryRule}}); err == nil {
		t.Fatal("selector above exact boundary accepted")
	}

	acceptedDepth := nestedCondition(MaxConditionDepth)
	if _, err := CompilePlan(Plan{Rules: []Rule{{
		ID: "depth", When: acceptedDepth, Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
	}}}); err != nil {
		t.Fatalf("exact condition depth rejected: %v", err)
	}
	rejectedDepth := nestedCondition(MaxConditionDepth + 1)
	if _, err := CompilePlan(Plan{Rules: []Rule{{
		ID: "depth", When: rejectedDepth, Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
	}}}); err == nil {
		t.Fatal("condition above exact depth accepted")
	}

	children := make([]Condition, MaxConditionNodes-1)
	for index := range children {
		children[index] = contextExistsCondition()
	}
	nodeBoundary := &Condition{All: children}
	if _, err := CompilePlan(Plan{Rules: []Rule{{
		ID: "nodes", When: nodeBoundary, Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
	}}}); err != nil {
		t.Fatalf("exact condition-node boundary rejected: %v", err)
	}
	nodeBoundary.All = append(nodeBoundary.All, contextExistsCondition())
	if _, err := CompilePlan(Plan{Rules: []Rule{{
		ID: "nodes", When: nodeBoundary, Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
	}}}); err == nil {
		t.Fatal("condition above exact node boundary accepted")
	}
}

func TestActionScalarAndJSONResourceBoundaries(t *testing.T) {
	t.Parallel()
	if _, err := CompilePointer("/" + strings.Repeat("a", MaxPointerBytes-1)); err != nil {
		t.Fatalf("exact pointer boundary rejected: %v", err)
	}
	if _, err := CompilePointer("/" + strings.Repeat("a", MaxPointerBytes)); err == nil {
		t.Fatal("pointer above exact boundary accepted")
	}
	if _, err := String(strings.Repeat("a", MaxJSONStringBytes)); err != nil {
		t.Fatalf("exact string boundary rejected: %v", err)
	}
	if _, err := String(strings.Repeat("a", MaxJSONStringBytes+1)); err == nil {
		t.Fatal("string above exact boundary accepted")
	}

	items := make([]Value, MaxJSONItems)
	for index := range items {
		items[index] = Null()
	}
	if _, err := Array(items); err != nil {
		t.Fatalf("exact JSON item boundary rejected: %v", err)
	}
	if _, err := Array(append(items, Null())); err == nil {
		t.Fatal("JSON item count above exact boundary accepted")
	}

	accepted := strings.Repeat("[", MaxJSONDepth) + "null" + strings.Repeat("]", MaxJSONDepth)
	if _, err := ParseJSON([]byte(accepted)); err != nil {
		t.Fatalf("exact JSON depth rejected: %v", err)
	}
	rejected := "[" + accepted + "]"
	if _, err := ParseJSON([]byte(rejected)); err == nil {
		t.Fatal("JSON above exact depth accepted")
	}
}

func TestActionTextBoundaries(t *testing.T) {
	t.Parallel()
	acceptedHost := hostTool(strings.Repeat("a", MaxSafeLabelBytes), strings.Repeat("x", MaxToolNameBytes), EffectExternal, nil, "")
	if _, err := CompilePlan(Plan{Tools: []Tool{acceptedHost}}); err != nil {
		t.Fatalf("exact label or host-tool boundary rejected: %v", err)
	}
	rejectedHost := acceptedHost
	rejectedHost.Tool += "x"
	if _, err := CompilePlan(Plan{Tools: []Tool{rejectedHost}}); err == nil {
		t.Fatal("host tool above exact boundary accepted")
	}

	acceptedGateway := Tool{
		ID: "gateway", Transport: TransportMCPStdio, ServerLabel: "server",
		Tool: strings.Repeat("x", MaxGatewayToolNameBytes), Effect: Effect{Kind: EffectExternal},
		Origin: OriginActions, SourceIdentity: ".reconc.yml",
	}
	if _, err := CompilePlan(Plan{Tools: []Tool{acceptedGateway}}); err != nil {
		t.Fatalf("exact gateway-tool boundary rejected: %v", err)
	}
	acceptedGateway.Tool += "x"
	if _, err := CompilePlan(Plan{Tools: []Tool{acceptedGateway}}); err == nil {
		t.Fatal("gateway tool above exact boundary accepted")
	}

	acceptedRule := Rule{
		ID: "message", Decision: DecisionBlock, Message: strings.Repeat("m", MaxRuleMessageBytes),
		SourceIdentity: strings.Repeat("s", MaxPointerBytes),
	}
	if _, err := CompilePlan(Plan{Rules: []Rule{acceptedRule}}); err != nil {
		t.Fatalf("exact message or source boundary rejected: %v", err)
	}
	acceptedRule.Message += "m"
	if _, err := CompilePlan(Plan{Rules: []Rule{acceptedRule}}); err == nil {
		t.Fatal("message above exact boundary accepted")
	}
}

func nestedCondition(depth int) *Condition {
	condition := contextExistsCondition()
	for level := 1; level < depth; level++ {
		child := condition
		condition = Condition{Not: &child}
	}
	return &condition
}

func contextExistsCondition() Condition {
	return Condition{Predicate: &Predicate{Source: SourceContext, Pointer: "", Op: OperatorExists}}
}
