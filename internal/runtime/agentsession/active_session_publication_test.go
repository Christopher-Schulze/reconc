package agentsession

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/retention"
)

func TestActiveSessionFirstPublicationWaitsForProjectRetentionLock(t *testing.T) {
	stateDirectory, repository := withStateRoot(t)
	resolved, err := ResolveRepoRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := privatefs.OpenLock(filepath.Join(stateDirectory, retention.ProjectRootRetentionLockName))
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := filelock.LockContext(context.Background(), lock, time.Second)
	if err != nil {
		_ = lock.Close()
		t.Fatal(err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			_ = unlock()
		}
		_ = lock.Close()
	})

	started := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		close(started)
		returned <- writeActiveSession(resolved, "retention-serialized")
	}()
	<-started
	select {
	case writeErr := <-returned:
		t.Fatalf("first publication bypassed project retention lock: %v", writeErr)
	case <-time.After(250 * time.Millisecond):
	}

	unlockErr := unlock()
	released = true
	if unlockErr != nil {
		t.Fatal(unlockErr)
	}
	select {
	case writeErr := <-returned:
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first publication did not resume after project retention lock release")
	}
	for _, path := range []string{projectDir(resolved), filepath.Dir(activeSessionLockPath(resolved))} {
		if err := privatefs.ValidateDirectory(path); err != nil {
			t.Fatalf("validate published private directory %s: %v", path, err)
		}
	}
}
