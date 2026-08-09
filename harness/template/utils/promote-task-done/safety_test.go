package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAtomicRejectsUnexpectedTransactionImage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.md")
	if err := os.WriteFile(path, []byte("current"), 0o640); err != nil {
		t.Fatal(err)
	}

	err := writeAtomic(root, path, []byte("replacement"), []byte("stale"))
	if err == nil || !strings.Contains(err.Error(), "expected transaction image") {
		t.Fatalf("expected transaction mismatch, got %v", err)
	}
	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(body, []byte("current")) {
		t.Fatalf("transaction mismatch changed target to %q", body)
	}
}

func TestMoveNoClobberPreservesExistingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.md")
	destination := filepath.Join(root, "destination.md")
	if err := os.WriteFile(source, []byte("source"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := moveNoClobber(source, destination, []byte("source")); err == nil {
		t.Fatal("move overwrote an existing destination")
	}
	for path, want := range map[string]string{source: "source", destination: "destination"} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(body) != want {
			t.Fatalf("%s content = %q, want %q", path, body, want)
		}
	}
}

func TestBoundedCommandOutputCapsAuditDiagnostics(t *testing.T) {
	var output boundedCommandOutput
	payload := []byte(strings.Repeat("x", maxAuditOutput+1))
	written, err := output.Write(payload)
	if err != nil || written != len(payload) {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if !output.truncated || len(output.String()) != maxAuditOutput {
		t.Fatalf("bounded output length=%d truncated=%v", len(output.String()), output.truncated)
	}
}
