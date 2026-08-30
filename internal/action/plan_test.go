package action

import (
	"math"
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
	if got.Ledger == nil || got.Ledger.Mode != LedgerRequired ||
		got.Ledger.ToolIdentity != LedgerDeclarationID || len(got.Ledger.SelectedFields) != 0 {
		t.Fatalf("canonical ledger = %#v", got.Ledger)
	}
	if got.Tools[0].ID != "a-tool" || got.Tools[1].Effect.PathFields[0] != "/a" {
		t.Fatalf("canonical tools = %#v", got.Tools)
	}
	if got.Rules[0].ID != "a-rule" || got.Rules[0].Cache != CacheExact || got.Rules[0].OnIndeterminate != DecisionBlock {
		t.Fatalf("canonical rules = %#v", got.Rules)
	}
}

func TestCompilePlanCanonicalizesAndDefensivelyCopiesLedgerPolicy(t *testing.T) {
	t.Parallel()
	tool := gatewayTool("query", "query")
	tool.LedgerNameSafe = true
	compiled, err := CompilePlan(Plan{
		Tools: []Tool{tool},
		Ledger: &LedgerPolicy{
			Mode: LedgerBestEffort, ToolIdentity: LedgerExactName,
			SelectedFields: []LedgerField{
				{Source: SourceResult, Pointer: "/rows"},
				{Source: SourceArguments, Pointer: "/database"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := compiled.Plan()
	if first.Ledger == nil || first.Ledger.SelectedFields[0].Source != SourceArguments ||
		first.Ledger.SelectedFields[1].Pointer != "/rows" {
		t.Fatalf("canonical ledger = %#v", first.Ledger)
	}
	first.Ledger.SelectedFields[0].Pointer = "/mutated"
	second := compiled.Plan()
	if second.Ledger.SelectedFields[0].Pointer != "/database" {
		t.Fatal("compiled ledger mutated through Plan")
	}
}

func TestCompilePlanRejectsInvalidLedgerPolicy(t *testing.T) {
	t.Parallel()
	tool := gatewayTool("query", "query")
	valid := LedgerPolicy{Mode: LedgerRequired, ToolIdentity: LedgerDeclarationID, SelectedFields: []LedgerField{{Source: SourceArguments, Pointer: "/database"}}}
	tests := []struct {
		name   string
		mutate func(*LedgerPolicy, *Tool)
		want   string
	}{
		{name: "mode", mutate: func(policy *LedgerPolicy, _ *Tool) { policy.Mode = "sometimes" }, want: "mode"},
		{name: "tool identity", mutate: func(policy *LedgerPolicy, _ *Tool) { policy.ToolIdentity = "raw" }, want: "tool_identity"},
		{name: "context source", mutate: func(policy *LedgerPolicy, _ *Tool) { policy.SelectedFields[0].Source = SourceContext }, want: "arguments or result"},
		{name: "invalid pointer", mutate: func(policy *LedgerPolicy, _ *Tool) { policy.SelectedFields[0].Pointer = "relative" }, want: "pointer"},
		{name: "duplicate", mutate: func(policy *LedgerPolicy, _ *Tool) {
			policy.SelectedFields = append(policy.SelectedFields, policy.SelectedFields[0])
		}, want: "duplicate"},
		{name: "too many", mutate: func(policy *LedgerPolicy, _ *Tool) { policy.SelectedFields = make([]LedgerField, MaxLedgerFields+1) }, want: "maximum"},
		{name: "unsafe exact name", mutate: func(policy *LedgerPolicy, _ *Tool) { policy.ToolIdentity = LedgerExactName }, want: "ledger_name_safe"},
		{name: "host name disclosure", mutate: func(_ *LedgerPolicy, tool *Tool) {
			*tool = hostTool("query", "query", EffectExternal, nil, "")
			tool.LedgerNameSafe = true
		}, want: "only for mcp_stdio"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := valid
			policy.SelectedFields = append([]LedgerField(nil), valid.SelectedFields...)
			candidate := tool
			test.mutate(&policy, &candidate)
			_, err := CompilePlan(Plan{Tools: []Tool{candidate}, Ledger: &policy})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompilePlanCanonicalizesAndDefensivelyCopiesBudgets(t *testing.T) {
	t.Parallel()
	tool := gatewayTool("query", "query")
	tool.CostUnits = uint64Pointer(2)
	tool.MaxResultBytes = 4096
	compiled, err := CompilePlan(Plan{
		Tools: []Tool{tool},
		Budgets: []Budget{
			{
				ID: "z-budget", Selector: Selector{ToolIDs: []string{"query"}},
				Limits: BudgetLimits{CallCount: 5}, Reset: BudgetResetNever,
				OnExhaustion: DecisionBlock, SourceIdentity: ".reconc.yml",
			},
			{
				ID: "a-budget", Selector: Selector{ToolIDs: []string{"query"}},
				Limits: BudgetLimits{ResultBytes: 8192, CostUnits: 4, Concurrent: 2},
				Reset:  BudgetResetOperatorRun, OnExhaustion: DecisionBlock,
				SourceIdentity: ".reconc.yml",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := compiled.Budgets()
	if len(first) != 2 || first[0].ID != "a-budget" || first[1].ID != "z-budget" {
		t.Fatalf("canonical budgets = %#v", first)
	}
	first[0].ID = "mutated"
	first[0].Selector.ToolIDs[0] = "mutated"
	second := compiled.Budgets()
	if second[0].ID != "a-budget" || second[0].Selector.ToolIDs[0] != "query" {
		t.Fatalf("budget mutated through defensive copy: %#v", second)
	}
	copyTool, ok := compiled.Tool("query")
	if !ok || copyTool.CostUnits == nil || *copyTool.CostUnits != 2 {
		t.Fatalf("compiled tool lookup = %#v, %t", copyTool, ok)
	}
	*copyTool.CostUnits = 99
	copyTool, _ = compiled.Tool("query")
	if *copyTool.CostUnits != 2 {
		t.Fatal("compiled cost units mutated through Tool")
	}
}

func TestCompilePlanCanonicalizesApprovalDisclosuresAndExactUnion(t *testing.T) {
	t.Parallel()
	tool := gatewayTool("query", "query")
	compiled, err := CompilePlan(Plan{
		Tools: []Tool{tool},
		Approvals: []ApprovalDisclosure{
			{
				ID: "z-summary", Selector: Selector{ToolIDs: []string{"query"}},
				SelectedArguments: []string{"/query", "/database"}, SourceIdentity: ".reconc.yml",
			},
			{
				ID: "a-summary", Selector: Selector{ToolIDs: []string{"query"}, Phases: []Phase{PhasePreCall}},
				SelectedArguments: []string{"/database"}, SourceIdentity: ".reconc.yml",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Transport: TransportMCPStdio, ServerLabel: "server", Tool: "query", Phase: PhasePreCall,
	}
	disclosures, pointers, err := compiled.ApprovalDisclosures(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(disclosures) != 2 || disclosures[0].ID != "a-summary" ||
		len(pointers) != 2 || pointers[0] != "/database" || pointers[1] != "/query" {
		t.Fatalf("approval disclosure union = %#v, %#v", disclosures, pointers)
	}
	disclosures[0].SelectedArguments[0] = "/mutated"
	again, _, err := compiled.ApprovalDisclosures(request)
	if err != nil || again[0].SelectedArguments[0] != "/database" {
		t.Fatal("approval disclosure mutated through returned copy")
	}
	request.Phase = PhaseProgress
	disclosures, pointers, err = compiled.ApprovalDisclosures(request)
	if err != nil || len(disclosures) != 0 || len(pointers) != 0 {
		t.Fatalf("non-approval phase disclosures = %#v, %#v, %v", disclosures, pointers, err)
	}
}

func TestCompilePlanRejectsUnsafeApprovalDisclosures(t *testing.T) {
	t.Parallel()
	valid := ApprovalDisclosure{
		ID: "summary", Selector: Selector{ToolIDs: []string{"query"}},
		SelectedArguments: []string{"/query"}, SourceIdentity: ".reconc.yml",
	}
	tests := []struct {
		name   string
		mutate func(*ApprovalDisclosure)
		want   string
	}{
		{name: "empty selector", mutate: func(value *ApprovalDisclosure) { value.Selector = Selector{} }, want: "selector must contain"},
		{name: "empty arguments", mutate: func(value *ApprovalDisclosure) { value.SelectedArguments = []string{} }, want: "at least one"},
		{name: "invalid pointer", mutate: func(value *ApprovalDisclosure) { value.SelectedArguments = []string{"relative"} }, want: "invalid value"},
		{name: "progress", mutate: func(value *ApprovalDisclosure) { value.Selector.Phases = []Phase{PhaseProgress} }, want: "only pre_call or post_result"},
		{name: "no tool", mutate: func(value *ApprovalDisclosure) { value.Selector.Tools = []string{"missing"} }, want: "cannot match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			disclosure := valid
			test.mutate(&disclosure)
			_, err := CompilePlan(Plan{Tools: []Tool{gatewayTool("query", "query")}, Approvals: []ApprovalDisclosure{disclosure}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	host := hostTool("query", "query", EffectExternal, nil, "")
	if _, err := CompilePlan(Plan{Tools: []Tool{host}, Approvals: []ApprovalDisclosure{valid}}); err == nil ||
		!strings.Contains(err.Error(), "host_mcp") {
		t.Fatalf("host approval disclosure error = %v", err)
	}
}

func TestCompilePlanRejectsInvalidBudgetContracts(t *testing.T) {
	t.Parallel()
	baseTool := gatewayTool("query", "query")
	baseTool.CostUnits = uint64Pointer(1)
	baseTool.MaxResultBytes = 64
	valid := Budget{
		ID: "budget", Selector: Selector{ToolIDs: []string{"query"}},
		Limits: BudgetLimits{CallCount: 1}, Reset: BudgetResetNever,
		OnExhaustion: DecisionBlock, SourceIdentity: ".reconc.yml",
	}
	tests := []struct {
		name   string
		mutate func(*Budget, *Tool)
		want   string
	}{
		{name: "empty selector", mutate: func(b *Budget, _ *Tool) { b.Selector = Selector{} }, want: "selector must contain"},
		{name: "empty limits", mutate: func(b *Budget, _ *Tool) { b.Limits = BudgetLimits{} }, want: "limits must contain"},
		{name: "limit overflow", mutate: func(b *Budget, _ *Tool) { b.Limits.CallCount = math.MaxInt64 + 1 }, want: "exceeds"},
		{name: "concurrency overflow", mutate: func(b *Budget, _ *Tool) { b.Limits = BudgetLimits{Concurrent: 5} }, want: "gateway maximum"},
		{name: "invalid reset", mutate: func(b *Budget, _ *Tool) { b.Reset = "hourly" }, want: "reset must"},
		{name: "missing window", mutate: func(b *Budget, _ *Tool) { b.Reset = BudgetResetFixedWindow }, want: "requires window_seconds"},
		{name: "extraneous window", mutate: func(b *Budget, _ *Tool) { b.WindowSeconds = 60 }, want: "valid only"},
		{name: "oversized window", mutate: func(b *Budget, _ *Tool) { b.Reset = BudgetResetFixedWindow; b.WindowSeconds = 86401 }, want: "between 1 and 86400"},
		{name: "rate without window", mutate: func(b *Budget, _ *Tool) { b.Limits = BudgetLimits{RateWindow: 1} }, want: "requires fixed_window"},
		{name: "non-block exhaustion", mutate: func(b *Budget, _ *Tool) { b.OnExhaustion = DecisionWarn }, want: "on_exhaustion"},
		{name: "post phase", mutate: func(b *Budget, _ *Tool) { b.Selector.Phases = []Phase{PhasePostResult} }, want: "only pre_call"},
		{name: "no matching tool", mutate: func(b *Budget, _ *Tool) { b.Selector.Tools = []string{"different"} }, want: "cannot match"},
		{name: "result contract absent", mutate: func(b *Budget, tool *Tool) { b.Limits = BudgetLimits{ResultBytes: 1}; tool.MaxResultBytes = 0 }, want: "max_result_bytes"},
		{name: "cost contract absent", mutate: func(b *Budget, tool *Tool) { b.Limits = BudgetLimits{CostUnits: 1}; tool.CostUnits = nil }, want: "cost_units"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			budget, tool := valid, baseTool
			test.mutate(&budget, &tool)
			_, err := CompilePlan(Plan{Tools: []Tool{tool}, Budgets: []Budget{budget}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	_, err := CompilePlan(Plan{Tools: []Tool{baseTool}, Budgets: []Budget{valid, valid}})
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("duplicate budget error = %v", err)
	}
	limitSetters := []struct {
		name string
		set  func(*BudgetLimits)
	}{
		{name: "call_count", set: func(value *BudgetLimits) { value.CallCount = math.MaxInt64 + 1 }},
		{name: "denied_count", set: func(value *BudgetLimits) { value.DeniedCount = math.MaxInt64 + 1 }},
		{name: "approval_count", set: func(value *BudgetLimits) { value.ApprovalCount = math.MaxInt64 + 1 }},
		{name: "argument_bytes", set: func(value *BudgetLimits) { value.ArgumentBytes = math.MaxInt64 + 1 }},
		{name: "result_bytes", set: func(value *BudgetLimits) { value.ResultBytes = math.MaxInt64 + 1 }},
		{name: "cost_units", set: func(value *BudgetLimits) { value.CostUnits = math.MaxInt64 + 1 }},
		{name: "concurrent", set: func(value *BudgetLimits) { value.Concurrent = math.MaxInt64 + 1 }},
		{name: "rate_window", set: func(value *BudgetLimits) { value.RateWindow = math.MaxInt64 + 1 }},
	}
	for _, setter := range limitSetters {
		setter := setter
		t.Run("unbounded "+setter.name, func(t *testing.T) {
			t.Parallel()
			budget := valid
			budget.Limits = BudgetLimits{}
			setter.set(&budget.Limits)
			_, err := CompilePlan(Plan{Tools: []Tool{baseTool}, Budgets: []Budget{budget}})
			if err == nil {
				t.Fatalf("unbounded %s limit compiled", setter.name)
			}
		})
	}
	tool := baseTool
	tool.CostUnits = uint64Pointer(math.MaxInt64 + 1)
	if _, err := CompilePlan(Plan{Tools: []Tool{tool}}); err == nil || !strings.Contains(err.Error(), "cost_units") {
		t.Fatalf("oversized cost error = %v", err)
	}
}

func TestCompilePlanValidatesMaxResultBytesContract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value uint64
		want  string
	}{
		{name: "omitted internal sentinel", value: 0},
		{name: "minimum", value: 1},
		{name: "maximum", value: MaxArgumentBytes},
		{name: "overflow", value: MaxArgumentBytes + 1, want: "omitted (zero in Go) or between 1 and"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tool := gatewayTool("query", "query")
			tool.MaxResultBytes = test.value
			compiled, err := CompilePlan(Plan{Tools: []Tool{tool}})
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %v, want %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, ok := compiled.Tool("query")
			if !ok || got.MaxResultBytes != test.value {
				t.Fatalf("max_result_bytes = %d, found %t, want %d", got.MaxResultBytes, ok, test.value)
			}
		})
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

func gatewayTool(id, name string) Tool {
	return Tool{
		ID: id, Transport: TransportMCPStdio, ServerLabel: "server",
		Tool: name, Effect: Effect{Kind: EffectExternal},
		Origin: OriginActions, SourceIdentity: ".reconc.yml",
	}
}

func uint64Pointer(value uint64) *uint64 {
	return &value
}
