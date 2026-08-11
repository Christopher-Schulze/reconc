// Package actionstate owns trusted Action Plane identities and crash-safe
// cumulative state. It is the IO boundary around the pure action evaluator.
package actionstate

import (
	"fmt"
	"regexp"
	"sort"

	"reconc.dev/reconc/internal/action"
)

const MaxRunSessionIDBytes = 128

var runSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type AuthorityOwner string

const (
	OwnerGatewayLaunch       AuthorityOwner = "gateway_launch"
	OwnerOperatorConfig      AuthorityOwner = "operator_config"
	OwnerOperatingSystem     AuthorityOwner = "operating_system"
	OwnerPolicyCompiler      AuthorityOwner = "policy_compiler"
	OwnerDownstreamDiscovery AuthorityOwner = "downstream_discovery"
)

type AuthorityBinding struct {
	Name        string            `json:"name"`
	Owner       AuthorityOwner    `json:"owner"`
	Provenance  action.Provenance `json:"provenance"`
	Persistence string            `json:"persistence"`
}

// IdentityAuthorities is the closed authority inventory. No request metadata,
// MCP client field, repository policy, or downstream response appears here as
// an owner of a trusted identity.
func IdentityAuthorities() []AuthorityBinding {
	return []AuthorityBinding{
		{Name: "principal", Owner: OwnerGatewayLaunch, Provenance: action.ProvenanceOperatorBound, Persistence: "safe-label"},
		{Name: "role", Owner: OwnerGatewayLaunch, Provenance: action.ProvenanceOperatorBound, Persistence: "safe-label"},
		{Name: "environment", Owner: OwnerGatewayLaunch, Provenance: action.ProvenanceOperatorBound, Persistence: "safe-label"},
		{Name: "credential_labels", Owner: OwnerGatewayLaunch, Provenance: action.ProvenanceOperatorBound, Persistence: "safe-labels-only"},
		{Name: "server_label", Owner: OwnerGatewayLaunch, Provenance: action.ProvenanceOperatorBound, Persistence: "safe-label"},
		{Name: "run_id", Owner: OwnerGatewayLaunch, Provenance: action.ProvenanceOperatorBound, Persistence: "keyed-identity-only"},
		{Name: "session_id", Owner: OwnerGatewayLaunch, Provenance: action.ProvenanceOperatorBound, Persistence: "keyed-identity-only"},
		{Name: "approval_authority", Owner: OwnerOperatorConfig, Provenance: action.ProvenanceOperatorBound, Persistence: "safe-key-id"},
		{Name: "expected_lock_digest", Owner: OwnerGatewayLaunch, Provenance: action.ProvenanceOperatorBound, Persistence: "sha256"},
		{Name: "executable", Owner: OwnerOperatingSystem, Provenance: action.ProvenanceHostObserved, Persistence: "sha256"},
		{Name: "working_directory", Owner: OwnerOperatingSystem, Provenance: action.ProvenanceHostObserved, Persistence: "keyed-identity-only"},
		{Name: "repository", Owner: OwnerOperatingSystem, Provenance: action.ProvenanceHostObserved, Persistence: "keyed-identity-only"},
		{Name: "server_identity", Owner: OwnerOperatingSystem, Provenance: action.ProvenanceHostObserved, Persistence: "keyed-identity-only"},
		{Name: "policy_and_lock", Owner: OwnerPolicyCompiler, Provenance: action.ProvenanceHostObserved, Persistence: "sha256"},
		{Name: "tool_name_and_contract", Owner: OwnerDownstreamDiscovery, Provenance: action.ProvenanceHostObserved, Persistence: "public-contract-digest"},
	}
}

type CredentialBinding struct {
	Label    string `json:"label"`
	Identity string `json:"identity,omitempty"`
}

type OperatorContext struct {
	Principal   string              `json:"principal"`
	Role        string              `json:"role,omitempty"`
	Environment string              `json:"environment,omitempty"`
	Credentials []CredentialBinding `json:"credentials"`
	ServerLabel string              `json:"server_label"`
	RunID       string              `json:"-"`
	SessionID   string              `json:"-"`
}

type BoundContext struct {
	Principal       string              `json:"principal"`
	Role            string              `json:"role,omitempty"`
	Environment     string              `json:"environment,omitempty"`
	Credentials     []CredentialBinding `json:"credentials"`
	ServerLabel     string              `json:"server_label"`
	RunIdentity     string              `json:"run_identity,omitempty"`
	SessionIdentity string              `json:"session_identity,omitempty"`
	ContextIdentity string              `json:"context_identity"`
	Provenance      action.Provenance   `json:"provenance"`
}

func (c OperatorContext) Validate() error {
	if !action.SafeLabel(c.Principal) {
		return fmt.Errorf("principal must be an exact safe label")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "role", value: c.Role},
		{name: "environment", value: c.Environment},
		{name: "server_label", value: c.ServerLabel},
	} {
		if field.value != "" && !action.SafeLabel(field.value) {
			return fmt.Errorf("%s must be an exact safe label when present", field.name)
		}
	}
	if c.ServerLabel == "" {
		return fmt.Errorf("server_label is required")
	}
	if c.RunID != "" && !ValidRunSessionID(c.RunID) {
		return fmt.Errorf("run_id must match [A-Za-z0-9._:-]{1,%d}", MaxRunSessionIDBytes)
	}
	if c.SessionID != "" && !ValidRunSessionID(c.SessionID) {
		return fmt.Errorf("session_id must match [A-Za-z0-9._:-]{1,%d}", MaxRunSessionIDBytes)
	}
	credentials := append([]CredentialBinding(nil), c.Credentials...)
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].Label < credentials[j].Label })
	if credentials == nil {
		credentials = []CredentialBinding{}
	}
	for index, credential := range credentials {
		if !action.SafeLabel(credential.Label) {
			return fmt.Errorf("credential label must be an exact safe label")
		}
		if credential.Identity != "" && !action.ValidKeyedIdentity(credential.Identity) {
			return fmt.Errorf("credential identity must use the keyed identity contract")
		}
		if index > 0 && credentials[index-1].Label == credential.Label {
			return fmt.Errorf("credential label %q is duplicated", credential.Label)
		}
	}
	return nil
}

func (c OperatorContext) Bind(key *IdentityKey) (BoundContext, error) {
	if err := c.Validate(); err != nil {
		return BoundContext{}, err
	}
	if key == nil {
		return BoundContext{}, fmt.Errorf("identity key is unavailable")
	}
	credentials := append([]CredentialBinding(nil), c.Credentials...)
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].Label < credentials[j].Label })
	for _, credential := range credentials {
		if credential.Identity != "" && !identityUsesKey(credential.Identity, key.ID()) {
			return BoundContext{}, fmt.Errorf("credential identity does not use the active identity key")
		}
	}
	if credentials == nil {
		credentials = []CredentialBinding{}
	}
	runIdentity := ""
	if c.RunID != "" {
		runIdentity = key.Identity(DomainRun, []byte(c.RunID))
	}
	sessionIdentity := ""
	if c.SessionID != "" {
		sessionIdentity = key.Identity(DomainSession, []byte(c.SessionID))
	}
	return BoundContext{
		Principal: c.Principal, Role: c.Role, Environment: c.Environment,
		Credentials: credentials, ServerLabel: c.ServerLabel,
		RunIdentity: runIdentity, SessionIdentity: sessionIdentity,
		ContextIdentity: contextIdentity(
			key, c.Principal, c.Role, c.Environment, c.ServerLabel,
			runIdentity, sessionIdentity, credentials,
		),
		Provenance: action.ProvenanceOperatorBound,
	}, nil
}

func contextIdentity(
	key *IdentityKey,
	principal, role, environment, serverLabel, runIdentity, sessionIdentity string,
	credentials []CredentialBinding,
) string {
	parts := [][]byte{
		[]byte(principal), []byte(role), []byte(environment), []byte(serverLabel),
		[]byte(runIdentity), []byte(sessionIdentity),
	}
	for _, credential := range credentials {
		parts = append(parts, []byte(credential.Label), []byte(credential.Identity))
	}
	return key.Identity(DomainContext, parts...)
}

func ValidRunSessionID(value string) bool {
	return len(value) <= MaxRunSessionIDBytes && runSessionIDPattern.MatchString(value)
}

type PolicyAuthority struct {
	Mode               action.AuthorityMode `json:"mode"`
	ExpectedLockDigest string               `json:"expected_lock_digest,omitempty"`
}

func (a PolicyAuthority) Validate() error {
	switch a.Mode {
	case action.AuthorityOperatorPinned:
		if !validLowerHex(a.ExpectedLockDigest, 64) {
			return fmt.Errorf("operator_pinned authority requires one exact lowercase lock digest")
		}
	case action.AuthorityRepositoryManaged:
		if a.ExpectedLockDigest != "" {
			return fmt.Errorf("repository_managed authority forbids an expected lock digest")
		}
	default:
		return fmt.Errorf("exactly one explicit policy authority mode is required")
	}
	return nil
}

// VerifyLockDigest binds one freshly observed lock digest to the explicit
// operator-selected authority mode. Repository-managed mode still requires a
// canonical observed digest, but does not pretend that it was operator-pinned.
func (a PolicyAuthority) VerifyLockDigest(observed string) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if !validLowerHex(observed, 64) {
		return fmt.Errorf("fresh lock digest must be exact lowercase SHA-256")
	}
	if a.Mode == action.AuthorityOperatorPinned && observed != a.ExpectedLockDigest {
		return fmt.Errorf("fresh lock digest does not match the operator-pinned digest")
	}
	return nil
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}
