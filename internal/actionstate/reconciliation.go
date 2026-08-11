package actionstate

import (
	"fmt"
	"strconv"
)

// NewReconciliationAuthorization binds one operator-keyed maintenance proof to
// one exact indeterminate reservation transition. It is not an independent
// human-approval receipt.
func NewReconciliationAuthorization(
	key *IdentityKey,
	reservationIdentity string,
	expectedStateVersion string,
	outcome TerminalOutcome,
	actualResultBytes uint64,
) (string, error) {
	if key == nil || !identityUsesKey(reservationIdentity, key.ID()) ||
		!identityUsesKey(expectedStateVersion, key.ID()) ||
		(outcome != OutcomeSucceeded && outcome != OutcomeFailed && outcome != OutcomeIndeterminateCommitted) ||
		outcome == OutcomeIndeterminateCommitted && actualResultBytes != 0 {
		return "", fmt.Errorf("reconciliation authorization input is invalid")
	}
	return key.Identity(
		DomainApproval, []byte("reconcile-indeterminate"), []byte(reservationIdentity),
		[]byte(expectedStateVersion), []byte(outcome), []byte(strconv.FormatUint(actualResultBytes, 10)),
	), nil
}

// NewOwnerAbandonmentAuthorization binds one operator-keyed maintenance proof
// to one exact owner and state version.
func NewOwnerAbandonmentAuthorization(
	key *IdentityKey,
	ownerID string,
	expectedStateVersion string,
) (string, error) {
	if key == nil || !validOpaqueStateIdentity(ownerID) ||
		!identityUsesKey(expectedStateVersion, key.ID()) {
		return "", fmt.Errorf("owner-abandonment authorization input is invalid")
	}
	return key.Identity(
		DomainApproval, []byte("mark-owner-abandoned"), []byte(ownerID), []byte(expectedStateVersion),
	), nil
}
