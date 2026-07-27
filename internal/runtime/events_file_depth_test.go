package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExecutionInputsFileContracts(t *testing.T) {
	t.Run("loads valid evidence", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "evidence.json")
		if err := os.WriteFile(path, []byte(`{"read_paths":["README.md"],"commands":["go test ./..."]}`), 0o600); err != nil {
			t.Fatalf("write evidence fixture: %v", err)
		}

		got, err := LoadExecutionInputsFile(path)
		if err != nil {
			t.Fatalf("LoadExecutionInputsFile: %v", err)
		}
		if len(got.ReadPaths) != 1 || got.ReadPaths[0] != "README.md" {
			t.Fatalf("unexpected read paths: %v", got.ReadPaths)
		}
		if len(got.Commands) != 1 || got.Commands[0] != "go test ./..." {
			t.Fatalf("unexpected commands: %v", got.Commands)
		}
	})

	t.Run("distinguishes a missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.json")
		_, err := LoadExecutionInputsFile(path)
		if err == nil || !strings.Contains(err.Error(), "execution input payload file not found") {
			t.Fatalf("expected missing-file error, got %v", err)
		}
	})

	t.Run("wraps other read failures", func(t *testing.T) {
		path := t.TempDir()
		_, err := LoadExecutionInputsFile(path)
		if err == nil || !strings.Contains(err.Error(), "read execution input file") {
			t.Fatalf("expected wrapped read error, got %v", err)
		}
	})
}
