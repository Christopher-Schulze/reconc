package cireport

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"reconc.dev/reconc/internal/runtime"
)

// hostileText carries every shape a violation message can pick up from a
// repository: characters XML 1.0 forbids outright, invalid UTF-8 from a binary
// path, XML and JSON metacharacters, and mixed line endings.
const hostileText = "a\x00b\x01c\x08d\x0be\x0cf\x1fg <>&\"']]> \r\n\r end"

func hostileModel(t *testing.T) Model {
	t.Helper()
	text := hostileText + string([]byte{0xff, 0xfe}) + strings.Repeat("🙂", 8)
	report := &runtime.CheckReport{
		Decision: "block", OK: false,
		Violations: []runtime.Violation{{
			RuleID: text, Mode: "block", Message: text,
			Explanation: text, RecommendedAction: text,
		}},
	}
	return FromCheck("check", "0.9.5", Candidate{Fingerprint: "f"}, nil, report)
}

// TestJUnitOutputStaysLegalXML pins the property a CI system depends on. A
// JUnit file containing a character XML 1.0 forbids is rejected by strict
// parsers wholesale, so one hostile violation message would discard the entire
// report rather than surface it.
func TestJUnitOutputStaysLegalXML(t *testing.T) {
	body, err := Render(FormatJUnit, hostileModel(t))
	if err != nil {
		t.Fatalf("render JUnit: %v", err)
	}
	if !utf8.Valid(body) {
		t.Fatal("JUnit output is not valid UTF-8")
	}
	rest := body
	for len(rest) > 0 {
		value, size := utf8.DecodeRune(rest)
		rest = rest[size:]
		switch {
		case value == 0x9 || value == 0xA || value == 0xD:
		case value >= 0x20 && value <= 0xD7FF:
		case value >= 0xE000 && value <= 0xFFFD:
		case value >= 0x10000 && value <= 0x10FFFF:
		default:
			t.Fatalf("JUnit output contains a rune XML 1.0 forbids: %U", value)
		}
	}
}

// TestSARIFOutputStaysParseableJSON is the same guarantee for the other
// consumer: a SARIF file that does not decode is not partially useful, it is
// discarded.
func TestSARIFOutputStaysParseableJSON(t *testing.T) {
	body, err := Render(FormatSARIF, hostileModel(t))
	if err != nil {
		t.Fatalf("render SARIF: %v", err)
	}
	if !utf8.Valid(body) {
		t.Fatal("SARIF output is not valid UTF-8")
	}
	var document map[string]interface{}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("SARIF output does not decode: %v", err)
	}
	if _, ok := document["runs"]; !ok {
		t.Fatalf("SARIF output lost its runs array: %v", document)
	}
}
