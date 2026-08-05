package tasklifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskControlReadIsBounded(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".reconc.yml")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxTaskControlBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(root); err == nil || !strings.Contains(err.Error(), "exceeds 4194304 bytes") {
		t.Fatalf("oversized TASK config error = %v", err)
	}
}
