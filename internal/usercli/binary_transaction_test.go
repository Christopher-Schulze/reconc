package usercli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/privatefs"
)

func TestFileBackedBinaryBackupRestoresExactBytesAndMode(t *testing.T) {
	target := filepath.Join(t.TempDir(), executableName())
	previous := bytes.Repeat([]byte("previous"), 1024)
	if err := os.WriteFile(target, previous, 0o711); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	wantMode := originalInfo.Mode().Perm()
	var backupPath string
	err = withBinaryBackup(target, func(backup *binaryBackup) error {
		backupPath = backup.path
		if backup.identity == nil || !os.SameFile(backup.identity, originalInfo) {
			t.Fatalf("backup identity = %v, want original target identity", backup.identity)
		}
		if backup.mode != wantMode || backup.size != int64(len(previous)) {
			t.Fatalf("backup metadata = mode %v size %d, want mode %v size %d", backup.mode, backup.size, wantMode, len(previous))
		}
		digest := sha256.Sum256(previous)
		if backup.digest != hex.EncodeToString(digest[:]) {
			t.Fatalf("backup digest = %q, want %x", backup.digest, digest)
		}
		file, err := os.Open(backup.path)
		if err != nil {
			t.Fatal(err)
		}
		info, statErr := file.Stat()
		validateErr := privatefs.ValidateFile(file, info)
		closeErr := file.Close()
		if statErr != nil || validateErr != nil || closeErr != nil {
			t.Fatalf("backup is not private: stat=%v validate=%v close=%v", statErr, validateErr, closeErr)
		}
		if err := os.WriteFile(target, []byte("broken"), 0o755); err != nil {
			t.Fatal(err)
		}
		return rollbackInstall(target, backup, true, errors.New("injected failure"))
	})
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("rollback error = %v", err)
	}
	body, readErr := os.ReadFile(target)
	if readErr != nil || !bytes.Equal(body, previous) {
		t.Fatalf("restored bytes differ: len=%d err=%v", len(body), readErr)
	}
	if info, statErr := os.Stat(target); statErr != nil || info.Mode().Perm() != wantMode {
		t.Fatalf("restored mode = %v, %v", info, statErr)
	}
	if _, statErr := os.Lstat(backupPath); !os.IsNotExist(statErr) {
		t.Fatalf("successful rollback retained backup: %v", statErr)
	}
}

func TestCapturedBinaryBackupCleansUpWhenOperationMissing(t *testing.T) {
	target := filepath.Join(t.TempDir(), executableName())
	if err := os.WriteFile(target, []byte("previous"), 0o755); err != nil {
		t.Fatal(err)
	}
	backup, err := captureBinaryBackup(target)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := backup.path
	err = withCapturedBinaryBackup(backup, nil)
	if err == nil || !strings.Contains(err.Error(), "operation is required") {
		t.Fatalf("missing operation error = %v", err)
	}
	if _, statErr := os.Lstat(backupPath); !os.IsNotExist(statErr) {
		t.Fatalf("backup retained after missing operation: %v", statErr)
	}
}

func TestCopyReleaseCandidateRejectsCancellationHashSizeAndShortWrite(t *testing.T) {
	body := []byte("candidate")
	sum := sha256.Sum256(body)
	valid := ReleaseAsset{Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name        string
		ctx         context.Context
		destination io.Writer
		asset       ReleaseAsset
		want        string
	}{
		{name: "cancelled", ctx: cancelled, destination: io.Discard, asset: valid, want: "canceled"},
		{name: "hash mismatch", ctx: context.Background(), destination: io.Discard, asset: ReleaseAsset{Size: int64(len(body)), SHA256: strings.Repeat("0", 64)}, want: "checksum"},
		{name: "size mismatch", ctx: context.Background(), destination: io.Discard, asset: ReleaseAsset{Size: int64(len(body) + 1), SHA256: valid.SHA256}, want: "size"},
		{name: "short write", ctx: context.Background(), destination: shortWriter{}, asset: valid, want: "short write"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := copyReleaseCandidate(test.ctx, test.destination, bytes.NewReader(body), test.asset)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("copy error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPrivateTemporaryBinaryIsCleanedAfterSuccessAndFailure(t *testing.T) {
	directory := t.TempDir()
	for _, operationErr := range []error{nil, errors.New("injected failure")} {
		var temporary string
		err := withPrivateTemporaryBinary(directory, ".candidate-*", func(path string) error {
			temporary = path
			return operationErr
		})
		if !errors.Is(err, operationErr) {
			t.Fatalf("operation error = %v, want %v", err, operationErr)
		}
		if _, statErr := os.Lstat(temporary); !os.IsNotExist(statErr) {
			t.Fatalf("temporary retained after operation: %s: %v", temporary, statErr)
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	return len(buffer) - 1, nil
}

func BenchmarkCopyReleaseCandidateNearLimitLocal(b *testing.B) {
	const size = maxBinaryBytes - 1
	hash := sha256.New()
	if _, err := io.Copy(hash, &repeatByteReader{remaining: size}); err != nil {
		b.Fatal(err)
	}
	asset := ReleaseAsset{Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}
	b.ReportAllocs()
	b.SetBytes(size)
	for range b.N {
		if err := copyReleaseCandidate(context.Background(), io.Discard, &repeatByteReader{remaining: size}, asset); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCopyReleaseCandidateNearLimitHTTP(b *testing.B) {
	const size = maxBinaryBytes - 1
	hash := sha256.New()
	if _, err := io.Copy(hash, &repeatByteReader{remaining: size}); err != nil {
		b.Fatal(err)
	}
	asset := ReleaseAsset{Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}
	previousClient := lifecycleHTTPClient
	b.Cleanup(func() { lifecycleHTTPClient = previousClient })
	lifecycleHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       &repeatByteReadCloser{repeatByteReader: repeatByteReader{remaining: size}},
			Header:     make(http.Header),
		}, nil
	})}
	b.ReportAllocs()
	b.SetBytes(size)
	for range b.N {
		err := streamDownload(context.Background(), "https://example.test/candidate", func(source io.Reader) error {
			return copyReleaseCandidate(context.Background(), io.Discard, source, asset)
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

type repeatByteReader struct {
	remaining int64
}

type repeatByteReadCloser struct {
	repeatByteReader
}

func (*repeatByteReadCloser) Close() error {
	return nil
}

func (reader *repeatByteReader) Read(buffer []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	count := int64(len(buffer))
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := range buffer[:count] {
		buffer[index] = 0x5a
	}
	reader.remaining -= count
	return int(count), nil
}

func TestPathInspectionReportsBrokenEntryAndKeepsLaterUsableCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires optional Windows privileges")
	}
	brokenDirectory := t.TempDir()
	usableDirectory := t.TempDir()
	if err := os.Symlink(filepath.Join(brokenDirectory, "missing"), filepath.Join(brokenDirectory, "reconc")); err != nil {
		t.Fatal(err)
	}
	usable := filepath.Join(usableDirectory, "reconc")
	if err := os.WriteFile(usable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", brokenDirectory+string(os.PathListSeparator)+usableDirectory)
	candidates, diagnostics, err := pathCandidatesDetailed()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !samePath(candidates[0], usable) {
		t.Fatalf("usable candidates = %v", candidates)
	}
	if len(diagnostics) != 1 || diagnostics[0].Status != "warn" || !strings.Contains(diagnostics[0].Detail, "broken") && !strings.Contains(diagnostics[0].Detail, "missing") {
		t.Fatalf("PATH diagnostics = %+v", diagnostics)
	}
}
