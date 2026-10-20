package tasklifecycle

import (
	"errors"
)

func withMutationLock(repoRoot string, fn func() error) (resultErr error) {
	if fn == nil {
		return errors.New("TASK mutation lock callback is required")
	}
	return withMutationLockLease(repoRoot, func(*taskMutationLockLease) error {
		return fn()
	})
}

func applyTransaction(repoRoot, action string, files []fileMutation, moves []moveMutation) error {
	return applyTransactionWithLease(repoRoot, action, files, moves, nil)
}

func publishTransaction(repoRoot string, journal transaction, files []fileMutation, moves []moveMutation) error {
	return publishTransactionWithLease(repoRoot, journal, files, moves, journal.lockLease)
}

func prepareTransactionDirectories(repoRoot string, directories []transactionDirectory) error {
	return prepareTransactionDirectoriesWithLease(repoRoot, directories, nil)
}

func rollbackTransaction(repoRoot string, journal transaction) error {
	return rollbackTransactionWithLease(repoRoot, journal, journal.lockLease)
}

func rollbackTransactionMove(repoRoot string, move transactionMove, sourceFile transactionFile) error {
	return rollbackTransactionMoveWithLease(repoRoot, move, sourceFile, nil)
}
