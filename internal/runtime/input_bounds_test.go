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

func TestDecodeLockfileRejectsNonStrictJSON(t *testing.T) {
	for _, test := range []struct {
		name string
		body []byte
		want string
	}{
		{name: "trailing", body: []byte(`{} {}`), want: "multiple JSON values"},
		{name: "duplicate root", body: []byte(`{"format_version":"5","format_version":"4"}`), want: "duplicate object key"},
		{name: "duplicate nested", body: []byte(`{"actions":{"rules":[],"rules":[]}}`), want: "duplicate object key"},
		{name: "invalid UTF-8", body: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, want: "invalid UTF-8"},
		{name: "unpaired surrogate", body: []byte(`{"x":"\ud800"}`), want: "unpaired high surrogate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeLockfile(test.body); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("strict lockfile JSON error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestExecutionInputRejectsDepthAndAggregateItemsBeforeDecode(t *testing.T) {
	deep := strings.Repeat(`{"x":`, maxExecutionInputJSONDepth+1) + `null` + strings.Repeat(`}`, maxExecutionInputJSONDepth+1)
	if _, err := LoadExecutionInputsText(deep, "deep"); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("deep execution input error = %v", err)
	}

	items := `{"read_paths":[` + strings.Repeat(`"x",`, maxExecutionInputItems) + `"x"]}`
	if _, err := LoadExecutionInputsText(items, "wide"); err == nil || !strings.Contains(err.Error(), "aggregate items") {
		t.Fatalf("wide execution input error = %v", err)
	}
}
