//go:build !windows

package actionstate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAcquireExistingIdentityKeyRejectsInsecureLockWithoutMutation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "reconc-home")
	if _, err := CreateIdentityKey(home, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		home,
		filepath.Join(home, "action"),
		filepath.Join(home, "action", "identity-key.lock"),
		filepath.Join(home, "action", "identity-key.json"),
	}
	if err := os.Chmod(paths[2], 0o644); err != nil {
		t.Fatal(err)
	}
	before := existingMetadataForTest(t, paths)
	if _, err := AcquireExistingIdentityKey(context.Background(), home); err == nil {
		t.Fatal("existing identity-key acquisition accepted an insecure lock mode")
	}
	if after := existingMetadataForTest(t, paths); !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected identity-key read changed metadata: before=%#v after=%#v", before, after)
	}
}
