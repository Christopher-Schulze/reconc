package actionstate

import (
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestIdentityAuthorityInventoryIsClosedAndUnique(t *testing.T) {
	t.Parallel()
	bindings := IdentityAuthorities()
	want := []string{
		"principal", "role", "environment", "credential_labels", "server_label",
		"run_id", "session_id", "approval_authority", "expected_lock_digest",
		"executable", "working_directory", "repository", "server_identity",
		"policy_and_lock", "tool_name_and_contract",
	}
	if len(bindings) != len(want) {
		t.Fatalf("authority bindings = %d, want %d", len(bindings), len(want))
	}
	for index, binding := range bindings {
		if binding.Name != want[index] || binding.Owner == "" ||
			!binding.Provenance.Valid() || binding.Persistence == "" {
			t.Fatalf("authority binding[%d] = %#v", index, binding)
		}
		if index > 0 && binding.Name == bindings[index-1].Name {
			t.Fatalf("duplicate authority binding %q", binding.Name)
		}
	}
}

func TestOperatorContextBindsOnlyValidatedOperatorValues(t *testing.T) {
	t.Parallel()
	key := testIdentityKey(t, "k")
	credentialIdentity, err := CredentialIdentity(key, "warehouse", []byte("credential-value"))
	if err != nil {
		t.Fatal(err)
	}
	input := OperatorContext{
		Principal: "release-operator", Role: "operator", Environment: "production",
		Credentials: []CredentialBinding{{Label: "warehouse", Identity: credentialIdentity}},
		ServerLabel: "warehouse", RunID: "Run:42", SessionID: "session_7",
	}
	bound, err := input.Bind(key)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Provenance != action.ProvenanceOperatorBound ||
		!action.ValidKeyedIdentity(bound.RunIdentity) ||
		!action.ValidKeyedIdentity(bound.SessionIdentity) ||
		!action.ValidKeyedIdentity(bound.ContextIdentity) {
		t.Fatalf("bound context = %#v", bound)
	}
	body, err := json.Marshal(bound)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Run:42", "session_7", "credential-value"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("bound context exposed %q: %s", forbidden, body)
		}
	}
}

func TestOperatorContextRejectsEveryTrustUpgradeShape(t *testing.T) {
	t.Parallel()
	key := testIdentityKey(t, "k")
	base := OperatorContext{Principal: "operator", ServerLabel: "server"}
	tests := []struct {
		name   string
		mutate func(*OperatorContext)
	}{
		{name: "principal normalization", mutate: func(value *OperatorContext) { value.Principal = " Operator " }},
		{name: "role case", mutate: func(value *OperatorContext) { value.Role = "Admin" }},
		{name: "missing server", mutate: func(value *OperatorContext) { value.ServerLabel = "" }},
		{name: "run whitespace", mutate: func(value *OperatorContext) { value.RunID = "run 1" }},
		{name: "oversized session", mutate: func(value *OperatorContext) { value.SessionID = strings.Repeat("a", 129) }},
		{name: "duplicate credential", mutate: func(value *OperatorContext) {
			value.Credentials = []CredentialBinding{{Label: "db"}, {Label: "db"}}
		}},
		{name: "unkeyed credential", mutate: func(value *OperatorContext) {
			value.Credentials = []CredentialBinding{{Label: "db", Identity: "sha256:" + strings.Repeat("a", 64)}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := base
			test.mutate(&candidate)
			if _, err := candidate.Bind(key); err == nil {
				t.Fatal("unsafe operator context was accepted")
			}
		})
	}
}

func TestPolicyAuthorityRequiresOneExplicitMode(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	valid := []PolicyAuthority{
		{Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: digest},
		{Mode: action.AuthorityRepositoryManaged},
	}
	for _, authority := range valid {
		if err := authority.Validate(); err != nil {
			t.Fatalf("valid authority rejected: %v", err)
		}
	}
	invalid := []PolicyAuthority{
		{},
		{Mode: action.AuthorityOperatorPinned},
		{Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: strings.ToUpper(digest)},
		{Mode: action.AuthorityRepositoryManaged, ExpectedLockDigest: digest},
	}
	for _, authority := range invalid {
		if err := authority.Validate(); err == nil {
			t.Fatalf("invalid authority accepted: %#v", authority)
		}
	}
}

func TestOperatorContextRejectsCredentialFromDifferentKeyGeneration(t *testing.T) {
	t.Parallel()
	active := testIdentityKey(t, "m")
	other := testIdentityKey(t, "n")
	identity, err := CredentialIdentity(other, "database", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = (OperatorContext{
		Principal: "operator", ServerLabel: "database",
		Credentials: []CredentialBinding{{Label: "database", Identity: identity}},
	}).Bind(active)
	if err == nil || !strings.Contains(err.Error(), "active identity key") {
		t.Fatalf("mixed key generation error = %v", err)
	}
}

func TestOperatorContextCanonicalizesEmptyCredentialsToArray(t *testing.T) {
	t.Parallel()
	bound, err := (OperatorContext{Principal: "operator", ServerLabel: "database"}).Bind(testIdentityKey(t, "o"))
	if err != nil {
		t.Fatal(err)
	}
	if bound.Credentials == nil || len(bound.Credentials) != 0 {
		t.Fatalf("empty credentials = %#v", bound.Credentials)
	}
}

func testIdentityKey(t testing.TB, fill string) *IdentityKey {
	t.Helper()
	key, err := newIdentityKey([]byte(strings.Repeat(fill, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return key
}
