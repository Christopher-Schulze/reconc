package agentsession

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStopPolicyScanCacheStableRejectsMissingAndUnreadableLockIdentity(t *testing.T) {
	repo := t.TempDir()
	cache := &stopPolicyScanCache{}
	if cache.stable(repo, nil) {
		t.Fatal("missing lock identity reported stable")
	}

	lockPath := filepath.Join(repo, policyLockfilePath)
	if err := os.MkdirAll(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if cache.stable(repo, nil) {
		t.Fatal("unreadable lock identity reported stable")
	}
	if (*stopPolicyScanCache)(nil).stable(repo, nil) {
		t.Fatal("absent cache reported stable without a lock identity")
	}
}
