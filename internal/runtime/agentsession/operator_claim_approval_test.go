package agentsession

import (
	"encoding/json"
	"testing"
)

func TestUserApprovedClaimCommandGuardRequiresLiteralBoundedAttestation(t *testing.T) {
	for _, tc := range []struct {
		command string
		allowed bool
	}{
		{"reconc hook claim . operator:edit --user-approved --for 1h", true},
		{"reconc hook claim . operator:edit --for 8h --user-approved --session s1", true},
		{"sh -c 'reconc hook claim . operator:edit --user-approved --for 1m'", true},
		{"reconc hook claim . operator:edit --for 1h", false},
		{"reconc hook claim . operator:edit --user-approved", false},
		{"reconc hook claim . operator:edit --user-approved --for 0s", false},
		{"reconc hook claim . operator:edit --user-approved --for 9h", false},
		{"reconc hook claim . operator:edit --session --user-approved --for 1h", false},
		{"reconc hook claim . operator:edit --output --user-approved --for 1h", false},
		{`reconc hook claim . "$CLAIM" --user-approved --for 1h`, false},
		{`reconc hook claim . operator:edit --user-approved --for "$DURATION"`, false},
		{`reconc hook claim . operator:edit "$FLAG" --for 1h`, false},
		{"reconc hook pause . --for 1h --user-approved", false},
		{"reconc hook resume . --user-approved", false},
		{"reconc hook evidence-resolve . --user-approved --for 1h", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			reason := operatorOnlyCommandReason(tc.command)
			if (reason == "") != tc.allowed {
				t.Fatalf("allowed=%v, reason=%q", tc.allowed, reason)
			}
		})
	}
}

func TestNativeHooksAdmitUserApprovedClaimWithoutGrantingIt(t *testing.T) {
	repo := setupOperatorClaimRepo(t)
	for _, tc := range []struct {
		tool  string
		input map[string]string
	}{
		{"Bash", map[string]string{"command": "reconc hook claim . operator:edit --user-approved --for 1h"}},
		{"functions.exec_command", map[string]string{"cmd": "reconc hook claim . operator:edit --user-approved --for 1h"}},
	} {
		body, err := json.Marshal(struct {
			Session string            `json:"session_id"`
			Tool    string            `json:"tool_name"`
			Input   map[string]string `json:"tool_input"`
		}{"approved", tc.tool, tc.input})
		if err != nil {
			t.Fatal(err)
		}
		if result := RunPreToolUse(repo, body); result.ExitCode != 0 {
			t.Fatalf("%s route blocked attestation: %+v", tc.tool, result)
		}
	}
	if claims := activeClaims(t, repo); len(claims) != 0 {
		t.Fatalf("pre-tool admission fabricated a grant: %v", claims)
	}
}

func TestApprovalQuoteValueNeverCountsAsTheAttestationFlag(t *testing.T) {
	for _, tc := range []struct {
		command string
		allowed bool
	}{
		{`reconc hook claim . operator:edit --user-approved --for 1h --approval-quote "ja, Diff freigegeben"`, true},
		{`reconc hook claim . operator:edit --for 1h --approval-quote --user-approved`, false},
		{`reconc hook claim . operator:edit --user-approved --for 1h --approval-quote`, false},
		{`reconc hook claim . operator:edit --user-approved --for 1h --approval-quote "$QUOTE"`, false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			reason := operatorOnlyCommandReason(tc.command)
			if (reason == "") != tc.allowed {
				t.Fatalf("allowed=%v, reason=%q", tc.allowed, reason)
			}
		})
	}
}
