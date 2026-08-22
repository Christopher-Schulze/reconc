package action

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/netip"
	"testing"
)

func FuzzParseJSONCanonicalRoundTrip(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`null`),
		[]byte(`{"a":[1.0,true,"x"],"b":{"~":"/"}}`),
		[]byte(`{"a":1,"a":2}`),
		{0xff},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		value, err := ParseJSON(input)
		if err != nil {
			return
		}
		canonical, err := value.MarshalJSON()
		if err != nil {
			t.Fatalf("marshal accepted value: %v", err)
		}
		reparsed, err := ParseJSON(canonical)
		if err != nil {
			t.Fatalf("parse canonical value %q: %v", canonical, err)
		}
		reencoded, err := reparsed.MarshalJSON()
		if err != nil || !bytes.Equal(canonical, reencoded) {
			t.Fatalf("canonical round trip = %q, %v; want %q", reencoded, err, canonical)
		}
		if size, err := value.CanonicalJSONSize(); err != nil || size != len(canonical) {
			t.Fatalf("canonical size = %d, %v; want %d", size, err, len(canonical))
		}
	})
}

func FuzzCompilePointerRoundTrip(f *testing.F) {
	for _, seed := range []string{"", "/a~1b/~0", "relative", "/bad~2", "/0"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pointer string) {
		tokens, err := CompilePointer(pointer)
		if err != nil {
			return
		}
		encoded := ""
		for _, token := range tokens {
			encoded += "/" + pointerEscape(token)
		}
		roundTrip, err := CompilePointer(encoded)
		if err != nil {
			t.Fatalf("compile reconstructed pointer %q: %v", encoded, err)
		}
		if data, _ := json.Marshal(tokens); !bytes.Equal(data, mustMarshalJSON(roundTrip)) {
			t.Fatalf("pointer tokens changed: %q -> %#v -> %#v", pointer, tokens, roundTrip)
		}
	})
}

func FuzzResolvePointerTypedStates(f *testing.F) {
	for _, seed := range []struct {
		body    string
		pointer string
	}{
		{body: `{"a":[null,{"b":1}]}`, pointer: "/a/1/b"},
		{body: `[]`, pointer: "/01"},
		{body: `null`, pointer: "/x"},
	} {
		f.Add(seed.body, seed.pointer)
	}
	f.Fuzz(func(t *testing.T, body, pointer string) {
		value, err := ParseJSON([]byte(body))
		if err != nil {
			return
		}
		result, err := ResolvePointer(value, pointer)
		if err != nil {
			return
		}
		switch result.State {
		case PointerPresent, PointerNull, PointerMissing, PointerWrongContainer, PointerInvalidIndex:
		default:
			t.Fatalf("unknown pointer state %q", result.State)
		}
	})
}

func FuzzNormalizeActionRequest(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{}`), []byte(`{"a":1.0}`), []byte(`{"a":1,"a":2}`), {0xff},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, arguments []byte) {
		request, err := NormalizeRequest(testRawRequest(arguments))
		if err != nil {
			return
		}
		first, err := CanonicalRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		second, err := CanonicalRequest(request)
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("canonical request is unstable: %v", err)
		}
	})
}

func FuzzActionEvaluatorAndCacheIdentity(f *testing.F) {
	operand, _ := String("production")
	rule := testRule("block-production", DecisionBlock, Predicate{
		Source: SourceArguments, Pointer: "/target", Op: OperatorEqual, Value: &operand,
	})
	evaluator, baseline := testActionEvaluator(f, []Rule{rule}, Defaults{}, testExternalEffect())
	for _, seed := range []string{"production", "staging", "", "秘密", string([]byte{0xff})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, target string) {
		value, err := String(target)
		if err != nil {
			return
		}
		arguments, err := Object([]Member{{Name: "target", Value: value}})
		if err != nil {
			t.Fatal(err)
		}
		input := baseline
		input.Request.Arguments = &arguments
		result := evaluator.Evaluate(input)
		if !result.Decision.Valid() || !result.Reason.Valid() {
			t.Fatalf("invalid evaluator result: %#v", result)
		}
		first := evaluator.CacheIdentity(input)
		second := evaluator.CacheIdentity(input)
		if first != second {
			t.Fatalf("cache identity is unstable: %#v != %#v", first, second)
		}
	})
}

func FuzzURLCIDRAndPathTargets(f *testing.F) {
	urlConstraint := URLConstraint{
		Schemes: []string{"https"}, Hosts: []string{"example.test"},
		Ports: []uint16{443}, PathPrefixes: []string{"/v1"},
	}
	pathConstraint := PathConstraint{Style: PathRepository, Base: "safe", CaseSensitive: true}
	for _, seed := range []string{
		"https://example.test/v1", "https://user@example.test/%2f", "10.0.0.1",
		"fe80::1%en0", "safe/file", "safe/../escape",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		for _, state := range []ConditionState{
			matchURLConstraint(value, urlConstraint),
			matchCIDRs(value, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}),
			matchPathConstraint(value, pathConstraint),
		} {
			if state != ConditionTrue && state != ConditionFalse && state != ConditionIndeterminate {
				t.Fatalf("invalid predicate state %q", state)
			}
		}
	})
}

func FuzzDecimalComparisonOracle(f *testing.F) {
	for _, seed := range [][2]string{
		{"1.0", "10e-1"}, {"1e100", "9e99"}, {"-1.19", "-1.2"}, {"1e-1000", "0"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, leftRaw, rightRaw string) {
		left, err := ParseDecimal(leftRaw)
		if err != nil {
			return
		}
		right, err := ParseDecimal(rightRaw)
		if err != nil {
			return
		}
		got := left.Compare(right)
		if got < -1 || got > 1 || right.Compare(left) != -got || left.Equal(right) != (got == 0) {
			t.Fatalf("comparison laws failed for %q and %q", leftRaw, rightRaw)
		}
		if left.exponent < -2048 || left.exponent > 2048 || right.exponent < -2048 || right.exponent > 2048 {
			return
		}
		want := decimalRat(left).Cmp(decimalRat(right))
		if got != want {
			t.Fatalf("Compare(%q, %q) = %d, exact rational oracle = %d", leftRaw, rightRaw, got, want)
		}
	})
}

func decimalRat(value Decimal) *big.Rat {
	coefficient := value.coefficient
	if coefficient == "" {
		coefficient = "0"
	}
	integer := new(big.Int)
	integer.SetString(coefficient, 10)
	if value.negative {
		integer.Neg(integer)
	}
	exponent := value.exponent
	if exponent < 0 {
		exponent = -exponent
	}
	power := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
	if value.exponent >= 0 {
		integer.Mul(integer, power)
		return new(big.Rat).SetInt(integer)
	}
	return new(big.Rat).SetFrac(integer, power)
}

func pointerEscape(value string) string {
	var out bytes.Buffer
	for _, character := range value {
		switch character {
		case '~':
			out.WriteString("~0")
		case '/':
			out.WriteString("~1")
		default:
			out.WriteRune(character)
		}
	}
	return out.String()
}

func mustMarshalJSON(value any) []byte {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}
