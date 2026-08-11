//go:build !windows

package actionledger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestStoreRejectsSymlinkAndSpecialFilesWithoutBlocking(t *testing.T) {
	tests := []struct {
		name   string
		create func(*testing.T, *ledgerStoreFixture)
		act    func(*ledgerStoreFixture) error
	}{
		{
			name: "symlink live",
			create: func(t *testing.T, fixture *ledgerStoreFixture) {
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, fixture.store.livePath); err != nil {
					t.Fatal(err)
				}
			},
			act: func(fixture *ledgerStoreFixture) error {
				_, err := fixture.store.Append(context.Background(), fixture.record(EventRequestAccepted))
				return err
			},
		},
		{
			name: "fifo live",
			create: func(t *testing.T, fixture *ledgerStoreFixture) {
				if err := syscall.Mkfifo(fixture.store.livePath, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			act: func(fixture *ledgerStoreFixture) error {
				_, err := fixture.store.Append(context.Background(), fixture.record(EventRequestAccepted))
				return err
			},
		},
		{
			name: "fifo journal",
			create: func(t *testing.T, fixture *ledgerStoreFixture) {
				if err := syscall.Mkfifo(fixture.store.layout.JournalPath, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			act: func(fixture *ledgerStoreFixture) error {
				return fixture.store.Recover(context.Background())
			},
		},
		{
			name: "device live",
			create: func(t *testing.T, fixture *ledgerStoreFixture) {
				err := unix.Mknod(fixture.store.livePath, unix.S_IFCHR|0o600, int(unix.Mkdev(1, 3)))
				if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) || errors.Is(err, unix.ENOTSUP) {
					t.Skipf("device creation is unavailable: %v", err)
				}
				if err != nil {
					t.Fatal(err)
				}
			},
			act: func(fixture *ledgerStoreFixture) error {
				_, err := fixture.store.Append(context.Background(), fixture.record(EventRequestAccepted))
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLedgerStoreFixture(t)
			test.create(t, fixture)
			result := make(chan error, 1)
			go func() { result <- test.act(fixture) }()
			select {
			case err := <-result:
				if err == nil {
					t.Fatalf("special path was accepted")
				}
			case <-time.After(time.Second):
				t.Fatalf("special path handling blocked")
			}
		})
	}
}

func TestStoreFailsClosedOnPrivateModeLoss(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	fixture.append(t, EventRequestAccepted)
	before, err := os.Stat(fixture.store.livePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fixture.store.livePath, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.Append(context.Background(), fixture.record(EventPreDecision))
	if err == nil {
		t.Fatalf("Append() accepted a public-mode ledger")
	}
	after, statErr := os.Stat(fixture.store.livePath)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if after.Size() != before.Size() || after.Mode().Perm() != 0o644 {
		t.Fatalf("failed append changed the ledger: before=%d after=%d mode=%o", before.Size(), after.Size(), after.Mode().Perm())
	}
}

func TestExistingStateRejectsUnsafeLoneLockWithoutRepair(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	if err := os.WriteFile(fixture.store.layout.LockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ExistingState(context.Background()); err == nil {
		t.Fatal("ExistingState() accepted a public-mode lone ledger lock")
	}
	info, err := os.Lstat(fixture.store.layout.LockPath)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("ExistingState() repaired the lock: %v, %v", info, err)
	}
}

func TestStoreFailsClosedOnActionDirectoryReplacement(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	original := fixture.store.directory
	moved := original + ".moved"
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	attacker := t.TempDir()
	if err := os.Symlink(attacker, original); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.store.Append(context.Background(), fixture.record(EventRequestAccepted))
	if err == nil {
		t.Fatalf("Append() accepted a replaced action directory")
	}
	entries, readErr := os.ReadDir(attacker)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("replaced directory received %d files", len(entries))
	}
}
