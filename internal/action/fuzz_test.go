package action

import (
	"bytes"
	"encoding/json"
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
