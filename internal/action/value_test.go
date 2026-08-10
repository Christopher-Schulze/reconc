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
