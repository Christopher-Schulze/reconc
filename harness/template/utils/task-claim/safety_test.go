package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadBindingsRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "unknown field", body: "default_claims: []\nbindings: []\nunknown: true\n", want: "field unknown not found"},
		{name: "multiple documents", body: "default_claims: []\nbindings: []\n---\ndefault_claims: []\n", want: "multiple YAML documents"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bindings.yaml")
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadBindings(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestValidateBindingsRejectsUnboundedClaimCount(t *testing.T) {
	cfg := bindingsConfig{DefaultClaims: make([]string, maxClaimEntries+1)}
	if err := validateBindings(cfg); err == nil || !strings.Contains(err.Error(), "claim entry count") {
		t.Fatalf("unbounded claim list accepted: %v", err)
	}
}

func TestClaimsForTaskTrimsBeforeDeduplication(t *testing.T) {
	cfg := bindingsConfig{
		DefaultClaims: []string{" ci-green ", "ci-green"},
		Bindings:      []binding{{Match: " TASK-0001 ", Claims: []string{" release-ready "}}},
	}
	got := claimsForTask("TASK-0001-Example", cfg)
	want := []string{"ci-green", "release-ready"}
	if len(got) != len(want) {
		t.Fatalf("claims = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("claims = %v, want %v", got, want)
		}
	}
}

func TestBoundedOutputCapsDiagnosticMemory(t *testing.T) {
	var output boundedOutput
	payload := []byte(strings.Repeat("x", maxOutputBytes+4096))
	written, err := output.Write(payload)
	if err != nil || written != len(payload) {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if output.buffer.Len() != maxOutputBytes || !strings.HasSuffix(output.String(), "[output truncated]") {
		t.Fatalf("bounded output length=%d truncated=%v", output.buffer.Len(), output.truncated)
	}
}
