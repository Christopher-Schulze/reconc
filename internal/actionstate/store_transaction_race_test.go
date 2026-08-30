package actionstate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/privatefs"
)

func prepareRecoveryTransaction(t *testing.T, fixture *storeFixture, persistAfter bool) []byte {
	t.Helper()
	previous, err := fixture.store.initialState()
	if err != nil {
		t.Fatal(err)
	}
	next := cloneState(previous)
	fixture.store.applyClock(&next, fixture.clock.snapshot)
	next.Revision = 1
	next.Digest, err = fixture.store.stateDigest(next)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := fixture.store.newTransaction(previous, false, next)
	if err != nil {
		t.Fatal(err)
	}
	if persistAfter {
		if err := writeBoundedJSON(fixture.store.statePath, next, MaxStateBytes); err != nil {
			t.Fatal(err)
		}
	}
	body, err := encodeBoundedCompactJSON(transaction, MaxStateTransaction)
	if err != nil {
		t.Fatal(err)
	}
	writePrivateTestFile(t, fixture.store.transactionPath, body)
	return body
}

func replaceRecoveryTransaction(t *testing.T, path string, body []byte) error {
	t.Helper()
	moved := path + ".original"
	if err := os.Rename(path, moved); err != nil {
		return err
	}
	t.Cleanup(func() { _ = os.Remove(moved) })
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := errors.Join(privatefs.SecureFile(file), file.Close()); err != nil {
		return err
	}
	return validatePrivateRegularFile(path, int64(len(body)+1))
}

func replaceRecoveryTransactionWithSymlink(t *testing.T, path string) (bool, error) {
	t.Helper()
	moved := path + ".original"
	if err := os.Rename(path, moved); err != nil {
		return false, err
	}
	t.Cleanup(func() { _ = os.Remove(moved) })
	foreign := filepath.Join(t.TempDir(), "foreign-transaction.json")
	if err := os.WriteFile(foreign, []byte(`{"replacement":true}`), 0o600); err != nil {
		return false, err
	}
	if err := os.Symlink(foreign, path); err != nil {
		return true, err
	}
	return false, nil
}

func TestRecoverTransactionPreservesReplacementAfterRead(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"recovery-replacement", action.BudgetLimits{CallCount: 1}, action.BudgetResetNever,
	)})
	replacement := []byte(`{"replacement":"after-read"}`)
	prepareRecoveryTransaction(t, fixture, false)
	replaced := false
	err := fixture.store.recoverTransactionWithHooks(transactionRecoveryHooks{
		afterRead: func() error {
			replaced = true
			return replaceRecoveryTransaction(t, fixture.store.transactionPath, replacement)
		},
	})
	if !replaced {
		t.Fatal("recovery read hook was not called")
	}
	if err == nil {
		t.Fatal("recovery removed a replacement transaction")
	}
	body, readErr := os.ReadFile(fixture.store.transactionPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(body, replacement) {
		t.Fatalf("replacement transaction = %q, want %q", body, replacement)
	}
}

func TestRecoverTransactionPreservesReplacementBeforeRemoval(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	replacement := []byte(`{"replacement":"before-remove"}`)
	prepareRecoveryTransaction(t, fixture, true)
	replaced := false
	err := fixture.store.recoverTransactionWithHooks(transactionRecoveryHooks{
		beforeRemove: func() error {
			replaced = true
			return replaceRecoveryTransaction(t, fixture.store.transactionPath, replacement)
		},
	})
	if !replaced {
		t.Fatal("recovery removal hook was not called")
	}
	if err == nil {
		t.Fatal("recovery removed a replacement transaction")
	}
	body, readErr := os.ReadFile(fixture.store.transactionPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(body, replacement) {
		t.Fatalf("replacement transaction = %q, want %q", body, replacement)
	}
}

func TestRecoverTransactionPreservesSymlinkBeforeRemoval(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	prepareRecoveryTransaction(t, fixture, true)
	symlinkUnavailable := false
	err := fixture.store.recoverTransactionWithHooks(transactionRecoveryHooks{
		beforeRemove: func() error {
			var linkErr error
			symlinkUnavailable, linkErr = replaceRecoveryTransactionWithSymlink(t, fixture.store.transactionPath)
			return linkErr
		},
	})
	if symlinkUnavailable {
		t.Skip("symlink substitution unavailable")
	}
	if err == nil {
		t.Fatal("recovery removed a symlink replacement")
	}
	if _, err := os.Lstat(fixture.store.transactionPath); err != nil {
		t.Fatal(err)
	}
}
