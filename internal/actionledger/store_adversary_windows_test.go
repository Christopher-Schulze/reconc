//go:build windows

package actionledger

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"

	"reconc.dev/reconc/internal/action"
)

func makeLedgerFileUnsafe(t testing.TB, path string) {
	t.Helper()
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	var pinner runtime.Pinner
	pinner.Pin(everyone)
	defer pinner.Unpin()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(everyone)
}

func TestStoreRejectsUnsafeWindowsLockDACLWithoutMutatingLedger(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	fixture.append(t, EventRequestAccepted)
	before, err := os.ReadFile(fixture.store.livePath)
	if err != nil {
		t.Fatal(err)
	}
	makeLedgerFileUnsafe(t, fixture.store.layout.LockPath)
	decision := fixture.record(EventPreDecision)
	decision.Decision.Decision = action.DecisionBlock
	decision.Decision.Reason = action.ReasonRuleMatched
	decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
	if _, err := fixture.store.Append(context.Background(), decision); err == nil ||
		ErrorCode(err) != action.ReasonLedgerCorrupt {
		t.Fatalf("Append() error = %v", err)
	}
	after, err := os.ReadFile(fixture.store.livePath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("rejected append changed ledger: err=%v", err)
	}
	if err := fixture.storage.ValidateJSONLFile(fixture.store.layout.LockPath, 4<<10); err == nil {
		t.Fatal("rejected append repaired the unsafe Windows lock DACL")
	}
}

func TestExistingStateRejectsUnsafeWindowsLockDACLWithoutRepair(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	fixture.append(t, EventRequestAccepted)
	makeLedgerFileUnsafe(t, fixture.store.layout.LockPath)
	if _, err := fixture.store.ExistingState(context.Background()); err == nil ||
		ErrorCode(err) != action.ReasonLedgerCorrupt {
		t.Fatalf("ExistingState() error = %v", err)
	}
	if err := fixture.storage.ValidateJSONLFile(fixture.store.layout.LockPath, 4<<10); err == nil {
		t.Fatal("rejected read repaired the unsafe Windows lock DACL")
	}
}
