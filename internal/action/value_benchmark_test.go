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

func BenchmarkParseDecimalTypical(b *testing.B) {
	const lexeme = "-1200.003400e-12"
	b.ReportAllocs()
	for b.Loop() {
		decimal, err := ParseDecimal(lexeme)
		if err != nil || decimal.coefficient == "" {
			b.Fatalf("ParseDecimal(%q): %v", lexeme, err)
		}
	}
}

func BenchmarkParseDecimalMaximumDigits(b *testing.B) {
	lexeme := "1." + strings.Repeat("2", MaxNumberDigits-1)
	b.ReportAllocs()
	for b.Loop() {
		decimal, err := ParseDecimal(lexeme)
		if err != nil || len(decimal.coefficient) != MaxNumberDigits {
			b.Fatalf("ParseDecimal maximum digits: %v", err)
		}
	}
}

func BenchmarkDecimalStringTypical(b *testing.B) {
	decimal, err := ParseDecimal("-1200.003400e-12")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if decimal.String() == "" {
			b.Fatal("decimal string is empty")
		}
	}
}

func BenchmarkDecimalAppendTypical(b *testing.B) {
	decimal, err := ParseDecimal("-1200.003400e-12")
	if err != nil {
		b.Fatal(err)
	}
	buffer := make([]byte, 0, decimal.canonicalJSONSize())
	b.ReportAllocs()
	for b.Loop() {
		buffer = buffer[:0]
		buffer = decimal.appendString(buffer)
	}
	if string(buffer) != decimal.String() {
		b.Fatalf("appendString = %q, String = %q", buffer, decimal.String())
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
