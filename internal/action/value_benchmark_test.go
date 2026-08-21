package action

import (
	"strings"
	"testing"
)

func BenchmarkValueMarshalJSONMaximumLegal(b *testing.B) {
	value := benchmarkMaximumLegalValue(b)
	b.ReportAllocs()
	b.SetBytes(MaxArgumentBytes)
	b.ResetTimer()
	var body []byte
	for b.Loop() {
		var err error
		body, err = value.MarshalJSON()
		if err != nil {
			b.Fatal(err)
		}
	}
	if len(body) != MaxArgumentBytes {
		b.Fatalf("canonical JSON bytes = %d, want %d", len(body), MaxArgumentBytes)
	}
}

func BenchmarkParseJSONMaximumLegal(b *testing.B) {
	value := benchmarkMaximumLegalValue(b)
	body, err := value.MarshalJSON()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	var parsed Value
	for b.Loop() {
		parsed, err = ParseJSON(body)
		if err != nil {
			b.Fatal(err)
		}
	}
	if !parsed.Equal(value) {
		b.Fatal("parsed value differs from benchmark input")
	}
}

func benchmarkMaximumLegalValue(tb testing.TB) Value {
	tb.Helper()
	first, err := String(strings.Repeat("a", MaxJSONStringBytes))
	if err != nil {
		tb.Fatal(err)
	}
	second, err := String(strings.Repeat("b", MaxArgumentBytes-MaxJSONStringBytes-15))
	if err != nil {
		tb.Fatal(err)
	}
	value, err := Object([]Member{{Name: "a", Value: first}, {Name: "b", Value: second}})
	if err != nil {
		tb.Fatal(err)
	}
	return value
}
