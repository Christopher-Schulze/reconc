package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func TestApprovalFixturePublishesOnlyItsSyntheticPublicKey(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"--public-key"}, bytes.NewReader(nil), &output); err != nil {
		t.Fatal(err)
	}
	want := base64.RawURLEncoding.EncodeToString(fixturePrivateKey().Public().(ed25519.PublicKey)) + "\n"
	if output.String() != want {
		t.Fatalf("fixture public key = %q, want %q", output.String(), want)
	}
}

func TestApprovalFixtureRejectsMalformedAndOversizedRequests(t *testing.T) {
	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "empty", body: nil},
		{name: "malformed", body: []byte(`{"schema":"wrong"}`)},
		{name: "oversized", body: bytes.Repeat([]byte{'x'}, (64<<10)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := run(nil, bytes.NewReader(test.body), &bytes.Buffer{}); err == nil {
				t.Fatal("invalid approval request was signed")
			}
		})
	}
}

func TestApprovalFixtureSignsTheExactCanonicalRequest(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	request, err := actionapproval.NewRequest(actionapproval.RequestInput{
		CallID:                   "act_" + strings.Repeat("a", 26),
		RequestIdentity:          fixtureKeyedIdentity('1'),
		RequiredApprovalIdentity: fixtureSHAIdentity('2'),
		PlanIdentity:             fixtureSHAIdentity('3'),
		SourceIdentity:           strings.Repeat("4", 64),
		RepositoryIdentity:       fixtureKeyedIdentity('5'),
		StateVersion:             fixtureKeyedIdentity('6'),
		PolicyDigest:             strings.Repeat("7", 64),
		LockDigest:               strings.Repeat("8", 64),
		ExecutableDigest:         fixtureSHAIdentity('9'),
		ServerLabel:              "langchain-fixture",
		ServerFingerprint:        fixtureKeyedIdentity('a'),
		ToolID:                   "fixture-approval",
		Tool:                     "approval",
		ToolContractDigest:       fixtureSHAIdentity('b'),
		Phase:                    action.PhasePreCall,
		Principal:                "integration-test",
		ContextIdentity:          fixtureKeyedIdentity('c'),
		CredentialLabels:         []string{},
		TaintIdentity:            "taint-clean",
		RepositoryEffectIdentity: "effect-absent",
		SelectedArguments:        []actionapproval.SelectedArgument{},
		BudgetReservationID:      fixtureKeyedIdentity('d'),
		ReasonCode:               action.ReasonApprovalRequired,
		RuleIDs:                  []string{"fixture-approval-required"},
		AuthorityPolicyID:        "langchain-integration",
		IssuedAt:                 now,
		TTL:                      30 * time.Second,
	}, bytes.NewReader(bytes.Repeat([]byte{0x31}, 48)))
	if err != nil {
		t.Fatal(err)
	}
	body, err := actionapproval.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(nil, bytes.NewReader(body), &output); err != nil {
		t.Fatal(err)
	}
	receipt, err := actionapproval.DecodeReceipt(output.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	equal, err := actionapproval.RequestsEqual(receipt.Request, request)
	if err != nil || !equal || receipt.AuthorityKeyID != fixtureAuthorityID ||
		receipt.Decision != actionapproval.DecisionApprove {
		t.Fatalf("fixture receipt = %#v, equal=%t, err=%v", receipt, equal, err)
	}
}

func fixtureKeyedIdentity(fill byte) string {
	return "hmac-sha256:v1:" + strings.Repeat("1", 32) + ":" + strings.Repeat(string(fill), 64)
}

func fixtureSHAIdentity(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}
