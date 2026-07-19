package agentsession

import (
	"strings"
	"testing"
)

func TestParsePayloadRejectsAmbiguousSessionIdentifiers(t *testing.T) {
	for _, payload := range []string{
		`{"session_id":" leading"}`,
		`{"session_id":"trailing "}`,
		`{"session_id":"control\ncharacter"}`,
		`{"session_id":"` + strings.Repeat("x", maxSessionIDBytes+1) + `"}`,
	} {
		if _, err := ParsePayload([]byte(payload)); err == nil {
			t.Fatalf("ambiguous session ID was accepted: %s", payload)
		}
	}
}

func TestExitCodeRejectsFractionalAndOutOfRangeNumbers(t *testing.T) {
	for _, payload := range []string{
		`{"session_id":"s1","tool_response":{"exit_code":1.5}}`,
		`{"session_id":"s1","tool_response":{"exit_code":1e100}}`,
	} {
		parsed, err := ParsePayload([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		if parsed.ExitCode() != nil {
			t.Fatalf("invalid exit code was accepted: %s", payload)
		}
	}
	parsed, err := ParsePayload([]byte(`{"session_id":"s1","tool_response":{"exit_code":7}}`))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ExitCode() == nil || *parsed.ExitCode() != 7 {
		t.Fatalf("integer exit code was not preserved: %#v", parsed.ExitCode())
	}
}
