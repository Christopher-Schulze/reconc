package actionstate

import (
	"strings"
	"testing"
)

func TestPrivateProjectStorageBindsDirectoryRepositoryAndKeyLease(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	storage, err := fixture.store.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	if storage.ActionDirectory() != fixture.store.directory ||
		storage.RepositoryIdentity() != fixture.store.repositoryIdentity ||
		storage.ProjectKey() != fixture.store.projectKey {
		t.Fatalf("private project storage does not match its store")
	}
	want := fixture.key.Identity(DomainLedger, []byte("selected-field"))
	var got string
	if err := storage.WithIdentity(func(key *IdentityKey) error {
		got = key.Identity(DomainLedger, []byte("selected-field"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != want || !strings.HasPrefix(got, "hmac-sha256:v1:") {
		t.Fatalf("ledger identity = %q, want %q", got, want)
	}
	if err := fixture.lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := storage.Validate(); err == nil {
		t.Fatalf("closed identity-key lease remained usable")
	}
}
