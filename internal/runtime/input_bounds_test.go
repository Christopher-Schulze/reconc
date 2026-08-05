package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/ingest"
)

func TestLockfileAndExecutionInputReadsAreBounded(t *testing.T) {
	tests := []struct {
		name  string
		limit int64
		run   func(string) error
	}{
		{
			name:  "lockfile",
			limit: maxLockfileBytes,
			run: func(root string) error {
				_, err := loadLockfile(root)
				return err
			},
		},
		{
			name:  "execution input",
			limit: maxExecutionInputFileBytes,
			run: func(root string) error {
				_, err := LoadExecutionInputsFile(filepath.Join(root, "events.json"))
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "events.json")
			if test.name == "lockfile" {
				path = filepath.Join(root, ingest.LockfilePath)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(test.limit + 1); err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := test.run(root); err == nil || !strings.Contains(err.Error(), "exceeds 16777216 bytes") {
				t.Fatalf("oversized %s error = %v", test.name, err)
			}
		})
	}
}
