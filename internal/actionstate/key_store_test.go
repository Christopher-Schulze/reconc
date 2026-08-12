package actionstate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIdentityKeyCreateLoadAndRotateWithoutDependentState(t *testing.T) {
	home := privateTestHome(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 123, time.UTC)
	key, err := createIdentityKey(home, now, strings.NewReader(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := readStoredIdentityKey(home)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID() != key.ID() || loaded.Identity(DomainBudget, []byte("x")) != key.Identity(DomainBudget, []byte("x")) {
		t.Fatal("loaded key differs from created key")
	}
	if _, err := CreateIdentityKey(home, now); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate creation error = %v", err)
	}
	rotatedID, err := RotateIdentityKey(home, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if rotatedID == key.ID() {
		t.Fatal("rotation retained the old key")
	}
	loaded, err = readStoredIdentityKey(home)
	if err != nil || loaded.ID() != rotatedID {
		t.Fatalf("rotated key was not published: %v", err)
	}
}

func TestIdentityKeyRotationLeavesOldGenerationActiveWhenStateExists(t *testing.T) {
	home := privateTestHome(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	key, err := createIdentityKey(home, now, strings.NewReader(strings.Repeat("y", 32)))
	if err != nil {
		t.Fatal(err)
	}
	actionDir, err := ensurePrivateSubdirectories(home, "projects", "0123456789abcdef", "action")
	if err != nil {
		t.Fatal(err)
	}
	writePrivateTestFile(t, filepath.Join(actionDir, "state.json"), []byte("dependent"))
	if _, err := RotateIdentityKey(home, now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "dependent action state") {
		t.Fatalf("rotation error = %v", err)
	}
	loaded, err := readStoredIdentityKey(home)
	if err != nil || loaded.ID() != key.ID() {
		t.Fatalf("blocked rotation changed active key: %v", err)
	}
}

func TestIdentityKeyStrictlyRejectsSymlinkAndUnknownFields(t *testing.T) {
	home := privateTestHome(t)
	other := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(other, []byte(`{"format_version":"1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	actionDir, err := ensurePrivateSubdirectories(home, "action")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(actionDir, "identity-key.json")
	if err := os.Symlink(other, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := readStoredIdentityKey(home); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
		t.Fatalf("symlink key error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	validTime := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	key := testIdentityKey(t, "z")
	if err := writeIdentityKey(path, key, validTime); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(body), "\n}", ",\n  \"unknown\": true\n}", 1)
	if err := os.WriteFile(path, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readStoredIdentityKey(home); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown key field error = %v", err)
	}
}

func readStoredIdentityKey(home string) (*IdentityKey, error) {
	return readIdentityKey(filepath.Join(home, "action", "identity-key.json"))
}

func TestIdentityKeyRotationWaitsForEverySharedGatewayLease(t *testing.T) {
	home := privateTestHome(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	original, err := createIdentityKey(home, now, strings.NewReader(strings.Repeat("r", 32)))
	if err != nil {
		t.Fatal(err)
	}
	first, err := AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	type rotationResult struct {
		keyID string
		err   error
	}
	result := make(chan rotationResult, 1)
	go func() {
		keyID, rotateErr := RotateIdentityKey(home, now.Add(time.Second))
		result <- rotationResult{keyID: keyID, err: rotateErr}
	}()
	select {
	case got := <-result:
		t.Fatalf("rotation bypassed shared leases: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		t.Fatalf("rotation bypassed second shared lease: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.err != nil || got.keyID == "" || got.keyID == original.ID() {
			t.Fatalf("rotation after shared leases = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("rotation did not proceed after every shared lease closed")
	}
}
