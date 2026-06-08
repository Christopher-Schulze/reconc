package agentsession

import (
	"sync"
	"testing"
)

// TestReconcileRunLoopToolEventConcurrencyKeepsEnabled reproduces the
// torn-read race that silently stopped an active runloop run mid-flight:
// parallel tool-event reconciles must never flip enabled true->false.
//
// Pre-fix (bare os.WriteFile truncate-then-write, no lock) a reader hitting
// the truncate window decoded an empty file into a disabled zero-value state
// and persisted enabled=false, after which every Stop took the
// allow_no_active_runloop branch and the agent halted. With atomic
// tmp+rename writes plus the per-state lock the run stays enabled under any
// amount of concurrent tool events.
func TestReconcileRunLoopToolEventConcurrencyKeepsEnabled(t *testing.T) {
	repo := t.TempDir()
	const sess = "race-session-0001"
	const rt = "cursor"

	// Seed an active run owned by sess/cursor.
	if err := saveRunLoopState(repo, runLoopState{
		Enabled:     true,
		SessionID:   sess,
		ActiveRunID: sess,
		Runtime:     rt,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	const workers = 24
	const iterations = 40
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			payload := &HookPayload{SessionID: sess}
			for i := 0; i < iterations; i++ {
				if err := reconcileRunLoopStateForRuntime(repo, sess, rt, payload, runLoopToolEvent); err != nil {
					t.Errorf("reconcile tool event: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	final, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatalf("load final state: %v", err)
	}
	if !final.Enabled {
		t.Fatalf("active runloop run was silently disabled by concurrent tool events: %+v", final)
	}
	if final.ActiveRunID != sess {
		t.Fatalf("active run id lost under concurrency: got %q want %q", final.ActiveRunID, sess)
	}
	if final.DisabledReason != "" {
		t.Fatalf("unexpected disabled reason under concurrency: %q", final.DisabledReason)
	}
}

// TestMutateRunLoopStateSerializesIncrements proves the lock serializes
// read-modify-write so concurrent mutators do not lose each other's updates.
// Each mutator increments NoProgressNudges; with a lost update the final count
// would be below the total number of mutations.
func TestMutateRunLoopStateSerializesIncrements(t *testing.T) {
	repo := t.TempDir()
	const sess = "mutate-session-0001"
	if err := saveRunLoopState(repo, runLoopState{Enabled: true, SessionID: sess, ActiveRunID: sess, Runtime: "cursor"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const workers = 16
	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if _, _, err := mutateRunLoopState(repo, func(s runLoopState) runLoopState {
					s.NoProgressNudges++
					return s
				}); err != nil {
					t.Errorf("mutate: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	final, err := loadRunLoopState(repo)
	if err != nil {
		t.Fatalf("load final: %v", err)
	}
	if want := workers * iterations; final.NoProgressNudges != want {
		t.Fatalf("lost updates under concurrency: NoProgressNudges=%d want %d", final.NoProgressNudges, want)
	}
}
