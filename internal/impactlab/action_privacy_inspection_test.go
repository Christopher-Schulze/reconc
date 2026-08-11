package impactlab

import (
	"encoding/json"
	"testing"
)

func TestMalformedActionJSONScansTheEntireBoundedPayload(t *testing.T) {
	t.Parallel()
	scanner := mustActionPrivacyScanner(t)
	private := json.RawMessage(`{"broken":,"email":"person@example.test"}`)
	if !sensitiveMalformedActionJSON(scanner, private) {
		t.Fatal("private text after the syntax error escaped inspection")
	}
	if sensitiveMalformedActionJSON(scanner, json.RawMessage(`{"broken":,"message":"ordinary value"}`)) {
		t.Fatal("ordinary malformed JSON was classified as private")
	}
}
