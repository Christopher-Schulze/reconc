package mcpgateway

import (
	"testing"

	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionstate"
)

// A call's identity is issued after its slot wait; its reservation and
// approvals then follow within the call timeout, one approval wait, and the
// timeout of the resumed call. That chain must stay inside the window in
// which the action state admits the identity.
func TestCallTimeoutsStayInsideTheCallIdentityAdmissionWindow(t *testing.T) {
	chain := 2*MaximumCallTimeout + actionapproval.MaximumApprovalTTL + actionstate.CallIdentityClockSkew
	if chain >= actionstate.CallIdentityAdmissionWindow {
		t.Fatalf("call timeouts span %s, not inside the %s admission window", chain, actionstate.CallIdentityAdmissionWindow)
	}
}
