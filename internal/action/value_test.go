package action

import "testing"

func TestParseJSONCanonicalNumbersAndObjectOrder(t *testing.T) {
	t.Parallel()
	value, err := ParseObjectJSON([]byte(`{"z":10e-1,"a":1.0}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), `{"a":1,"z":1}`; got != want {
		t.Fatalf("canonical JSON = %s, want %s", got, want)
	}
}

func TestParseJSONRejectsUnsafeForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body []byte
	}{
		{name: "duplicate root", body: []byte(`{"a":1,"a":2}`)},
		{name: "duplicate nested", body: []byte(`{"a":{"b":1,"b":2}}`)},
		{name: "trailing", body: []byte(`{} {}`)},
		{name: "unpaired high", body: []byte(`{"a":"\ud800"}`)},
		{name: "unpaired low", body: []byte(`{"a":"\udc00"}`)},
		{name: "invalid utf8", body: []byte{'{', '"', 'a', '"', ':', '"', 0xff, '"', '}'}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseJSON(test.body); err == nil {
				t.Fatal("expected strict JSON rejection")
			}
		})
	}
}

func TestParseDecimalBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{input: "0", want: "0"},
		{input: "-0.0", want: "0"},
		{input: "1.0", want: "1"},
		{input: "10e-1", want: "1"},
		{input: "-1200.00", want: "-12e2"},
	}
	for _, test := range tests {
		decimal, err := ParseDecimal(test.input)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", test.input, err)
		}
		if got := decimal.String(); got != test.want {
			t.Fatalf("ParseDecimal(%q) = %s, want %s", test.input, got, test.want)
		}
	}
	for _, input := range []string{"01", "+1", "1.", "NaN", "1e100001"} {
		if _, err := ParseDecimal(input); err == nil {
			t.Fatalf("ParseDecimal(%q) unexpectedly succeeded", input)
		}
	}
}

func TestDecimalCompareExactMagnitude(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		left  string
		right string
		want  int
	}{
		{name: "equal spelling variants", left: "1.0", right: "10e-1", want: 0},
		{name: "large exponent", left: "1e100", right: "9e99", want: 1},
		{name: "fractional alignment", left: "1.19", right: "1.2", want: -1},
		{name: "negative", left: "-1.19", right: "-1.2", want: 1},
		{name: "negative below zero", left: "-1e-100000", right: "0", want: -1},
		{name: "positive above zero", left: "1e-100000", right: "0", want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			left, err := ParseDecimal(test.left)
			if err != nil {
				t.Fatal(err)
			}
			right, err := ParseDecimal(test.right)
			if err != nil {
				t.Fatal(err)
			}
			if got := left.Compare(right); got != test.want {
				t.Fatalf("Compare(%s, %s) = %d, want %d", test.left, test.right, got, test.want)
			}
			if got := right.Compare(left); got != -test.want {
				t.Fatalf("reverse Compare(%s, %s) = %d, want %d", test.right, test.left, got, -test.want)
			}
		})
	}
}

func TestZeroDecimalIsCanonicalAcrossConstructionPaths(t *testing.T) {
	t.Parallel()
	parsed, err := ParseJSON([]byte(`0`))
	if err != nil {
		t.Fatal(err)
	}
	constructed := Number(Decimal{})
	if !constructed.Equal(parsed) || constructed.number.String() != "0" {
		t.Fatalf("zero decimal construction is not canonical: %#v != %#v", constructed, parsed)
	}
}

func TestValueEqualIsExactForEveryClosedKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{name: "null", left: `null`, right: `null`, want: true},
		{name: "boolean equal", left: `true`, right: `true`, want: true},
		{name: "boolean differs", left: `true`, right: `false`},
		{name: "number mathematical equality", left: `1.0`, right: `10e-1`, want: true},
		{name: "number differs", left: `1`, right: `2`},
		{name: "string equal", left: `"é"`, right: `"é"`, want: true},
		{name: "string bytewise differs", left: `"é"`, right: `"é"`},
		{name: "array equal", left: `[1,"x",null]`, right: `[1.0,"x",null]`, want: true},
		{name: "array length differs", left: `[1]`, right: `[1,2]`},
		{name: "array member differs", left: `[1,2]`, right: `[1,3]`},
		{name: "object order canonical", left: `{"b":2,"a":1}`, right: `{"a":1.0,"b":2}`, want: true},
		{name: "object key differs", left: `{"a":1}`, right: `{"b":1}`},
		{name: "object value differs", left: `{"a":1}`, right: `{"a":2}`},
		{name: "kind differs", left: `1`, right: `"1"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := mustTestValue(t, test.left)
			right := mustTestValue(t, test.right)
			if got := left.Equal(right); got != test.want {
				t.Fatalf("Equal(%s, %s) = %t, want %t", test.left, test.right, got, test.want)
			}
			if got := right.Equal(left); got != test.want {
				t.Fatalf("reverse Equal(%s, %s) = %t, want %t", test.right, test.left, got, test.want)
			}
		})
	}
}
