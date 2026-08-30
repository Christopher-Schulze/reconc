package actionledger

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestHeadPublicationFailureLeavesRecoverableTransaction(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	realPublisher := fixture.store.publishHead
	fixture.store.publishHead = func(string, []byte) error { return syscall.ENOSPC }
	_, err := fixture.store.Append(context.Background(), fixture.record(EventRequestAccepted))
	if err == nil || ErrorCode(err) != action.ReasonLedgerUnavailable || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Append() error = %v", err)
	}
	for _, path := range []string{fixture.store.livePath, fixture.store.layout.JournalPath} {
		if _, statErr := os.Lstat(path); statErr != nil {
			t.Fatalf("durable recovery input %s is missing: %v", path, statErr)
		}
	}
	fixture.store.publishHead = realPublisher
	if err := fixture.store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(fixture.store.layout.JournalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered transaction journal remains: %v", err)
	}
	report, err := fixture.store.Verify(context.Background())
	if err != nil || report.Integrity != StatusVerified || report.RecordCount != 1 ||
		report.DetachedHead != HeadMatched {
		t.Fatalf("Verify() = %#v, %v", report, err)
	}
}

func TestRetryAfterHeadPublicationFailureDoesNotDuplicateRecord(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	record := fixture.record(EventRequestAccepted)
	realPublisher := fixture.store.publishHead
	fixture.store.publishHead = func(string, []byte) error { return syscall.ENOSPC }
	if _, err := fixture.store.Append(context.Background(), record); err == nil {
		t.Fatal("Append() unexpectedly survived the injected head failure")
	}
	fixture.store.publishHead = realPublisher
	sealed, err := fixture.store.Append(context.Background(), record)
	if err != nil || sealed.Sequence != 1 {
		t.Fatalf("retry Append() = sequence %d, %v", sealed.Sequence, err)
	}
	report, err := fixture.store.Verify(context.Background())
	if err != nil || report.RecordCount != 1 || report.Integrity != StatusVerified {
		t.Fatalf("retry Verify() = %#v, %v", report, err)
	}
}

func TestRecoverSerializesCheckpointPublicationWithAppend(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	realPublisher := fixture.store.publishHead
	fixture.store.publishHead = func(string, []byte) error { return syscall.ENOSPC }
	if _, err := fixture.store.Append(
		context.Background(), fixture.record(EventRequestAccepted),
	); err == nil {
		t.Fatal("Append() unexpectedly survived the injected head failure")
	}

	recoveryPublishedHead := make(chan struct{})
	releaseRecovery := make(chan struct{})
	var blockFirst sync.Once
	fixture.store.publishHead = func(name string, body []byte) error {
		if err := realPublisher(name, body); err != nil {
			return err
		}
		blockFirst.Do(func() {
			close(recoveryPublishedHead)
			<-releaseRecovery
		})
		return nil
	}
	recoverDone := make(chan error, 1)
	go func() {
		recoverDone <- fixture.store.Recover(context.Background())
	}()
	<-recoveryPublishedHead
	if fixture.store.appendMu.TryLock() {
		fixture.store.appendMu.Unlock()
		close(releaseRecovery)
		<-recoverDone
		t.Fatal("Recover() published the checkpoint cache outside the append synchronization boundary")
	}

	appendAtBoundary := make(chan struct{})
	fixture.store.checkpointHooks.beforeAppendLock = func() { close(appendAtBoundary) }
	appendDone := make(chan error, 1)
	go func() {
		_, err := fixture.store.Append(context.Background(), fixture.record(EventPreDecision))
		appendDone <- err
	}()
	<-appendAtBoundary
	close(releaseRecovery)
	if err := <-recoverDone; err != nil {
		t.Fatal(err)
	}
	if err := <-appendDone; err != nil {
		t.Fatal(err)
	}
	report, err := fixture.store.Verify(context.Background())
	if err != nil || report.Integrity != StatusVerified || report.RecordCount != 2 ||
		report.DetachedHead != HeadMatched {
		t.Fatalf("concurrent recovery and append verification = %#v, %v", report, err)
	}
}

func TestMissingHeadPublisherFailsClosedBeforeFalseSuccess(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	fixture.store.publishHead = nil
	result, err := fixture.store.Record(
		context.Background(), action.LedgerRequired, fixture.record(EventRequestAccepted),
	)
	if err == nil || result.Proceed || result.Status != RecordingFailed ||
		result.Reason != action.ReasonLedgerUnavailable {
		t.Fatalf("Record(required) = %#v, %v", result, err)
	}
}
