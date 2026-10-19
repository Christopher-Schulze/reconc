package tasklifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/atomicfile"
)

func TestTransactionMoveRetainsSourceUntilDestinationIsDurable(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "rollback"}[rollback], func(t *testing.T) {
			root := t.TempDir()
			source, destination := "active/detail.md", "done/detail.md"
			for _, directory := range []string{"active", "done"} {
				if err := os.Mkdir(filepath.Join(root, directory), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			body := []byte("preserved task\n")
			initial := source
			if rollback {
				initial = destination
			}
			if err := os.WriteFile(filepath.Join(root, initial), body, 0o644); err != nil {
				t.Fatal(err)
			}
			move := transactionMove{Source: source, Destination: destination, BeforeHash: hashContent(body), AfterHash: hashContent(body)}
			file := transactionFile{Path: source, Before: body, BeforeMode: 0o644, BeforeHash: move.BeforeHash, AfterHash: move.AfterHash}
			previous := syncTransactionDirectory
			t.Cleanup(func() { syncTransactionDirectory = previous })
			failure := errors.New("directory sync failed")
			syncTransactionDirectory = func(*os.Root) error { return failure }
			var err error
			if rollback {
				err = rollbackTransactionMove(root, move, file)
			} else {
				err = publishTransactionMoveWithLease(root, move, map[string]transactionFile{source: file}, moveMutation{Source: source, Destination: destination}, nil)
			}
			if !errors.Is(err, failure) {
				t.Fatalf("sync failure was not propagated: %v", err)
			}
			left, leftErr := os.Stat(filepath.Join(root, source))
			right, rightErr := os.Stat(filepath.Join(root, destination))
			if leftErr != nil || rightErr != nil || !os.SameFile(left, right) {
				t.Fatalf("sync failure lost the original link: source=%v destination=%v", leftErr, rightErr)
			}
			syncTransactionDirectory = atomicfile.SyncDirectory
			if err := rollbackTransactionMove(root, move, file); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, source)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, destination)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovery did not remove the extra link: %v", err)
			}
		})
	}
}
