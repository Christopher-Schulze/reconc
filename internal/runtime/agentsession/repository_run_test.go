package agentsession

import (
	"os"
)

func repositoryRunStatePath(repoRoot string) (string, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return "", err
	}
	return repositoryRunStatePathResolved(root), nil
}

func appendRunDecision(repoRoot string, decision RunDecision) error {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return appendRunDecisionResolved(root, decision)
}

func loadRepositoryRunState(repoRoot string) (repositoryRunState, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return repositoryRunState{}, err
	}
	return loadRepositoryRunStateResolved(root)
}

// saveRepositoryRunState persists state while holding the repository run state
// lock. Use this for standalone/terminal writes; use
// mutateRepositoryRunStateResolved
// for read-modify-write so the load and save share a single lock.
func saveRepositoryRunState(repoRoot string, state repositoryRunState) error {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return withRepositoryRunFileResolved(root, func(file *os.File) error {
		snapshot, readErr := readRepositoryRunSnapshotFile(file)
		if readErr != nil {
			return wrapRepositoryRunRecovery(root, readErr)
		}
		if identityErr := validateRepositoryRunIdentity(root, snapshot); identityErr != nil {
			return wrapRepositoryRunRecovery(root, identityErr)
		}
		state.RootIdentity = repositoryRunRootIdentity(root)
		return commitRepositoryRunSnapshotFile(file, state, snapshot)
	})
}

// mutateRepositoryRunStateResolved serializes load -> mutate -> save under one lock so
// concurrent agent tool events cannot lose each other's updates. Mirror of
// MutateSessionState. Corrupt or unreadable state fails closed and is never
// replaced with a zero value; CRC-protected alternating slots recover from a
// torn newest write without accepting corrupt state.
func mutateRepositoryRunState(repoRoot string, fn func(repositoryRunState) repositoryRunState) (repositoryRunState, repositoryRunState, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return repositoryRunState{}, repositoryRunState{}, err
	}
	return mutateRepositoryRunStateResolved(root, fn)
}
