package actionstate

import (
	"context"
	"errors"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

func TestHealthyActionStateReadersShareLockAndBoundWaitingWriter(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	fixture.store.lockTimeout = 30 * time.Millisecond
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- fixture.store.withReadLock(context.Background(), func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first healthy reader did not acquire the shared lock")
	}

	secondEntered := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		secondResult <- fixture.store.withReadLock(context.Background(), func() error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		close(releaseFirst)
		t.Fatal("second healthy reader serialized behind the first reader")
	}
	if err := <-secondResult; err != nil {
		close(releaseFirst)
		t.Fatal(err)
	}

	if err := fixture.store.withLock(context.Background(), func() error { return nil }); err == nil {
		close(releaseFirst)
		t.Fatal("writer entered while a healthy reader held the shared lock")
	} else {
		requireStateCode(t, err, action.ReasonStateUnavailable)
	}
	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.withLock(context.Background(), func() error { return nil }); err != nil {
		t.Fatalf("writer remained starved after readers released: %v", err)
	}
}

func TestActionStateReadCancellationAndRecoveryEscalation(t *testing.T) {
	fixture := newStoreFixture(t, nil)
	fixture.store.lockTimeout = 100 * time.Millisecond
	held, err := acquireFileLock(context.Background(), fixture.store.lockPath, StateLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.store.CurrentStateVersion(canceled); !errors.Is(err, context.Canceled) {
		_ = held.close()
		t.Fatalf("canceled healthy read = %v", err)
	}
	if err := held.close(); err != nil {
		t.Fatal(err)
	}

	prepareRecoveryTransaction(t, fixture, false)
	version, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, persisted, err := fixture.store.loadStateWithoutRecovery()
	if err != nil {
		t.Fatal(err)
	}
	if !persisted || version != state.Digest || state.Revision != 1 {
		t.Fatalf("recovered read = version %q, state %#v, persisted %t", version, state, persisted)
	}
}
