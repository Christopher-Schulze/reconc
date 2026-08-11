//go:build !windows

package actionstate

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

type fileInfoWithSystem struct {
	os.FileInfo
	system any
}

func (i fileInfoWithSystem) Sys() any {
	return i.system
}

func TestPrivateStateRequiresCurrentUserOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		t.Fatal("test filesystem owner identity is unavailable")
	}
	other := *stat
	other.Uid++
	if err := validateCurrentUserOwner(fileInfoWithSystem{FileInfo: info, system: &other}); err == nil {
		t.Fatal("private state owned by another user was accepted")
	}
}

func TestPrivateStateRejectsInsecureMode(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"private", action.BudgetLimits{CallCount: 1}, action.BudgetResetNever,
	)})
	fixture.reserve(t, callID("w"))
	if err := os.Chmod(fixture.store.statePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CurrentStateVersion(context.Background()); err == nil {
		t.Fatal("world-readable action state was accepted")
	} else {
		requireStateCode(t, err, action.ReasonStateCorrupt)
	}
}

func TestBudgetStoreFailsClosedWhenStateDirectoryLosesWritePermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write-permission checks")
	}
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"permission", action.BudgetLimits{CallCount: 1}, action.BudgetResetNever,
	)})
	version, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fixture.store.directory, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(fixture.store.directory, 0o700); err != nil {
			t.Errorf("restore action-state directory mode: %v", err)
		}
	}()
	request := fixture.request
	request.CallID, request.StateVersion = callID("7"), version
	_, err = fixture.store.Reserve(context.Background(), ReserveRequest{
		Plan: fixture.plan, Request: request, Context: fixture.context,
		Authority: fixture.authority, Server: fixture.server,
	})
	if err == nil {
		t.Fatal("permission loss allowed a budget reservation")
	}
	requireStateCode(t, err, action.ReasonStateUnavailable)
}

func TestBudgetStoreRecoversExactJournalAfterDiskFullStatePublication(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"disk-full", action.BudgetLimits{CallCount: 1}, action.BudgetResetNever,
	)})
	version, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.request
	request.CallID, request.StateVersion = callID("b"), version
	input := ReserveRequest{
		Plan: fixture.plan, Request: request, Context: fixture.context,
		Authority: fixture.authority, Server: fixture.server,
	}
	publish := fixture.store.publish
	fixture.store.publish = func(path string, body []byte) error {
		if path == fixture.store.statePath {
			return syscall.ENOSPC
		}
		return publish(path, body)
	}
	if _, err := fixture.store.Reserve(context.Background(), input); err == nil {
		t.Fatal("disk-full state publication reported a reservation")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
	if _, err := os.Lstat(fixture.store.transactionPath); err != nil {
		t.Fatalf("committed recovery journal missing after disk-full failure: %v", err)
	}

	fixture.store.publish = publish
	recoveredVersion, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	input.Request.StateVersion = recoveredVersion
	recovered, err := fixture.store.Reserve(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Reservation == nil || recovered.Reservation.CallID != request.CallID ||
		recovered.Snapshot.StateVersion != recoveredVersion {
		t.Fatalf("recovered reservation = %#v", recovered)
	}
}

func TestPrivateStateRejectsSymlinkAndFIFOWithoutBlocking(t *testing.T) {
	tests := []struct {
		name  string
		write func(testing.TB, string)
	}{
		{
			name: "symlink",
			write: func(t testing.TB, target string) {
				t.Helper()
				other := filepath.Join(t.TempDir(), "state.json")
				if err := os.WriteFile(other, []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, target); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
		{
			name: "fifo",
			write: func(t testing.TB, target string) {
				t.Helper()
				if err := syscall.Mkfifo(target, 0o600); err != nil {
					t.Skipf("FIFO unavailable: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoreFixture(t, nil)
			test.write(t, fixture.store.statePath)
			started := time.Now()
			if _, err := fixture.store.CurrentStateVersion(context.Background()); err == nil {
				t.Fatal("non-regular state was accepted")
			} else {
				requireStateCode(t, err, action.ReasonStateCorrupt)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("special-file rejection blocked for %s", elapsed)
			}
		})
	}
}

func TestPrivateTransactionRejectsSymlink(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	other := filepath.Join(t.TempDir(), "transaction.json")
	if err := os.WriteFile(other, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, fixture.store.transactionPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := fixture.store.CurrentStateVersion(context.Background()); err == nil {
		t.Fatal("symlinked transaction was accepted")
	} else {
		requireStateCode(t, err, action.ReasonStateCorrupt)
	}
}

func TestStoreRejectsSymlinkedPrivateDirectoryTree(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	moved := fixture.store.directory + ".moved"
	if err := os.Rename(fixture.store.directory, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), fixture.store.directory); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := fixture.store.CurrentStateVersion(context.Background()); err == nil {
		t.Fatal("symlinked private action-state directory was accepted")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
}
