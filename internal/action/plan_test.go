package action

import (
	"strings"
	"testing"
)

func TestCompilePlanCanonicalizesDefaultsAndOrdering(t *testing.T) {
	t.Parallel()
	plan := Plan{
		Tools: []Tool{
			hostTool("z-tool", "write", EffectRepositoryWrite, []string{"/z", "/a"}, ""),
			hostTool("a-tool", "read", EffectRepositoryRead, []string{"/path"}, ""),
		},
		Rules: []Rule{
			{ID: "z-rule", Selector: Selector{ToolIDs: []string{"z-tool"}}, Decision: DecisionBlock, SourceIdentity: ".reconc.yml"},
			{ID: "a-rule", Selector: Selector{ToolIDs: []string{"a-tool"}}, Decision: DecisionWarn, SourceIdentity: ".reconc.yml"},
		},
	}
	compiled, err := CompilePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	got := compiled.Plan()
	if got.FormatVersion != PlanFormatVersion || got.Defaults != FrozenDefaults() {
		t.Fatalf("canonical metadata = %#v", got)
	}
	if got.Tools[0].ID != "a-tool" || got.Tools[1].Effect.PathFields[0] != "/a" {
		t.Fatalf("canonical tools = %#v", got.Tools)
	}
	if got.Rules[0].ID != "a-rule" || got.Rules[0].Cache != CacheExact || got.Rules[0].OnIndeterminate != DecisionBlock {
		t.Fatalf("canonical rules = %#v", got.Rules)
	}
}

func TestCompilePlanRejectsDuplicateExactOwnership(t *testing.T) {
	t.Parallel()
	first := hostTool("first", "same", EffectExternal, nil, "")
	second := first
	second.ID = "second"
	if _, err := CompilePlan(Plan{Tools: []Tool{first, second}}); err == nil || !strings.Contains(err.Error(), "same exact tool") {
		t.Fatalf("duplicate ownership error = %v", err)
	}
}

func TestCompilePlanCompilesCorePredicates(t *testing.T) {
	t.Parallel()
	stringValue, _ := String(`^prod-[0-9]+$`)
	cidrA, _ := String("10.0.0.0/8")
	cidrB, _ := String("2001:db8::/32")
	cidrs, _ := Array([]Value{cidrB, cidrA})
	tests := []struct {
		name      string
		predicate Predicate
		verify    func(*testing.T, *CompiledPredicate)
	}{
		{
			name:      "regex",
			predicate: Predicate{Source: SourceArguments, Pointer: "/name", Op: OperatorRegex, Value: &stringValue},
			verify: func(t *testing.T, predicate *CompiledPredicate) {
				if !predicate.Regex.MatchString("prod-42") || predicate.Regex.MatchString("xprod-42") {
					t.Fatal("regex is not a full-string matcher")
				}
			},
		},
		{
			name:      "cidr",
			predicate: Predicate{Source: SourceArguments, Pointer: "/ip", Op: OperatorCIDR, Value: &cidrs},
			verify: func(t *testing.T, predicate *CompiledPredicate) {
				if len(predicate.CIDRs) != 2 || predicate.CIDRs[0].String() != "10.0.0.0/8" {
					t.Fatalf("CIDRs = %#v", predicate.CIDRs)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			condition := Condition{Predicate: &test.predicate}
			plan := Plan{Rules: []Rule{{
				ID: "rule", Selector: Selector{Phases: []Phase{PhasePreCall}}, When: &condition,
				Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
			}}}
			compiled, err := CompilePlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			test.verify(t, compiled.rules[0].Condition.Predicate)
		})
	}
}

func TestCompilePlanRejectsUnsafeConditionShapes(t *testing.T) {
	t.Parallel()
	operand, _ := String("x")
	tests := []struct {
		name string
		rule Rule
	}{
		{
			name: "allow from arguments",
			rule: Rule{ID: "rule", Selector: Selector{Phases: []Phase{PhasePreCall}}, Decision: DecisionAllow, SourceIdentity: "source", When: &Condition{Predicate: &Predicate{Source: SourceArguments, Pointer: "/x", Op: OperatorEqual, Value: &operand}}},
		},
		{
			name: "phase mismatch",
			rule: Rule{ID: "rule", Selector: Selector{Phases: []Phase{PhasePostResult}}, Decision: DecisionBlock, SourceIdentity: "source", When: &Condition{Predicate: &Predicate{Source: SourceArguments, Pointer: "/x", Op: OperatorEqual, Value: &operand}}},
		},
		{
			name: "empty selector",
			rule: Rule{ID: "rule", Selector: Selector{Tools: []string{}}, Decision: DecisionBlock, SourceIdentity: "source"},
		},
		{
			name: "multiple nodes",
			rule: Rule{ID: "rule", Decision: DecisionBlock, SourceIdentity: "source", When: &Condition{All: []Condition{{Predicate: &Predicate{Source: SourceContext, Pointer: "", Op: OperatorExists}}}, Predicate: &Predicate{Source: SourceContext, Pointer: "", Op: OperatorExists}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := CompilePlan(Plan{Rules: []Rule{test.rule}}); err == nil {
				t.Fatal("expected compile rejection")
			}
		})
	}
}

func TestCompilePointerRFC6901(t *testing.T) {
	t.Parallel()
	tokens, err := CompilePointer("/a~1b/~0/c")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tokens, "|") != "a/b|~|c" {
		t.Fatalf("tokens = %#v", tokens)
	}
	for _, pointer := range []string{"relative", "/bad~", "/bad~2"} {
		if _, err := CompilePointer(pointer); err == nil {
			t.Fatalf("pointer %q unexpectedly accepted", pointer)
		}
	}
}

func TestCompiledPlanRulesAreDefensiveCopies(t *testing.T) {
	t.Parallel()
	pattern, _ := String("prod-*")
	condition := Condition{Predicate: &Predicate{
		Source: SourceArguments, Pointer: "/name", Op: OperatorGlob, Value: &pattern,
	}}
	compiled, err := CompilePlan(Plan{Rules: []Rule{{
		ID: "block-production", Selector: Selector{Phases: []Phase{PhasePreCall}},
		When: &condition, Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
	}}})
	if err != nil {
		t.Fatal(err)
	}

	first := compiled.Rules()
	first[0].Rule.ID = "mutated"
	first[0].Condition.Kind = ConditionAny
	first[0].Condition.Predicate.Tokens[0] = "mutated"
	first[0].Condition.Predicate.Glob.pattern = "*"

	second := compiled.Rules()
	if second[0].Rule.ID != "block-production" || second[0].Condition.Kind != ConditionPredicate ||
		second[0].Condition.Predicate.Tokens[0] != "name" || second[0].Condition.Predicate.Glob.pattern != "prod-*" {
		t.Fatalf("compiled plan was mutated through Rules(): %#v", second[0])
	}
}

func TestCompilePlanNormalizesUnambiguousURLAndPathConstraints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		check func(*testing.T, *CompiledPredicate)
	}{
		{
			name:  "URL",
			value: `{"schemes":["https"],"hosts":["EXAMPLE.TEST."],"path_prefixes":["/caf%C3%A9"],"allow_query":false,"allow_ip_literals":false}`,
			check: func(t *testing.T, predicate *CompiledPredicate) {
				if predicate.URL.Hosts[0] != "example.test" || predicate.URL.PathPrefixes[0] != "/café" {
					t.Fatalf("URL constraint = %#v", predicate.URL)
				}
			},
		},
		{
			name:  "Windows UNC",
			value: `{"style":"windows","base":"\\\\Server\\Share\\dir","case_sensitive":false}`,
			check: func(t *testing.T, predicate *CompiledPredicate) {
				if predicate.Path.Base != "//Server/Share/dir" {
					t.Fatalf("UNC base = %q", predicate.Path.Base)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			operand, err := ParseJSON([]byte(test.value))
			if err != nil {
				t.Fatal(err)
			}
			op := OperatorURL
			if test.name == "Windows UNC" {
				op = OperatorPathWithin
			}
			when := Condition{Predicate: &Predicate{Source: SourceArguments, Pointer: "/value", Op: op, Value: &operand}}
			plan, err := CompilePlan(Plan{Rules: []Rule{{
				ID: "rule", Selector: Selector{Phases: []Phase{PhasePreCall}}, When: &when,
				Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
			}}})
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, plan.rules[0].Condition.Predicate)
		})
	}
}

func TestCompilePlanRejectsAmbiguousURLAndPathConstraints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		op    Operator
		value string
	}{
		{name: "encoded slash", op: OperatorURL, value: `{"schemes":["https"],"hosts":["example.test"],"path_prefixes":["/a%2Fb"],"allow_query":false,"allow_ip_literals":false}`},
		{name: "encoded backslash", op: OperatorURL, value: `{"schemes":["https"],"hosts":["example.test"],"path_prefixes":["/a%5cb"],"allow_query":false,"allow_ip_literals":false}`},
		{name: "double encoded segment", op: OperatorURL, value: `{"schemes":["https"],"hosts":["example.test"],"path_prefixes":["/a%252fb"],"allow_query":false,"allow_ip_literals":false}`},
		{name: "encoded dot segment", op: OperatorURL, value: `{"schemes":["https"],"hosts":["example.test"],"path_prefixes":["/%2e%2e"],"allow_query":false,"allow_ip_literals":false}`},
		{name: "malformed IPv4 literal", op: OperatorURL, value: `{"schemes":["https"],"hosts":["127.000.0.1"],"allow_query":false,"allow_ip_literals":true}`},
		{name: "repository parent", op: OperatorPathWithin, value: `{"style":"repository","base":"a/../b","case_sensitive":true}`},
		{name: "POSIX repeated separator", op: OperatorPathWithin, value: `{"style":"posix","base":"/a//b","case_sensitive":true}`},
		{name: "Windows repeated separator", op: OperatorPathWithin, value: `{"style":"windows","base":"C://a","case_sensitive":false}`},
		{name: "Windows alternate stream", op: OperatorPathWithin, value: `{"style":"windows","base":"C:/a:file","case_sensitive":false}`},
		{name: "Windows incomplete UNC", op: OperatorPathWithin, value: `{"style":"windows","base":"//server","case_sensitive":false}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			operand, err := ParseJSON([]byte(test.value))
			if err != nil {
				t.Fatal(err)
			}
			when := Condition{Predicate: &Predicate{Source: SourceArguments, Pointer: "/value", Op: test.op, Value: &operand}}
			_, err = CompilePlan(Plan{Rules: []Rule{{
				ID: "rule", Selector: Selector{Phases: []Phase{PhasePreCall}}, When: &when,
				Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
			}}})
			if err == nil {
				t.Fatal("ambiguous constraint unexpectedly compiled")
			}
		})
	}
}

func TestCompilePlanReportsOriginalInvalidURLHost(t *testing.T) {
	t.Parallel()
	operand := testValue(t, `{"schemes":["https"],"hosts":["bad host"],"allow_query":false,"allow_ip_literals":false}`)
	when := Condition{Predicate: &Predicate{Source: SourceArguments, Pointer: "/value", Op: OperatorURL, Value: &operand}}
	_, err := CompilePlan(Plan{Rules: []Rule{{
		ID: "rule", Selector: Selector{Phases: []Phase{PhasePreCall}}, When: &when,
		Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
	}}})
	if err == nil || !strings.Contains(err.Error(), `url host "bad host" is invalid`) {
		t.Fatalf("error = %v, want original invalid host", err)
	}
}

func hostTool(id, name string, effect EffectKind, paths []string, command string) Tool {
	return Tool{
		ID: id, Transport: TransportHostMCP, Platform: PlatformClaudeCode,
		Tool: name, Effect: Effect{Kind: effect, PathFields: paths, CommandField: command},
		Origin: OriginActions, SourceIdentity: ".reconc.yml",
	}
}
