package agentsession

import (
	"sync"
	"testing"
)

// TestMutateRepositoryRunStateSerializesIncrements proves the lock serializes
// read-modify-write so concurrent mutators do not lose each other's updates.
// Each mutator increments NoProgressNudges; with a lost update the final count
// would be below the total number of mutations.
func TestMutateRepositoryRunStateSerializesIncrements(t *testing.T) {
	repo := t.TempDir()
	if err := saveRepositoryRunState(repo, repositoryRunState{Enabled: true}); err != nil {
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
				if _, _, err := mutateRepositoryRunState(repo, func(s repositoryRunState) repositoryRunState {
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

	final, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatalf("load final: %v", err)
	}
	if want := workers * iterations; final.NoProgressNudges != want {
		t.Fatalf("lost updates under concurrency: NoProgressNudges=%d want %d", final.NoProgressNudges, want)
	}
}
