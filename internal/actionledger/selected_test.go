package actionledger

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

func TestSelectedFieldRejectsCanonicalExpansionBeyondLedgerBound(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	value, err := action.String(strings.Repeat("<", action.MaxArgumentBytes/6+1))
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.SelectedField(SelectedFieldInput{
		PolicyDigest: strings.Repeat("1", 64), LockDigest: strings.Repeat("2", 64),
		ToolContractDigest: "sha256:" + strings.Repeat("3", 64),
		Source:             action.SourceArguments, Pointer: "/payload",
		Selected: action.PointerResult{State: action.PointerPresent, Value: value},
	})
	if err == nil || !strings.Contains(err.Error(), "canonical selected field exceeds") {
		t.Fatalf("SelectedField() error = %v", err)
	}
}

func TestSelectedFieldIdentityBindsCanonicalDeclarationIndex(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	value, err := action.String("value")
	if err != nil {
		t.Fatal(err)
	}
	input := SelectedFieldInput{
		PolicyDigest: strings.Repeat("1", 64), LockDigest: strings.Repeat("2", 64),
		ToolContractDigest: "sha256:" + strings.Repeat("3", 64),
		Source:             action.SourceArguments, Pointer: "/payload",
		Selected: action.PointerResult{State: action.PointerPresent, Value: value},
	}
	first, err := fixture.store.SelectedField(input)
	if err != nil {
		t.Fatal(err)
	}
	input.DeclarationIndex = 1
	second, err := fixture.store.SelectedField(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeclarationIndex != 0 || second.DeclarationIndex != 1 ||
		first.PointerIdentity == second.PointerIdentity || first.ValueIdentity == second.ValueIdentity {
		t.Fatalf("selected declaration identities are not index-bound: first=%#v second=%#v", first, second)
	}
}

func TestSelectedFieldIdentityBindsRepositoryIdentity(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	value, err := action.String("value")
	if err != nil {
		t.Fatal(err)
	}
	input := SelectedFieldInput{
		PolicyDigest: strings.Repeat("1", 64), LockDigest: strings.Repeat("2", 64),
		ToolContractDigest: "sha256:" + strings.Repeat("3", 64),
		Source:             action.SourceArguments, Pointer: "/payload",
		Selected: action.PointerResult{State: action.PointerPresent, Value: value},
	}
	first, err := fixture.store.SelectedField(input)
	if err != nil {
		t.Fatal(err)
	}
	state, err := actionstate.OpenStore(actionstate.StoreOptions{
		Home: fixture.home, Repository: t.TempDir(), KeyLease: fixture.lease,
		OwnerID: "ledger-selected-repository-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := state.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := OpenStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondStore.SelectedField(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.PointerIdentity == second.PointerIdentity || first.ValueIdentity == second.ValueIdentity {
		t.Fatalf("selected identities correlate across repositories: first=%#v second=%#v", first, second)
	}
}

func TestSelectedFieldPreservesDepthFailureReason(t *testing.T) {
	value, err := action.String("value")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := countSelectedItems(value, action.MaxJSONDepth+1); err == nil ||
		!strings.Contains(err.Error(), "exceeds depth") {
		t.Fatalf("countSelectedItems() error = %v", err)
	}
}
