package actionledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
)

const (
	ledgerHelperCall = "RECONC_LEDGER_HELPER_CALL"
	ledgerHelperGate = "RECONC_LEDGER_HELPER_GATE"
	ledgerHelperHome = "RECONC_LEDGER_HELPER_HOME"
	ledgerHelperRepo = "RECONC_LEDGER_HELPER_REPO"
)

type ledgerStoreFixture struct {
	store   *Store
	lease   *actionstate.IdentityKeyLease
	storage actionstate.PrivateProjectStorage
	home    string
}

func newLedgerStoreFixture(t testing.TB) *ledgerStoreFixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "reconc-home")
	if _, err := actionstate.CreateIdentityKey(home, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	lease, err := actionstate.AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Errorf("close identity-key lease: %v", err)
		}
	})
	stateStore, err := actionstate.OpenStore(actionstate.StoreOptions{
		Home: home, Repository: t.TempDir(), KeyLease: lease, OwnerID: "ledger-test-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := stateStore.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	return &ledgerStoreFixture{store: store, lease: lease, storage: storage, home: home}
}

func (f *ledgerStoreFixture) record(event EventType) Record {
	return f.bindRecord(testLedgerRecord(event))
}

func (f *ledgerStoreFixture) bindRecord(record Record) Record {
	record.Call.RepositoryIdentity = f.storage.RepositoryIdentity()
	record.Call.RequestIdentity = f.identity("request")
	record.Call.ServerFingerprint = f.identity("server")
	record.Call.RunIdentity = f.identity("run")
	record.Call.SessionIdentity = f.identity("session")
	record.Call.ContextIdentity = f.identity("context")
	for index := range record.SelectedFields {
		record.SelectedFields[index].PointerIdentity = f.identity("pointer")
		record.SelectedFields[index].ValueIdentity = f.identity("value")
	}
	if record.Budget != nil {
		record.Budget.ReservationIdentity = f.identity("reservation")
		record.Budget.StateVersion = f.identity("state")
	}
	if record.Dispatch != nil {
		record.Dispatch.ReservationIdentity = f.identity("reservation")
	}
	return record
}

func (f *ledgerStoreFixture) appendRecord(t testing.TB, record Record) Record {
	t.Helper()
	sealed, err := f.store.Append(context.Background(), f.bindRecord(record))
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func (f *ledgerStoreFixture) identity(label string) string {
	return f.lease.Key.Identity(actionstate.DomainLedger, []byte(label))
}

func (f *ledgerStoreFixture) append(t testing.TB, event EventType) Record {
	t.Helper()
	record, err := f.store.Append(context.Background(), f.record(event))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func writePrivateLedgerTestFile(t testing.TB, fixture *ledgerStoreFixture, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fixture.storage.SecureJSONLFile(path); err != nil {
		t.Fatal(err)
	}
}

func TestStoreAppendsVerifiesAndReopensCompleteLifecycle(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	records := successfulBudgetLifecycle()
	for _, record := range records {
		fixture.appendRecord(t, record)
	}
	report, err := fixture.store.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Integrity != StatusVerified || report.DetachedHead != HeadMatched ||
		report.RecordCount != uint64(len(records)) || !report.EventsComplete || !report.CallsComplete || report.DroppedHistory {
		t.Fatalf("Verify() = %#v", report)
	}
	reopened, err := OpenStore(fixture.storage)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reopened.Verify(context.Background())
	if err != nil || second != report {
		t.Fatalf("reopened Verify() = %#v, %v; want %#v", second, err, report)
	}
	for _, name := range []string{liveFileName, headFileName, lockFileName} {
		path := filepath.Join(fixture.store.directory, name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
		if err := fixture.storage.ValidateJSONLFile(path, MaxLiveBytes); err != nil {
			t.Fatalf("%s is not private: %v", name, err)
		}
	}
}

func TestStoreSerializesConcurrentWriters(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	const calls = 4
	stores := make([]*Store, calls)
	for index := range stores {
		var err error
		stores[index], err = OpenStore(fixture.storage)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	errs := make(chan error, calls)
	for index := range stores {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			callID := "act_" + strings.Repeat(string(rune('a'+index)), 26)
			request := fixture.record(EventRequestAccepted)
			request.Call.CallID = callID
			if _, err := stores[index].Append(context.Background(), request); err != nil {
				errs <- err
				return
			}
			decision := fixture.record(EventPreDecision)
			decision.Call.CallID = callID
			decision.Decision.Decision = action.DecisionBlock
			decision.Decision.Reason = action.ReasonRuleMatched
			decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
			if _, err := stores[index].Append(context.Background(), decision); err != nil {
				errs <- err
			}
		}(index)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	report, err := fixture.store.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.RecordCount != calls*2 || !report.CallsComplete {
		t.Fatalf("concurrent Verify() = %#v", report)
	}
}

func TestExistingStateSerializesWithLedgerWriter(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	fixture.append(t, EventRequestAccepted)
	acquired := make(chan struct{})
	release := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- boundedio.WithRegularFileSnapshot(
			fixture.store.layout.LockPath, 1,
			func(file *os.File, _ os.FileInfo) error {
				unlock, err := filelock.Lock(file)
				if err != nil {
					return err
				}
				close(acquired)
				<-release
				return unlock()
			},
		)
	}()
	<-acquired
	queryContext, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := fixture.store.ExistingState(queryContext); err == nil ||
		!strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("ExistingState() did not serialize with the writer: %v", err)
	}
	close(release)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	exists, err := fixture.store.ExistingState(context.Background())
	if err != nil || !exists {
		t.Fatalf("ExistingState() after writer release = %v, %v", exists, err)
	}
}

func TestStoreSerializesMultipleProcesses(t *testing.T) {
	home := filepath.Join(t.TempDir(), "reconc-home")
	repository := t.TempDir()
	gate := filepath.Join(t.TempDir(), "start")
	if _, err := actionstate.CreateIdentityKey(home, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 4)
	outputs := make([]bytes.Buffer, len(commands))
	for index := range commands {
		callID := "act_" + strings.Repeat(string(rune('a'+index)), 26)
		commands[index] = exec.Command(os.Args[0], "-test.run=^TestStoreMultiprocessHelper$")
		commands[index].Env = append(os.Environ(),
			ledgerHelperCall+"="+callID,
			ledgerHelperGate+"="+gate,
			ledgerHelperHome+"="+home,
			ledgerHelperRepo+"="+repository,
		)
		commands[index].Stdout = &outputs[index]
		commands[index].Stderr = &outputs[index]
		if err := commands[index].Start(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gate, []byte("start\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("ledger helper: %v: %s", err, outputs[index].Bytes())
		}
	}
	lease, err := actionstate.AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	stateStore, err := actionstate.OpenStore(actionstate.StoreOptions{
		Home: home, Repository: repository, KeyLease: lease, OwnerID: "ledger-parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := stateStore.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	report, err := store.Verify(context.Background())
	if err != nil || report.RecordCount != 8 || !report.CallsComplete {
		t.Fatalf("multiprocess Verify() = %#v, %v", report, err)
	}
}

func TestStoreMultiprocessHelper(t *testing.T) {
	callID := os.Getenv(ledgerHelperCall)
	if callID == "" {
		return
	}
	gate := os.Getenv(ledgerHelperGate)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("ledger helper gate timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
	lease, err := actionstate.AcquireIdentityKey(context.Background(), os.Getenv(ledgerHelperHome))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	stateStore, err := actionstate.OpenStore(actionstate.StoreOptions{
		Home: os.Getenv(ledgerHelperHome), Repository: os.Getenv(ledgerHelperRepo),
		KeyLease: lease, OwnerID: "owner-" + strings.TrimPrefix(callID, "act_"),
	})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := stateStore.PrivateProjectStorage()
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &ledgerStoreFixture{store: store, lease: lease, storage: storage}
	request := fixture.record(EventRequestAccepted)
	request.Call.CallID = callID
	if _, err := store.Append(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	decision := fixture.record(EventPreDecision)
	decision.Call.CallID = callID
	decision.Decision.Decision = action.DecisionBlock
	decision.Decision.Reason = action.ReasonRuleMatched
	decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
	if _, err := store.Append(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
}

func TestStoreReportsDroppedHistoryAfterBoundedRotation(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	fixture.store.policy.MaxBytes = 8 << 10
	for index := 0; index < 24; index++ {
		character := string(rune('a' + index))
		callID := "act_" + strings.Repeat(character, 26)
		request := fixture.record(EventRequestAccepted)
		request.Call.CallID = callID
		if _, err := fixture.store.Append(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		decision := fixture.record(EventPreDecision)
		decision.Call.CallID = callID
		decision.Decision.Decision = action.DecisionBlock
		decision.Decision.Reason = action.ReasonRuleMatched
		decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
		if _, err := fixture.store.Append(context.Background(), decision); err != nil {
			t.Fatal(err)
		}
	}
	report, err := fixture.store.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ArchiveCount != MaxArchives || !report.DroppedHistory || report.DroppedBeforeSequence <= 1 ||
		report.FirstRecordedSequence != 1 || report.Integrity != StatusVerified {
		t.Fatalf("rotated Verify() = %#v", report)
	}
}

func TestStoreNeverPrunesAnActiveCallDuringRotation(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	fixture.store.policy.MaxBytes = 8 << 10
	fixture.append(t, EventRequestAccepted)
	fixture.append(t, EventPreDecision)
	dispatch := fixture.record(EventDownstreamDispatch)
	dispatch.Dispatch.ReservationIdentity = "absent"
	if _, err := fixture.store.Append(context.Background(), dispatch); err != nil {
		t.Fatal(err)
	}

	var blocked error
	for index := uint64(1); index < 100; index++ {
		progress := fixture.record(EventPreDecision)
		progress.Decision.Phase = action.PhaseProgress
		progress.PreDecision.Outcome = action.OutcomeProgressEligible
		progress.SelectedFields = []SelectedFieldEvidence{}
		progress.LatencyMicros = index
		if _, err := fixture.store.Append(context.Background(), progress); err != nil {
			blocked = err
			break
		}
	}
	if blocked == nil || ErrorCode(blocked) != action.ReasonLedgerUnavailable ||
		!strings.Contains(blocked.Error(), "would prune active call") {
		t.Fatalf("active-call retention error = %v", blocked)
	}
	report, err := fixture.store.Verify(context.Background())
	if err != nil || report.Integrity != StatusVerified || report.DroppedHistory ||
		report.ArchiveCount != MaxArchives || report.CallsComplete {
		t.Fatalf("protected active ledger = %#v, %v", report, err)
	}

	failure := fixture.record(EventTerminalFailure)
	if _, err := fixture.store.Append(context.Background(), failure); err != nil {
		t.Fatalf("terminal event could not release active-call retention protection: %v", err)
	}
	report, err = fixture.store.Verify(context.Background())
	if err != nil || report.Integrity != StatusVerified || !report.DroppedHistory {
		t.Fatalf("terminalized rotated ledger = %#v, %v", report, err)
	}
}

func TestStoreDetectsTamperTruncationReorderDuplicateArchiveGapAndMissingHead(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *ledgerStoreFixture)
	}{
		{
			name: "tamper",
			mutate: func(t *testing.T, fixture *ledgerStoreFixture) {
				body, err := os.ReadFile(fixture.store.livePath)
				if err != nil {
					t.Fatal(err)
				}
				body = []byte(strings.Replace(string(body), "release-operator", "release-operat0r", 1))
				if err := os.WriteFile(fixture.store.livePath, body, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "partial tail",
			mutate: func(t *testing.T, fixture *ledgerStoreFixture) {
				info, err := os.Stat(fixture.store.livePath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Truncate(fixture.store.livePath, info.Size()-1); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "reordered records",
			mutate: func(t *testing.T, fixture *ledgerStoreFixture) {
				body, err := os.ReadFile(fixture.store.livePath)
				if err != nil {
					t.Fatal(err)
				}
				lines := bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n"))
				if len(lines) != 2 {
					t.Fatalf("ledger lines = %d, want 2", len(lines))
				}
				mutated := append(append(append([]byte{}, lines[1]...), '\n'), lines[0]...)
				mutated = append(mutated, '\n')
				if err := os.WriteFile(fixture.store.livePath, mutated, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "duplicated record",
			mutate: func(t *testing.T, fixture *ledgerStoreFixture) {
				body, err := os.ReadFile(fixture.store.livePath)
				if err != nil {
					t.Fatal(err)
				}
				firstEnd := bytes.IndexByte(body, '\n')
				if firstEnd < 0 {
					t.Fatal("ledger contains no complete record")
				}
				body = append(body, body[:firstEnd+1]...)
				if err := os.WriteFile(fixture.store.livePath, body, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "archive gap",
			mutate: func(t *testing.T, fixture *ledgerStoreFixture) {
				body, err := boundedio.ReadRegularFile(fixture.store.livePath, MaxLiveBytes)
				if err != nil {
					t.Fatal(err)
				}
				writePrivateLedgerTestFile(t, fixture, fixture.store.livePath+".2", body)
			},
		},
		{
			name: "missing head",
			mutate: func(t *testing.T, fixture *ledgerStoreFixture) {
				if err := os.Remove(fixture.store.headPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "head timestamp drift",
			mutate: func(t *testing.T, fixture *ledgerStoreFixture) {
				body, err := fixture.storage.ReadPrivateFile(headFileName, maxHeadBytes)
				if err != nil {
					t.Fatal(err)
				}
				head, err := decodeChainHead(body)
				if err != nil {
					t.Fatal(err)
				}
				head.UpdatedAt = "2026-08-11T12:00:01Z"
				body, err = encodeChainHead(head)
				if err != nil {
					t.Fatal(err)
				}
				if err := fixture.store.publishHead(headFileName, body); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLedgerStoreFixture(t)
			fixture.append(t, EventRequestAccepted)
			decision := fixture.record(EventPreDecision)
			decision.Decision.Decision = action.DecisionBlock
			decision.Decision.Reason = action.ReasonRuleMatched
			decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
			if _, err := fixture.store.Append(context.Background(), decision); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, fixture)
			report, err := fixture.store.Verify(context.Background())
			if err == nil || report.Integrity != StatusInvalid {
				t.Fatalf("Verify() = %#v, %v; want invalid", report, err)
			}
		})
	}
}

func TestVerificationNeverClaimsCompletenessBeforeEvaluation(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	fixture.append(t, EventRequestAccepted)
	if err := os.WriteFile(fixture.store.livePath, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := fixture.store.Verify(context.Background())
	if err == nil || report.Integrity != StatusInvalid || report.EventsEvaluated ||
		report.EventsComplete || report.IncompleteEvents != 0 || report.CallsEvaluated ||
		report.CallsComplete || report.IncompleteCalls != 0 {
		t.Fatalf("invalid verification made an unevaluated completeness claim: %#v, %v", report, err)
	}
}

func TestStoreVerificationRejectsForeignIdentityKeyGeneration(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	record := fixture.record(EventRequestAccepted)
	record.Call.RequestIdentity = testKeyedIdentity("f")
	sealed, body, err := Seal(record, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	writePrivateLedgerTestFile(t, fixture, fixture.store.layout.LockPath, nil)
	writePrivateLedgerTestFile(t, fixture, fixture.store.livePath, append(body, '\n'))
	head, err := encodeChainHead(newChainHead(sealed))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.publishHead(headFileName, head); err != nil {
		t.Fatal(err)
	}
	report, err := fixture.store.Verify(context.Background())
	if err == nil || report.Integrity != StatusInvalid ||
		!strings.Contains(err.Error(), "identity generation drifted") {
		t.Fatalf("Verify() = %#v, %v; want foreign key generation failure", report, err)
	}
}

func TestStoreAllowsConcurrentCallTimestampsToInterleave(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	first := fixture.record(EventRequestAccepted)
	first.Timestamp = "2026-08-11T12:00:01Z"
	if _, err := fixture.store.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := fixture.record(EventRequestAccepted)
	second.Call.CallID = "act_" + strings.Repeat("b", 26)
	second.Timestamp = "2026-08-11T12:00:00Z"
	if _, err := fixture.store.Append(context.Background(), second); err != nil {
		t.Fatalf("concurrent call with an earlier event time was rejected: %v", err)
	}
	report, err := fixture.store.Verify(context.Background())
	if err != nil || report.Integrity != StatusVerified || report.RecordCount != 2 {
		t.Fatalf("Verify() = %#v, %v", report, err)
	}
}

func TestStoreRejectsInvalidLifecycleBeforeMutation(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	fixture.append(t, EventRequestAccepted)
	if _, err := fixture.store.Append(context.Background(), fixture.record(EventDownstreamOutcome)); err == nil {
		t.Fatal("Append() accepted a downstream outcome before evaluation and dispatch")
	}
	report, err := fixture.store.Verify(context.Background())
	if err != nil || report.Integrity != StatusVerified || report.RecordCount != 1 {
		t.Fatalf("rejected append changed ledger: %#v, %v", report, err)
	}
}

func TestSelectedFieldUsesPolicyBoundKeyedCanonicalIdentity(t *testing.T) {
	fixture := newLedgerStoreFixture(t)
	value, err := action.ParseJSON([]byte(`{"z":1.0,"a":"secret-value"}`))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := fixture.store.SelectedField(SelectedFieldInput{
		PolicyDigest: strings.Repeat("1", 64), LockDigest: strings.Repeat("2", 64),
		ToolContractDigest: "sha256:" + strings.Repeat("3", 64),
		Source:             action.SourceArguments, Pointer: "/payload",
		Selected: action.PointerResult{State: action.PointerPresent, Value: value},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	plain := sha256.Sum256(canonical)
	if !evidence.Complete || !action.ValidKeyedIdentity(evidence.PointerIdentity) ||
		!action.ValidKeyedIdentity(evidence.ValueIdentity) || evidence.ByteLength != uint64(len(canonical)) ||
		evidence.ItemCount != 2 || evidence.ValueIdentity == hex.EncodeToString(plain[:]) {
		t.Fatalf("SelectedField() = %#v", evidence)
	}
	if strings.Contains(evidence.ValueIdentity, "secret-value") {
		t.Fatalf("selected field identity disclosed the raw value")
	}
}

func TestRecordingModeControlsPreDispatchFailure(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		fixture := newLedgerStoreFixture(t)
		result, err := fixture.store.Record(context.Background(), action.LedgerOff, fixture.record(EventPreDecision))
		if err != nil || result.Status != RecordingSkipped || !result.Proceed || result.EvidenceComplete {
			t.Fatalf("Record(off) = %#v, %v", result, err)
		}
		if _, err := os.Lstat(fixture.store.livePath); !os.IsNotExist(err) {
			t.Fatalf("off mode created a ledger: %v", err)
		}
	})
	t.Run("best effort and required", func(t *testing.T) {
		fixture := newLedgerStoreFixture(t)
		fixture.append(t, EventRequestAccepted)
		makeLedgerFileUnsafe(t, fixture.store.livePath)
		record := fixture.record(EventPreDecision)
		best, err := fixture.store.Record(context.Background(), action.LedgerBestEffort, record)
		if err != nil || best.Status != RecordingFailed || !best.Proceed || best.EvidenceComplete ||
			best.Reason != action.ReasonLedgerUnavailable {
			t.Fatalf("Record(best_effort) = %#v, %v", best, err)
		}
		required, err := fixture.store.Record(context.Background(), action.LedgerRequired, record)
		if err == nil || required.Status != RecordingFailed || required.Proceed || required.EvidenceComplete ||
			required.Reason != action.ReasonLedgerUnavailable {
			t.Fatalf("Record(required) = %#v, %v", required, err)
		}
	})
}

func TestStoreRejectsUnsafeLockModeWithoutMutatingLedger(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission contract")
	}
	fixture := newLedgerStoreFixture(t)
	fixture.append(t, EventRequestAccepted)
	before, err := os.ReadFile(fixture.store.livePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fixture.store.layout.LockPath, 0o644); err != nil {
		t.Fatal(err)
	}
	decision := fixture.record(EventPreDecision)
	decision.Decision.Decision = action.DecisionBlock
	decision.Decision.Reason = action.ReasonRuleMatched
	decision.PreDecision.Outcome = action.OutcomeDispatchBlocked
	if _, err := fixture.store.Append(context.Background(), decision); err == nil ||
		ErrorCode(err) != action.ReasonLedgerCorrupt {
		t.Fatalf("Append() error = %v", err)
	}
	after, err := os.ReadFile(fixture.store.livePath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("rejected append changed ledger: err=%v", err)
	}
	info, err := os.Lstat(fixture.store.layout.LockPath)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("rejected append repaired unsafe lock mode: %v, %v", info, err)
	}
}
