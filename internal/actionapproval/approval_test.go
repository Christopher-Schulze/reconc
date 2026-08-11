package actionapproval

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

func TestApprovalReceiptCanonicalSignatureAndBinding(t *testing.T) {
	request, registry, privateKey, now := testApprovalFixture(t)
	receipt, body, err := SignReceipt(
		request, "security-primary", privateKey, DecisionApprove,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x42}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReceipt(body)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ReceiptID != receipt.ReceiptID || !bytes.Equal(body, mustEncodeReceipt(t, decoded)) {
		t.Fatal("canonical receipt did not round-trip exactly")
	}
	verified, err := VerifyReceipt(registry, request, body, now)
	if err != nil || !action.ValidSHA256Identity(verified.Identity) {
		t.Fatalf("verify canonical receipt: %#v, %v", verified, err)
	}

	mutations := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{name: "receipt schema", mutate: func(value *Receipt) { value.Schema = "reconc.action-approval-receipt/v2" }},
		{name: "receipt format", mutate: func(value *Receipt) { value.FormatVersion = "2" }},
		{name: "request schema", mutate: func(value *Receipt) { value.Request.Schema = "reconc.action-approval-request/v2" }},
		{name: "request format", mutate: func(value *Receipt) { value.Request.FormatVersion = "2" }},
		{name: "request id", mutate: func(value *Receipt) { value.Request.RequestID = testPrefixedID("apr_", 'b') }},
		{name: "call id", mutate: func(value *Receipt) { value.Request.CallID = testPrefixedID("act_", 'b') }},
		{name: "request identity", mutate: func(value *Receipt) { value.Request.RequestIdentity = testKeyedIdentity('b') }},
		{name: "required approval", mutate: func(value *Receipt) { value.Request.RequiredApprovalIdentity = testSHAIdentity('b') }},
		{name: "plan", mutate: func(value *Receipt) { value.Request.PlanIdentity = testSHAIdentity('b') }},
		{name: "source", mutate: func(value *Receipt) { value.Request.SourceIdentity = strings.Repeat("b", 64) }},
		{name: "repository", mutate: func(value *Receipt) { value.Request.RepositoryIdentity = testKeyedIdentity('b') }},
		{name: "state", mutate: func(value *Receipt) { value.Request.StateVersion = testKeyedIdentity('b') }},
		{name: "policy", mutate: func(value *Receipt) { value.Request.PolicyDigest = strings.Repeat("b", 64) }},
		{name: "lock", mutate: func(value *Receipt) { value.Request.LockDigest = strings.Repeat("b", 64) }},
		{name: "executable", mutate: func(value *Receipt) { value.Request.ExecutableDigest = testSHAIdentity('b') }},
		{name: "server label", mutate: func(value *Receipt) { value.Request.ServerLabel = "backup" }},
		{name: "server fingerprint", mutate: func(value *Receipt) { value.Request.ServerFingerprint = testKeyedIdentity('b') }},
		{name: "tool id", mutate: func(value *Receipt) { value.Request.ToolID = "backup-tool" }},
		{name: "tool", mutate: func(value *Receipt) { value.Request.Tool = "backup" }},
		{name: "tool contract", mutate: func(value *Receipt) { value.Request.ToolContractDigest = testSHAIdentity('b') }},
		{name: "phase", mutate: func(value *Receipt) { value.Request.Phase = action.PhasePostResult }},
		{name: "principal", mutate: func(value *Receipt) { value.Request.Principal = "backup-operator" }},
		{name: "context", mutate: func(value *Receipt) { value.Request.ContextIdentity = testKeyedIdentity('b') }},
		{name: "credentials", mutate: func(value *Receipt) { value.Request.CredentialLabels = []string{"secondary"} }},
		{name: "taint", mutate: func(value *Receipt) { value.Request.TaintIdentity = "taint-backup" }},
		{name: "repository effect", mutate: func(value *Receipt) { value.Request.RepositoryEffectIdentity = "effect-backup" }},
		{name: "selected argument pointer", mutate: func(value *Receipt) { value.Request.SelectedArguments[0].Pointer = "/other" }},
		{name: "selected argument state", mutate: func(value *Receipt) { value.Request.SelectedArguments[0].State = action.PointerNull }},
		{name: "selected argument kind", mutate: func(value *Receipt) { value.Request.SelectedArguments[0].Kind = action.ValueObject }},
		{name: "selected argument bytes", mutate: func(value *Receipt) { value.Request.SelectedArguments[0].ByteLength++ }},
		{name: "selected argument", mutate: func(value *Receipt) { value.Request.SelectedArguments[0].Identity = testKeyedIdentity('b') }},
		{name: "budget", mutate: func(value *Receipt) { value.Request.BudgetReservationID = "absent" }},
		{name: "reason", mutate: func(value *Receipt) { value.Request.ReasonCode = action.ReasonConditionIndeterminate }},
		{name: "rules", mutate: func(value *Receipt) { value.Request.RuleIDs = []string{"backup-rule"} }},
		{name: "authority policy", mutate: func(value *Receipt) { value.Request.AuthorityPolicyID = "backup-policy" }},
		{name: "issued", mutate: func(value *Receipt) { value.Request.IssuedAt = now.Add(time.Second).Format(time.RFC3339Nano) }},
		{name: "expiry", mutate: func(value *Receipt) { value.Request.ExpiresAt = now.Add(61 * time.Second).Format(time.RFC3339Nano) }},
		{name: "nonce", mutate: func(value *Receipt) {
			value.Request.Nonce = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
		}},
		{name: "decision", mutate: func(value *Receipt) { value.Decision = DecisionReject }},
		{name: "authority", mutate: func(value *Receipt) { value.AuthorityKeyID = "security-secondary" }},
		{name: "receipt id", mutate: func(value *Receipt) { value.ReceiptID = testPrefixedID("arc_", 'b') }},
		{name: "signed", mutate: func(value *Receipt) { value.SignedAt = now.Add(time.Second).Format(time.RFC3339Nano) }},
		{name: "signature", mutate: func(value *Receipt) {
			value.Signature = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, ed25519.SignatureSize))
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := receipt
			changed.Request = cloneRequest(receipt.Request)
			mutation.mutate(&changed)
			changedBody, encodeErr := canonicalJSON(changed)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if _, verifyErr := VerifyReceipt(registry, request, changedBody, now); verifyErr == nil {
				t.Fatal("altered receipt was accepted")
			}
		})
	}
}

func TestApprovalReceiptRejectsCanonicalSignatureTimeAndAuthorityFailures(t *testing.T) {
	request, registry, privateKey, now := testApprovalFixture(t)
	_, approvedBody, err := SignReceipt(
		request, "security-primary", privateKey, DecisionApprove,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x31}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, rejectedBody, err := SignReceipt(
		request, "security-primary", privateKey, DecisionReject,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x32}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body []byte
		now  time.Time
		code action.ReasonCode
	}{
		{name: "reject", body: rejectedBody, now: now, code: action.ReasonApprovalRejected},
		{name: "expired", body: approvedBody, now: now.Add(time.Minute), code: action.ReasonApprovalExpired},
		{name: "non canonical", body: append([]byte(" "), approvedBody...), now: now, code: action.ReasonApprovalInvalid},
		{name: "duplicate key", body: bytes.Replace(approvedBody, []byte(`{"authority_key_id":`), []byte(`{"authority_key_id":"security-primary","authority_key_id":`), 1), now: now, code: action.ReasonApprovalInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, verifyErr := VerifyReceipt(registry, request, test.body, test.now)
			var approvalErr *ApprovalError
			if !errors.As(verifyErr, &approvalErr) || approvalErr.Code != test.code {
				t.Fatalf("error = %v, want %s", verifyErr, test.code)
			}
		})
	}
}

func TestApprovalDecodersRejectCaseFoldedFieldAliases(t *testing.T) {
	request, _, privateKey, now := testApprovalFixture(t)
	requestBody, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	_, receiptBody, err := SignReceipt(
		request, "security-primary", privateKey, DecisionApprove,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x33}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		body   []byte
		decode func([]byte) error
	}{
		{
			name: "request nested field alias",
			body: bytes.Replace(
				requestBody, []byte(`"selected_arguments":`), []byte(`"selectEd_arguments":`), 1,
			),
			decode: func(body []byte) error { _, err := DecodeRequest(body); return err },
		},
		{
			name:   "receipt top-level field alias",
			body:   bytes.Replace(receiptBody, []byte(`"signature":`), []byte(`"Signature":`), 1),
			decode: func(body []byte) error { _, err := DecodeReceipt(body); return err },
		},
		{
			name: "receipt alias collision",
			body: bytes.Replace(
				receiptBody,
				[]byte(`"signature":`),
				[]byte(`"Signature":"invalid","signature":`),
				1,
			),
			decode: func(body []byte) error { _, err := DecodeReceipt(body); return err },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.decode(test.body); err == nil {
				t.Fatal("case-folded JSON field alias was accepted")
			}
		})
	}
}

func TestApprovalReceiptEnforcesTrustedClockAndAuthorityBoundaries(t *testing.T) {
	request, registry, privateKey, now := testApprovalFixture(t)
	sign := func(t testing.TB, value Request, keyID string) []byte {
		t.Helper()
		signedAt, parseErr := parseCanonicalTime(value.IssuedAt)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		_, body, err := SignReceipt(
			value, keyID, privateKey, DecisionApprove,
			signedAt,
			bytes.NewReader(bytes.Repeat([]byte{0x63}, 16)),
		)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}

	atSkew := cloneRequest(request)
	atSkew.IssuedAt = now.Add(MaximumApprovalFutureSkew).Format(time.RFC3339Nano)
	atSkew.ExpiresAt = now.Add(MaximumApprovalFutureSkew + 30*time.Second).Format(time.RFC3339Nano)
	if _, err := VerifyReceipt(registry, atSkew, sign(t, atSkew, "security-primary"), now); err != nil {
		t.Fatalf("exact future-skew boundary was rejected: %v", err)
	}
	beyondSkew := cloneRequest(atSkew)
	beyondSkew.IssuedAt = now.Add(MaximumApprovalFutureSkew + time.Nanosecond).Format(time.RFC3339Nano)
	beyondSkew.ExpiresAt = now.Add(MaximumApprovalFutureSkew + 30*time.Second).Format(time.RFC3339Nano)
	if _, err := VerifyReceipt(registry, beyondSkew, sign(t, beyondSkew, "security-primary"), now); err == nil {
		t.Fatal("receipt issued beyond the future-skew boundary was accepted")
	}
	if _, err := VerifyReceipt(registry, request, sign(t, request, "security-primary"), time.Time{}); err == nil {
		t.Fatal("receipt was accepted without a trusted clock")
	}
	if _, err := VerifyReceipt(registry, request, sign(t, request, "security-other"), now); err == nil {
		t.Fatal("receipt from an unknown authority key was accepted")
	}
	inactive, err := CompileRegistry(testRegistry(
		privateKey.Public().(ed25519.PublicKey), now.Add(time.Second), time.Time{},
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyReceipt(inactive, request, sign(t, request, "security-primary"), now); err == nil {
		t.Fatal("receipt issued before authority activation was accepted")
	}
	if _, err := VerifyReceipt(registry, request, sign(t, request, "security-primary"), now.Add(30*time.Second)); err == nil {
		t.Fatal("receipt was accepted at its exact expiry")
	}
}

func TestApprovalRegistryRotationAndOperatorPolicy(t *testing.T) {
	request, _, privateKey, now := testApprovalFixture(t)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	registry, err := CompileRegistry(testRegistry(
		publicKey, now.Add(-time.Hour), now.Add(time.Second),
	))
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := SignReceipt(
		request, "security-primary", privateKey, DecisionApprove,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x21}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyReceipt(registry, request, body, now); err != nil {
		t.Fatalf("receipt issued before revocation was rejected: %v", err)
	}
	_, body, err = SignReceipt(
		request, "security-primary", privateKey, DecisionApprove,
		now.Add(2*time.Second),
		bytes.NewReader(bytes.Repeat([]byte{0x23}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyReceipt(registry, request, body, now.Add(2*time.Second)); err == nil {
		t.Fatal("receipt signed after revocation was accepted for an older request")
	}
	request.IssuedAt = now.Add(2 * time.Second).Format(time.RFC3339Nano)
	request.ExpiresAt = now.Add(30 * time.Second).Format(time.RFC3339Nano)
	_, body, err = SignReceipt(
		request, "security-primary", privateKey, DecisionApprove,
		now.Add(2*time.Second),
		bytes.NewReader(bytes.Repeat([]byte{0x22}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyReceipt(registry, request, body, now.Add(2*time.Second)); err == nil {
		t.Fatal("receipt issued after revocation was accepted")
	}
}

func TestApprovalProviderTimeoutAndBoundedCopy(t *testing.T) {
	request, _, _, _ := testApprovalFixture(t)
	provider := providerFunc(func(ctx context.Context, _ Request) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if _, err := RequestFromProvider(context.Background(), provider, request, time.Millisecond); err == nil {
		t.Fatal("timed-out provider was accepted")
	}
	provider = providerFunc(func(ctx context.Context, _ Request) ([]byte, error) {
		<-ctx.Done()
		return []byte(`{"late":"receipt"}`), nil
	})
	if _, err := RequestFromProvider(context.Background(), provider, request, time.Millisecond); err == nil {
		t.Fatal("provider response returned after timeout was accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	provider = providerFunc(func(context.Context, Request) ([]byte, error) {
		called = true
		return []byte(`{"receipt":"unexpected"}`), nil
	})
	if _, err := RequestFromProvider(cancelled, provider, request, time.Second); err == nil || called {
		t.Fatal("pre-cancelled approval request invoked its provider")
	}
	body := []byte(`{"receipt":"bounded"}`)
	provider = providerFunc(func(context.Context, Request) ([]byte, error) { return body, nil })
	got, err := RequestFromProvider(context.Background(), provider, request, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	body[0] = 'x'
	if got[0] != '{' {
		t.Fatal("provider response was not cloned")
	}
}

func TestApprovalRequestRejectsEveryToolControlCharacter(t *testing.T) {
	request, _, _, _ := testApprovalFixture(t)
	for _, tool := range []string{"database\x00execute", "database\nexecute", "database\u007fexecute"} {
		changed := cloneRequest(request)
		changed.Tool = tool
		if err := changed.Validate(); err == nil {
			t.Fatalf("approval request accepted controlled tool name %q", tool)
		}
	}
}

type providerFunc func(context.Context, Request) ([]byte, error)

func (f providerFunc) RequestApproval(ctx context.Context, request Request) ([]byte, error) {
	return f(ctx, request)
}

func testApprovalFixture(t testing.TB) (Request, *CompiledRegistry, ed25519.PrivateKey, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 11, 16, 0, 0, 0, time.UTC)
	seed := bytes.Repeat([]byte{0x11}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	registry, err := CompileRegistry(testRegistry(privateKey.Public().(ed25519.PublicKey), now.Add(-time.Hour), time.Time{}))
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewRequest(RequestInput{
		CallID: testPrefixedID("act_", 'a'), RequestIdentity: testKeyedIdentity('1'),
		RequiredApprovalIdentity: testSHAIdentity('2'), RepositoryIdentity: testKeyedIdentity('3'),
		PlanIdentity: testSHAIdentity('c'), SourceIdentity: strings.Repeat("d", 64),
		StateVersion: testKeyedIdentity('4'), PolicyDigest: strings.Repeat("5", 64),
		LockDigest: strings.Repeat("6", 64), ExecutableDigest: testSHAIdentity('7'),
		ServerLabel: "warehouse", ServerFingerprint: testKeyedIdentity('8'),
		ToolID: "database-write", Tool: "database.execute", ToolContractDigest: testSHAIdentity('9'),
		Phase: action.PhasePreCall, Principal: "release-operator", ContextIdentity: testKeyedIdentity('e'),
		CredentialLabels: []string{"production-database"},
		TaintIdentity:    "taint-clean", RepositoryEffectIdentity: "effect-absent",
		SelectedArguments: []SelectedArgument{{
			Pointer: "/query", State: action.PointerPresent, Kind: action.ValueString,
			ByteLength: 15, Identity: testKeyedIdentity('a'),
		}},
		BudgetReservationID: testKeyedIdentity('b'), ReasonCode: action.ReasonApprovalRequired,
		RuleIDs: []string{"approve-production-write"}, AuthorityPolicyID: "production-writes",
		IssuedAt: now, TTL: 30 * time.Second,
	}, bytes.NewReader(bytes.Repeat([]byte{0x41}, 48)))
	if err != nil {
		t.Fatal(err)
	}
	return request, registry, privateKey, now
}

func testRegistry(publicKey ed25519.PublicKey, active, revoked time.Time) Registry {
	revokedAt := ""
	if !revoked.IsZero() {
		revokedAt = revoked.UTC().Format(time.RFC3339Nano)
	}
	return Registry{
		Schema: RegistrySchema, FormatVersion: FormatVersion,
		Authorities: []Authority{{
			ID: "security-primary", PublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
			ActiveFrom: active.UTC().Format(time.RFC3339Nano), RevokedAt: revokedAt,
		}},
		AuthorityPolicies: []AuthorityPolicy{{
			ID: "production-writes", AuthorityKeyIDs: []string{"security-primary"},
		}},
	}
}

func testPrefixedID(prefix string, fill byte) string {
	return prefix + strings.Repeat(string(fill), 26)
}

func testKeyedIdentity(fill byte) string {
	return "hmac-sha256:v1:" + strings.Repeat("1", 32) + ":" + strings.Repeat(string(fill), 64)
}

func testSHAIdentity(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}

func mustEncodeReceipt(t *testing.T, receipt Receipt) []byte {
	t.Helper()
	body, err := EncodeReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
