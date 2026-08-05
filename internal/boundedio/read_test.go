package boundedio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileEnforcesExactLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := ReadFile(path, 5)
	if err != nil || string(body) != "12345" {
		t.Fatalf("exact-limit read = %q, %v", body, err)
	}
	if _, err := ReadFile(path, 4); err == nil || !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("oversized read error = %v", err)
	}
	if _, err := ReadFile(path, 0); err == nil {
		t.Fatal("non-positive limit must fail")
	}
}
