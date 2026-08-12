package actionevidence

import (
	"bytes"
	"testing"
)

func TestDecodeReportRejectsDuplicateKeys(t *testing.T) {
	report, err := Build(completeBuildInput(t))
	if err != nil {
		t.Fatal(err)
	}
	body, err := MarshalJSON(report)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := bytes.Replace(
		body,
		[]byte("{\n  \"schema\": "),
		[]byte("{\n  \"schema\": \"reconc.action-evidence/v1\",\n  \"schema\": "),
		1,
	)
	if bytes.Equal(duplicate, body) {
		t.Fatal("test did not inject a duplicate report field")
	}
	if _, err := DecodeReport(duplicate); err == nil {
		t.Fatal("DecodeReport accepted duplicate JSON object keys")
	}
}

func TestReportRejectsUnsortedKnownGapsAfterResealing(t *testing.T) {
	report, err := Build(completeBuildInput(t))
	if err != nil {
		t.Fatal(err)
	}
	report.Controls[0].KnownGaps = []string{"second gap", "first gap"}
	report.Identity, err = reportIdentity(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReport(report); err == nil {
		t.Fatal("ValidateReport accepted non-canonical known-gap ordering")
	}
}

func TestRenderVerificationTextReportsExactBoundedSummary(t *testing.T) {
	report, err := Build(completeBuildInput(t))
	if err != nil {
		t.Fatal(err)
	}
	text := RenderVerificationText(report)
	for _, expected := range [][]byte{
		[]byte("Action evidence: covered\n"),
		[]byte("Controls: 1\n"),
		[]byte("Mapping packs: 1\n"),
		[]byte("Ledger: verified\n"),
		[]byte("State integrity: unavailable\n"),
		[]byte("Scenario evidence: true\n"),
	} {
		if !bytes.Contains(text, expected) {
			t.Fatalf("verification text omitted %q: %s", expected, text)
		}
	}
	for _, private := range [][]byte{[]byte("raw-secret-value"), []byte("alice@example.test")} {
		if bytes.Contains(text, private) {
			t.Fatalf("verification text exposed private data: %q", private)
		}
	}
}

func FuzzDecodeReportStrictRoundTrip(f *testing.F) {
	report, err := Build(completeBuildInput(f))
	if err != nil {
		f.Fatal(err)
	}
	body, err := MarshalJSON(report)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(body)
	f.Fuzz(func(t *testing.T, input []byte) {
		decoded, decodeErr := DecodeReport(input)
		if decodeErr != nil {
			return
		}
		canonical, marshalErr := MarshalJSON(decoded)
		if marshalErr != nil {
			t.Fatalf("accepted report did not remarshal: %v", marshalErr)
		}
		if _, roundTripErr := DecodeReport(canonical); roundTripErr != nil {
			t.Fatalf("canonical report did not round-trip: %v", roundTripErr)
		}
	})
}
