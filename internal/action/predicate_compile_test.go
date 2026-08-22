package action

import (
	"strconv"
	"strings"
	"testing"
)

func TestCompilePredicateOperandContracts(t *testing.T) {
	t.Parallel()
	validURL := testValue(t, `{"schemes":["https"],"hosts":["example.test"],"ports":[443],"path_prefixes":["/api"],"allow_query":false,"allow_ip_literals":false}`)
	validPath := testValue(t, `{"style":"repository","base":"generated","case_sensitive":true}`)
	validCIDR := testValue(t, `["10.0.0.0/8"]`)
	validList := testValue(t, `["a","b"]`)
	stringValue := testValue(t, `"value"`)
	numberValue := testValue(t, `1.0`)
	objectValue := testValue(t, `{"a":1}`)
	tests := []struct {
		name    string
		op      Operator
		value   *Value
		wantErr string
	}{
		{name: "exists without operand", op: OperatorExists},
		{name: "exists with operand", op: OperatorExists, value: &stringValue, wantErr: "forbids value"},
		{name: "eq object", op: OperatorEqual, value: &objectValue},
		{name: "neq null", op: OperatorNotEqual, value: valuePointer(Null())},
		{name: "eq missing", op: OperatorEqual, wantErr: "requires value"},
		{name: "in same type", op: OperatorIn, value: &validList},
		{name: "not-in same type", op: OperatorNotIn, value: &validList},
		{name: "in mixed types", op: OperatorIn, value: valuePointer(testValue(t, `["a",1]`)), wantErr: "same-type"},
		{name: "in null", op: OperatorIn, value: valuePointer(testValue(t, `[null]`)), wantErr: "non-null"},
		{name: "in empty", op: OperatorIn, value: valuePointer(testValue(t, `[]`)), wantErr: "1 to"},
		{name: "prefix string", op: OperatorPrefix, value: &stringValue},
		{name: "suffix string", op: OperatorSuffix, value: &stringValue},
		{name: "contains string", op: OperatorContains, value: &stringValue},
		{name: "prefix number", op: OperatorPrefix, value: &numberValue, wantErr: "string value"},
		{name: "glob", op: OperatorGlob, value: valuePointer(testValue(t, `"a/**"`))},
		{name: "glob malformed", op: OperatorGlob, value: valuePointer(testValue(t, `"["`)), wantErr: "valid pattern"},
		{name: "regex", op: OperatorRegex, value: valuePointer(testValue(t, `"prod-[0-9]+"`))},
		{name: "regex malformed", op: OperatorRegex, value: valuePointer(testValue(t, `"("`)), wantErr: "invalid"},
		{name: "greater", op: OperatorGreater, value: &numberValue},
		{name: "greater equal", op: OperatorGreaterEq, value: &numberValue},
		{name: "less", op: OperatorLess, value: &numberValue},
		{name: "less equal", op: OperatorLessEq, value: &numberValue},
		{name: "numeric string", op: OperatorGreater, value: &stringValue, wantErr: "number value"},
		{name: "URL", op: OperatorURL, value: &validURL},
		{name: "URL missing Boolean", op: OperatorURL, value: valuePointer(testValue(t, `{"schemes":["https"],"hosts":["example.test"],"allow_query":false}`)), wantErr: "requires allow_ip_literals"},
		{name: "CIDR", op: OperatorCIDR, value: &validCIDR},
		{name: "CIDR zone", op: OperatorCIDR, value: valuePointer(testValue(t, `["fe80::1%eth0/64"]`)), wantErr: "canonical prefix"},
		{name: "path", op: OperatorPathWithin, value: &validPath},
		{name: "path missing case", op: OperatorPathWithin, value: valuePointer(testValue(t, `{"style":"repository","base":"generated"}`)), wantErr: "requires case_sensitive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			predicate := Predicate{Source: SourceArguments, Pointer: "/value", Op: test.op, Value: test.value}
			_, err := compilePredicate(&predicate, DecisionBlock, []Phase{PhasePreCall})
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("compile predicate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestCompilePredicateExactPatternAndListBoundaries(t *testing.T) {
	t.Parallel()
	for _, op := range []Operator{OperatorGlob, OperatorRegex} {
		t.Run(string(op), func(t *testing.T) {
			accepted := testValue(t, `"`+strings.Repeat("a", MaxPatternBytes)+`"`)
			predicate := Predicate{Source: SourceArguments, Pointer: "", Op: op, Value: &accepted}
			if _, err := compilePredicate(&predicate, DecisionBlock, []Phase{PhasePreCall}); err != nil {
				t.Fatalf("exact pattern boundary rejected: %v", err)
			}
			rejected := testValue(t, `"`+strings.Repeat("a", MaxPatternBytes+1)+`"`)
			predicate.Value = &rejected
			if _, err := compilePredicate(&predicate, DecisionBlock, []Phase{PhasePreCall}); err == nil {
				t.Fatal("pattern above exact boundary accepted")
			}
		})
	}

	items := make([]Value, MaxListValues)
	for index := range items {
		items[index] = testValue(t, `"value-`+strconv.Itoa(index)+`"`)
	}
	boundary, err := Array(items)
	if err != nil {
		t.Fatal(err)
	}
	predicate := Predicate{Source: SourceArguments, Pointer: "", Op: OperatorIn, Value: &boundary}
	if _, err := compilePredicate(&predicate, DecisionBlock, []Phase{PhasePreCall}); err != nil {
		t.Fatalf("exact list boundary rejected: %v", err)
	}
	items = append(items, testValue(t, `"overflow"`))
	overflow, err := Array(items)
	if err != nil {
		t.Fatal(err)
	}
	predicate.Value = &overflow
	if _, err := compilePredicate(&predicate, DecisionBlock, []Phase{PhasePreCall}); err == nil {
		t.Fatal("list above exact boundary accepted")
	}
}

func TestCompilePredicatePreparesPathConstraint(t *testing.T) {
	t.Parallel()
	value := testValue(t, `{"style":"windows","base":"C:/Workspace","case_sensitive":false}`)
	predicate := Predicate{Source: SourceArguments, Pointer: "/path", Op: OperatorPathWithin, Value: &value}
	compiled, err := compilePredicate(&predicate, DecisionBlock, []Phase{PhasePreCall})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Path == nil || !compiled.Path.prepared || compiled.Path.matchBase != "c:/workspace" ||
		compiled.Path.matchVolume != "c:" || compiled.Path.matchPrefix != "c:/workspace/" {
		t.Fatalf("prepared path constraint = %#v", compiled.Path)
	}
	if state := matchPathConstraint(`c:\WORKSPACE\nested`, *compiled.Path); state != ConditionTrue {
		t.Fatalf("prepared path match = %s", state)
	}
}

func testValue(t *testing.T, raw string) Value {
	t.Helper()
	value, err := ParseJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parse test value %s: %v", raw, err)
	}
	return value
}

func valuePointer(value Value) *Value { return &value }
