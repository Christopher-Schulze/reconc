package jsonl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type recordingLayoutSecurity struct {
	identity        string
	directory       string
	rejectPath      string
	secured         map[string]bool
	validated       map[string]int
	directoryChecks int
}

func (s *recordingLayoutSecurity) JSONLSecurityIdentity() string {
	return s.identity
}

func (s *recordingLayoutSecurity) ValidateJSONLDirectory(path string) error {
	if path != s.directory {
		return errors.New("unexpected directory")
	}
	s.directoryChecks++
	return nil
}

func (s *recordingLayoutSecurity) SecureJSONLFile(path string) error {
	s.secured[path] = true
	return nil
}

func (s *recordingLayoutSecurity) ValidateJSONLFile(path string, _ int64) error {
	if s.validated == nil {
		s.validated = make(map[string]int)
	}
	s.validated[path]++
	if path == s.rejectPath {
		return errors.New("rejected existing file")
	}
	return nil
}

func TestLayoutSecurityCoversRotationArchivesAndRecoveryBackups(t *testing.T) {
	directory := privateTestDirectory(t)
	path := filepath.Join(directory, "ledger.jsonl")
	security := &recordingLayoutSecurity{
		identity: "test-private-v1", directory: directory, secured: make(map[string]bool),
	}
	layout := privateTestLayout(path)
	layout.Security = security
	policy := Policy{MaxBytes: 48, MaxArchives: 2}
	for _, record := range [][]byte{
		[]byte(`{"record":1}`),
		[]byte(`{"padding":"123456789012345678901234567890"}`),
		[]byte(`{"record":3}`),
	} {
		body := append([]byte(nil), record...)
		if err := AppendTransactionWithLayout(path, policy, layout, func() ([]byte, error) {
			return body, nil
		}, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	for _, candidate := range []string{
		path + ".1",
		appendBackupPathWithLayout(layout, 0), appendBackupPathWithLayout(layout, 1),
	} {
		if security.validated[candidate] == 0 {
			t.Fatalf("rotated or recovery JSONL path was not security-validated: %s", candidate)
		}
	}
}

func legacyLayoutIdentityForTest(path string, layout Layout) string {
	hash := sha256.New()
	for _, value := range []string{
		filepath.Clean(path), layout.LockPath, layout.JournalPath, layout.BackupPrefix,
		fmt.Sprintf("%04o", layout.DirectoryMode.Perm()), fmt.Sprintf("%04o", layout.FileMode.Perm()),
		fmt.Sprintf("%04o", layout.JournalMode.Perm()), layout.LockTimeout.String(),
	} {
		_, _ = io.WriteString(hash, value)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func TestLayoutSecuritySecuresEveryNewDurableFileAndRejectsExistingDrift(t *testing.T) {
	directory := privateTestDirectory(t)
	path := filepath.Join(directory, "ledger.jsonl")
	security := &recordingLayoutSecurity{
		identity: "test-private-v1", directory: directory, secured: make(map[string]bool),
	}
	layout := privateTestLayout(path)
	layout.Security = security
	policy := Policy{MaxBytes: 64, MaxArchives: 2}
	if err := AppendTransactionWithLayout(path, policy, layout, func() ([]byte, error) {
		return []byte(`{"record":1}`), nil
	}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, layout.LockPath, layout.JournalPath} {
		if !security.secured[candidate] {
			t.Fatalf("new durable JSONL file was not secured: %s", candidate)
		}
	}
	if security.directoryChecks == 0 {
		t.Fatal("JSONL directory security was not validated")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	security.rejectPath = path
	if err := AppendWithLayout(path, []byte(`{"record":2}`), policy, layout); err == nil {
		t.Fatal("existing security drift was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("rejected security drift changed JSONL data: %v", err)
	}
}

func TestLayoutSecurityIdentityIsBoundIntoRecoveryJournal(t *testing.T) {
	directory := privateTestDirectory(t)
	path := filepath.Join(directory, "ledger.jsonl")
	security := &recordingLayoutSecurity{
		identity: "test-private-v1", directory: directory, secured: make(map[string]bool),
	}
	layout := privateTestLayout(path)
	layout.Security = security
	policy := Policy{MaxBytes: 64, MaxArchives: 2}
	if err := withLayoutLock(path, layout, func() error {
		_, err := beginAppendJournalWithLayout(path, policy, layout, false, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	foreignSecurity := &recordingLayoutSecurity{
		identity: "test-private-v2", directory: directory, secured: make(map[string]bool),
	}
	foreign := layout
	foreign.Security = foreignSecurity
	if err := RecoverWithLayout(path, foreign, func() error { return nil }); err == nil {
		t.Fatal("recovery accepted a different filesystem-security contract")
	}
	if _, err := os.Lstat(layout.JournalPath); err != nil {
		t.Fatalf("rejected recovery changed the original journal: %v", err)
	}
}

func TestLayoutSecurityAcceptsPreSecurityRecoveryJournal(t *testing.T) {
	directory := privateTestDirectory(t)
	path := filepath.Join(directory, "ledger.jsonl")
	security := &recordingLayoutSecurity{
		identity: "test-private-v1", directory: directory, secured: make(map[string]bool),
	}
	layout := privateTestLayout(path)
	layout.Security = security
	journal, err := beginAppendJournalWithLayout(
		path, Policy{MaxBytes: 64, MaxArchives: 2}, layout, false, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	journal.LayoutIdentity = legacyLayoutIdentityForTest(path, layout)
	if err := writeAppendJournalWithLayout(path, layout, journal); err != nil {
		t.Fatal(err)
	}
	loaded, err := readAppendJournalWithLayout(path, layout)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.LayoutIdentity != journal.LayoutIdentity {
		t.Fatalf("legacy recovery journal = %#v, want identity %q", loaded, journal.LayoutIdentity)
	}
}
