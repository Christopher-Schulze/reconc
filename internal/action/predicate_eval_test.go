package action

import "testing"

func TestEvaluateEveryActionPredicate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		op      Operator
		target  string
		operand string
		want    ConditionState
	}{
		{name: "exists present null", op: OperatorExists, target: "null", want: ConditionTrue},
		{name: "equal exact decimal", op: OperatorEqual, target: "1.0", operand: "10e-1", want: ConditionTrue},
		{name: "not equal", op: OperatorNotEqual, target: `"one"`, operand: `"two"`, want: ConditionTrue},
		{name: "in", op: OperatorIn, target: `"β"`, operand: `["a","β"]`, want: ConditionTrue},
		{name: "not in", op: OperatorNotIn, target: `"c"`, operand: `["a","b"]`, want: ConditionTrue},
		{name: "prefix", op: OperatorPrefix, target: `"café"`, operand: `"caf"`, want: ConditionTrue},
		{name: "suffix", op: OperatorSuffix, target: `"café"`, operand: `"fé"`, want: ConditionTrue},
		{name: "contains", op: OperatorContains, target: `"αβγ"`, operand: `"β"`, want: ConditionTrue},
		{name: "glob", op: OperatorGlob, target: `"a/b/c"`, operand: `"a/**"`, want: ConditionTrue},
		{name: "regex full", op: OperatorRegex, target: `"prod-42"`, operand: `"prod-[0-9]+"`, want: ConditionTrue},
		{name: "regex substring false", op: OperatorRegex, target: `"xprod-42"`, operand: `"prod-[0-9]+"`, want: ConditionFalse},
		{name: "greater huge", op: OperatorGreater, target: "1e100", operand: "9e99", want: ConditionTrue},
		{name: "greater equal exact", op: OperatorGreaterEq, target: "1.0", operand: "1", want: ConditionTrue},
		{name: "less negative", op: OperatorLess, target: "-1e-100000", operand: "0", want: ConditionTrue},
		{name: "less equal", op: OperatorLessEq, target: "1", operand: "1.0", want: ConditionTrue},
		{name: "url", op: OperatorURL, target: `"https://api.example.test/v1/items"`, operand: `{"schemes":["https"],"hosts":["api.example.test"],"ports":[443],"path_prefixes":["/v1"],"allow_query":false,"allow_ip_literals":false}`, want: ConditionTrue},
		{name: "cidr", op: OperatorCIDR, target: `"10.2.3.4"`, operand: `["10.0.0.0/8"]`, want: ConditionTrue},
		{name: "path", op: OperatorPathWithin, target: `"safe/data/file"`, operand: `{"style":"repository","base":"safe/data","case_sensitive":true}`, want: ConditionTrue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			predicate := Predicate{Source: SourceArguments, Pointer: "/value", Op: test.op}
			if test.op != OperatorExists {
				operand := mustTestValue(t, test.operand)
				predicate.Value = &operand
			}
			compiled := compileTestPredicate(t, predicate)
			arguments := mustTestValue(t, `{"value":`+test.target+`}`)
			got := evaluatePredicate(compiled, Request{Arguments: &arguments}, DecisionBlock)
			if got.state != test.want {
				t.Fatalf("state = %s (%s), want %s", got.state, got.reason, test.want)
			}
		})
	}
}

func TestPredicateMissingNullWrongTypeAndUnicodeSemantics(t *testing.T) {
	t.Parallel()
	stringOperand := `"x"`
	numberOperand := "1"
	listOperand := `["x"]`
	tests := []struct {
		name     string
		op       Operator
		pointer  string
		argument string
		operand  string
		want     ConditionState
	}{
		{name: "exists missing false", op: OperatorExists, pointer: "/missing", argument: `{}`, want: ConditionFalse},
		{name: "exists wrong container", op: OperatorExists, pointer: "/value/x", argument: `{"value":1}`, want: ConditionIndeterminate},
		{name: "equal cross type false", op: OperatorEqual, pointer: "/value", argument: `{"value":"1"}`, operand: numberOperand, want: ConditionFalse},
		{name: "not equal missing indeterminate", op: OperatorNotEqual, pointer: "/missing", argument: `{}`, operand: stringOperand, want: ConditionIndeterminate},
		{name: "in null indeterminate", op: OperatorIn, pointer: "/value", argument: `{"value":null}`, operand: listOperand, want: ConditionIndeterminate},
		{name: "not in wrong type", op: OperatorNotIn, pointer: "/value", argument: `{"value":1}`, operand: listOperand, want: ConditionIndeterminate},
		{name: "prefix normalized unicode differs", op: OperatorPrefix, pointer: "/value", argument: `{"value":"é"}`, operand: `"é"`, want: ConditionFalse},
		{name: "contains number indeterminate", op: OperatorContains, pointer: "/value", argument: `{"value":1}`, operand: stringOperand, want: ConditionIndeterminate},
		{name: "numeric string indeterminate", op: OperatorGreater, pointer: "/value", argument: `{"value":"2"}`, operand: numberOperand, want: ConditionIndeterminate},
		{name: "glob missing indeterminate", op: OperatorGlob, pointer: "/missing", argument: `{}`, operand: `"*"`, want: ConditionIndeterminate},
		{name: "regex null indeterminate", op: OperatorRegex, pointer: "/value", argument: `{"value":null}`, operand: `".*"`, want: ConditionIndeterminate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			predicate := Predicate{Source: SourceArguments, Pointer: test.pointer, Op: test.op}
			if test.op != OperatorExists {
				operand := mustTestValue(t, test.operand)
				predicate.Value = &operand
			}
			compiled := compileTestPredicate(t, predicate)
			arguments := mustTestValue(t, test.argument)
			got := evaluatePredicate(compiled, Request{Arguments: &arguments}, DecisionBlock)
			if got.state != test.want {
				t.Fatalf("state = %s (%s), want %s", got.state, got.reason, test.want)
			}
		})
	}
}

func TestEvaluateMembershipSeparatesTargetMismatchFromOperandCorruption(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		op      Operator
		target  string
		operand string
		want    ConditionState
		reason  ReasonCode
	}{
		{name: "boolean target string list", op: OperatorIn, target: `true`, operand: `["true"]`, want: ConditionIndeterminate, reason: ReasonConditionIndeterminate},
		{name: "number target string list", op: OperatorIn, target: `1`, operand: `["1"]`, want: ConditionIndeterminate, reason: ReasonConditionIndeterminate},
		{name: "string target boolean list", op: OperatorNotIn, target: `"true"`, operand: `[true]`, want: ConditionIndeterminate, reason: ReasonConditionIndeterminate},
		{name: "string target number list", op: OperatorNotIn, target: `"1"`, operand: `[1]`, want: ConditionIndeterminate, reason: ReasonConditionIndeterminate},
		{name: "null target", op: OperatorIn, target: `null`, operand: `["x"]`, want: ConditionIndeterminate, reason: ReasonConditionIndeterminate},
		{name: "array target", op: OperatorIn, target: `[]`, operand: `["x"]`, want: ConditionIndeterminate, reason: ReasonConditionIndeterminate},
		{name: "object target", op: OperatorIn, target: `{}`, operand: `["x"]`, want: ConditionIndeterminate, reason: ReasonConditionIndeterminate},
		{name: "valid match", op: OperatorIn, target: `"x"`, operand: `["x","y"]`, want: ConditionTrue},
		{name: "valid not in", op: OperatorNotIn, target: `"z"`, operand: `["x","y"]`, want: ConditionTrue},
		{name: "operand is not array", op: OperatorIn, target: `"x"`, operand: `"x"`, want: ConditionIndeterminate, reason: ReasonInternalInvariant},
		{name: "operand is empty", op: OperatorIn, target: `"x"`, operand: `[]`, want: ConditionIndeterminate, reason: ReasonInternalInvariant},
		{name: "operand contains null", op: OperatorIn, target: `"x"`, operand: `[null]`, want: ConditionIndeterminate, reason: ReasonInternalInvariant},
		{name: "operand contains mixed scalar kinds", op: OperatorIn, target: `"x"`, operand: `["x",1]`, want: ConditionIndeterminate, reason: ReasonInternalInvariant},
		{name: "operand contains collection", op: OperatorIn, target: `"x"`, operand: `["x",[]]`, want: ConditionIndeterminate, reason: ReasonInternalInvariant},
		{name: "invalid operator", op: OperatorEqual, target: `"x"`, operand: `["x"]`, want: ConditionIndeterminate, reason: ReasonInternalInvariant},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state, reason := evaluateMembership(
				test.op, mustTestValue(t, test.target), mustTestValue(t, test.operand),
			)
			if state != test.want || reason != test.reason {
				t.Fatalf("membership = %s (%s), want %s (%s)", state, reason, test.want, test.reason)
			}
		})
	}

	oversized := Value{kind: ValueArray, array: make([]Value, MaxListValues+1)}
	state, reason := evaluateMembership(OperatorIn, testStringValue(t, "x"), oversized)
	if state != ConditionIndeterminate || reason != ReasonInternalInvariant {
		t.Fatalf("oversized operand = %s (%s), want indeterminate internal invariant", state, reason)
	}
}

func TestEveryPredicateStateMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		op            Operator
		operand       string
		positive      string
		negative      string
		wrongType     string
		wantNegative  ConditionState
		wantNull      ConditionState
		wantWrongType ConditionState
	}{
		{name: "equal", op: OperatorEqual, operand: `"x"`, positive: `"x"`, negative: `"y"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionFalse, wantWrongType: ConditionFalse},
		{name: "not equal", op: OperatorNotEqual, operand: `"x"`, positive: `"y"`, negative: `"x"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionTrue, wantWrongType: ConditionTrue},
		{name: "in", op: OperatorIn, operand: `["x"]`, positive: `"x"`, negative: `"y"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "not in", op: OperatorNotIn, operand: `["x"]`, positive: `"y"`, negative: `"x"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "prefix", op: OperatorPrefix, operand: `"pre"`, positive: `"prefix"`, negative: `"xpre"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "suffix", op: OperatorSuffix, operand: `"fix"`, positive: `"suffix"`, negative: `"fixx"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "contains", op: OperatorContains, operand: `"mid"`, positive: `"a-mid-z"`, negative: `"none"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "glob", op: OperatorGlob, operand: `"a/**"`, positive: `"a/b"`, negative: `"b/a"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "regex", op: OperatorRegex, operand: `"a[0-9]+"`, positive: `"a12"`, negative: `"a"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "greater", op: OperatorGreater, operand: `1`, positive: `2`, negative: `0`, wrongType: `"2"`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "greater equal", op: OperatorGreaterEq, operand: `1`, positive: `1`, negative: `0`, wrongType: `"1"`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "less", op: OperatorLess, operand: `1`, positive: `0`, negative: `2`, wrongType: `"0"`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "less equal", op: OperatorLessEq, operand: `1`, positive: `1`, negative: `2`, wrongType: `"1"`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "url", op: OperatorURL, operand: `{"schemes":["https"],"hosts":["example.test"],"allow_query":false,"allow_ip_literals":false}`, positive: `"https://example.test"`, negative: `"https://other.test"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "cidr", op: OperatorCIDR, operand: `["10.0.0.0/8"]`, positive: `"10.1.2.3"`, negative: `"192.0.2.1"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
		{name: "path", op: OperatorPathWithin, operand: `{"style":"repository","base":"safe","case_sensitive":true}`, positive: `"safe/file"`, negative: `"unsafe/file"`, wrongType: `1`, wantNegative: ConditionFalse, wantNull: ConditionIndeterminate, wantWrongType: ConditionIndeterminate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operand := mustTestValue(t, test.operand)
			compiled := compileTestPredicate(t, Predicate{
				Source: SourceArguments, Pointer: "/value", Op: test.op, Value: &operand,
			})
			vectors := []struct {
				name  string
				value string
				want  ConditionState
			}{
				{name: "positive", value: test.positive, want: ConditionTrue},
				{name: "negative", value: test.negative, want: test.wantNegative},
				{name: "missing", value: "missing", want: ConditionIndeterminate},
				{name: "null", value: `null`, want: test.wantNull},
				{name: "wrong type", value: test.wrongType, want: test.wantWrongType},
			}
			for _, vector := range vectors {
				body := `{"value":` + vector.value + `}`
				if vector.name == "missing" {
					body = `{}`
				}
				arguments := mustTestValue(t, body)
				got := evaluatePredicate(compiled, Request{Arguments: &arguments}, DecisionBlock)
				if got.state != vector.want {
					t.Fatalf("%s state = %s (%s), want %s", vector.name, got.state, got.reason, vector.want)
				}
			}
		})
	}

	exists := compileTestPredicate(t, Predicate{Source: SourceArguments, Pointer: "/value", Op: OperatorExists})
	for _, vector := range []struct {
		body string
		want ConditionState
	}{{body: `{"value":null}`, want: ConditionTrue}, {body: `{}`, want: ConditionFalse}, {body: `{"value":1}`, want: ConditionTrue}} {
		arguments := mustTestValue(t, vector.body)
		if got := evaluatePredicate(exists, Request{Arguments: &arguments}, DecisionBlock); got.state != vector.want {
			t.Fatalf("exists %s = %s, want %s", vector.body, got.state, vector.want)
		}
	}
}

func TestContextRootSelectionPreservesValuesAndMinimumTrust(t *testing.T) {
	t.Parallel()
	operand := mustTestValue(t, `{"role":"operator"}`)
	predicate := compileTestPredicate(t, Predicate{
		Source: SourceContext, Pointer: "", MinimumProvenance: ProvenanceHostObserved,
		Op: OperatorEqual, Value: &operand,
	})
	request := Request{Context: []ContextValue{{
		Name: "role", Value: testStringValue(t, "operator"),
		Provenance: ProvenanceHostObserved, Available: true,
	}}}
	if got := evaluatePredicate(predicate, request, DecisionBlock); got.state != ConditionTrue {
		t.Fatalf("context root = %#v", got)
	}
	request.Context[0].Available = false
	if got := evaluatePredicate(predicate, request, DecisionBlock); got.state != ConditionIndeterminate {
		t.Fatalf("unavailable context root = %#v", got)
	}
}

func TestURLPredicateRejectsAmbiguityAndComparesNormalizedParts(t *testing.T) {
	t.Parallel()
	constraint := mustTestValue(t, `{"schemes":["https"],"hosts":["api.example.test"],"ports":[443],"path_prefixes":["/v1"],"allow_query":false,"allow_ip_literals":false}`)
	predicate := compileTestPredicate(t, Predicate{
		Source: SourceArguments, Pointer: "/value", Op: OperatorURL, Value: &constraint,
	})
	tests := []struct {
		name string
		url  string
		want ConditionState
	}{
		{name: "default port", url: "https://API.EXAMPLE.TEST./v1/items", want: ConditionTrue},
		{name: "explicit port", url: "https://api.example.test:443/v1", want: ConditionTrue},
		{name: "wrong port", url: "https://api.example.test:444/v1", want: ConditionFalse},
		{name: "sibling path", url: "https://api.example.test/v10", want: ConditionFalse},
		{name: "query forbidden", url: "https://api.example.test/v1?q=x", want: ConditionFalse},
		{name: "userinfo", url: "https://user@api.example.test/v1", want: ConditionIndeterminate},
		{name: "fragment", url: "https://api.example.test/v1#x", want: ConditionIndeterminate},
		{name: "encoded slash", url: "https://api.example.test/v1%2fescape", want: ConditionIndeterminate},
		{name: "encoded parent", url: "https://api.example.test/v1/%2e%2e", want: ConditionIndeterminate},
		{name: "double slash", url: "https://api.example.test/v1//x", want: ConditionIndeterminate},
		{name: "unicode host", url: "https://éxample.test/v1", want: ConditionIndeterminate},
		{name: "IP literal trailing DNS dot", url: "https://127.0.0.1./v1", want: ConditionIndeterminate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			arguments := mustTestValue(t, `{"value":`+quoteTestJSON(t, test.url)+`}`)
			got := evaluatePredicate(predicate, Request{Arguments: &arguments}, DecisionBlock)
			if got.state != test.want {
				t.Fatalf("state = %s, want %s", got.state, test.want)
			}
		})
	}
}

func TestURLConstraintRejectsTrailingDotOnIPLiteral(t *testing.T) {
	t.Parallel()
	if _, err := normalizeConstraintHost("127.0.0.1."); err == nil {
		t.Fatal("policy host accepted a trailing DNS dot on an IP literal")
	}
	if got, err := normalizeConstraintHost("Example.Test."); err != nil || got != "example.test" {
		t.Fatalf("DNS trailing-dot normalization = %q, %v", got, err)
	}
}

func TestCIDRAndPathAdversarialSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		op      Operator
		target  string
		operand string
		want    ConditionState
	}{
		{name: "cidr family mismatch", op: OperatorCIDR, target: `"2001:db8::1"`, operand: `["10.0.0.0/8"]`, want: ConditionFalse},
		{name: "cidr mapped IPv4 normalized", op: OperatorCIDR, target: `"::ffff:10.1.2.3"`, operand: `["::ffff:10.0.0.0/104"]`, want: ConditionTrue},
		{name: "cidr zone", op: OperatorCIDR, target: `"fe80::1%en0"`, operand: `["fe80::/10"]`, want: ConditionIndeterminate},
		{name: "repository sibling", op: OperatorPathWithin, target: `"safe/database"`, operand: `{"style":"repository","base":"safe/data","case_sensitive":true}`, want: ConditionFalse},
		{name: "repository parent escape", op: OperatorPathWithin, target: `"safe/data/../secret"`, operand: `{"style":"repository","base":"safe/data","case_sensitive":true}`, want: ConditionIndeterminate},
		{name: "posix child", op: OperatorPathWithin, target: `"/srv/data/file"`, operand: `{"style":"posix","base":"/srv/data","case_sensitive":true}`, want: ConditionTrue},
		{name: "windows case fold", op: OperatorPathWithin, target: `"c:\\DATA\\File"`, operand: `{"style":"windows","base":"C:/data","case_sensitive":false}`, want: ConditionTrue},
		{name: "windows volume mismatch", op: OperatorPathWithin, target: `"D:/data/file"`, operand: `{"style":"windows","base":"C:/data","case_sensitive":false}`, want: ConditionIndeterminate},
		{name: "windows alternate stream", op: OperatorPathWithin, target: `"C:/data/file:secret"`, operand: `{"style":"windows","base":"C:/data","case_sensitive":false}`, want: ConditionIndeterminate},
		{name: "windows UNC case fold", op: OperatorPathWithin, target: `"\\\\SERVER\\SHARE\\data\\file"`, operand: `{"style":"windows","base":"//server/share/data","case_sensitive":false}`, want: ConditionTrue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			operand := mustTestValue(t, test.operand)
			compiled := compileTestPredicate(t, Predicate{
				Source: SourceArguments, Pointer: "/value", Op: test.op, Value: &operand,
			})
			arguments := mustTestValue(t, `{"value":`+test.target+`}`)
			got := evaluatePredicate(compiled, Request{Arguments: &arguments}, DecisionBlock)
			if got.state != test.want {
				t.Fatalf("state = %s, want %s", got.state, test.want)
			}
		})
	}
}

func compileTestPredicate(t testing.TB, predicate Predicate) *CompiledPredicate {
	t.Helper()
	condition := Condition{Predicate: &predicate}
	plan, err := CompilePlan(Plan{Rules: []Rule{{
		ID: "predicate", Selector: Selector{Phases: []Phase{PhasePreCall}},
		When: &condition, Decision: DecisionBlock, SourceIdentity: ".reconc.yml",
	}}})
	if err != nil {
		t.Fatalf("CompilePlan: %v", err)
	}
	return plan.Rules()[0].Condition.Predicate
}

func quoteTestJSON(t testing.TB, value string) string {
	t.Helper()
	encoded, err := testStringValue(t, value).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
