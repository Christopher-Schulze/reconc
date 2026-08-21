package agentsession

import (
	"fmt"
	"sync"
	"testing"
)

func TestActiveSessionConcurrentWritersShareOnePointerLock(t *testing.T) {
	_, repository := withStateRoot(t)
	var err error
	repository, err = ResolveRepoRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 24
	// Keep the full writer fan-in that reproduces cross-session pointer races,
	// but do not turn a correctness regression into a 960-fsync soak test. Four
	// publication generations exercise repeated lock handoff without starving a
	// valid waiter behind the production 30-second contention bound on Windows.
	const rounds = 4
	start := make(chan struct{})
	errors := make(chan error, writers*rounds)
	var wait sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			for round := 0; round < rounds; round++ {
				sessionID := fmt.Sprintf("writer-%02d-round-%02d", index, round)
				if err := writeActiveSession(repository, sessionID); err != nil {
					errors <- err
				}
			}
		}(writer)
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatalf("concurrent active-session publication: %v", err)
	}
	sessionID, err := ResolveActiveSessionID(repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSessionID(sessionID); err != nil {
		t.Fatalf("final active session %q is invalid: %v", sessionID, err)
	}
}
