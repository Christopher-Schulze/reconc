package actionstate

import (
	"os"
	"path/filepath"
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

func TestPrivateProjectStorageSecuresAndValidatesOnlyBoundJSONLPaths(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	storage, err := fixture.store.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	if storage.JSONLSecurityIdentity() != privateJSONLSecurityIdentity {
		t.Fatalf("JSONL security identity = %q", storage.JSONLSecurityIdentity())
	}
	path := filepath.Join(storage.ActionDirectory(), "ledger.jsonl")
	if err := os.WriteFile(path, []byte("record\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.ValidateJSONLFile(path, 64); err == nil {
		t.Fatal("unsafe JSONL file was accepted before publication security")
	}
	if err := storage.SecureJSONLFile(path); err != nil {
		t.Fatal(err)
	}
	if err := storage.ValidateJSONLFile(path, 64); err != nil {
		t.Fatal(err)
	}
	if err := storage.ValidateJSONLDirectory(storage.ActionDirectory()); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(storage.ActionDirectory()), "outside.jsonl")
	if err := storage.SecureJSONLFile(outside); err == nil {
		t.Fatal("private JSONL capability accepted an outside path")
	}
	if err := storage.ValidateJSONLDirectory(filepath.Dir(storage.ActionDirectory())); err == nil {
		t.Fatal("private JSONL capability accepted an outside directory")
	}
}
