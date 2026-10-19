package jsonl

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedAppendLeavesNoTornRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := appendRecordWithLayout(path, []byte("{\"first\":true}\n"), defaultLayout(path), 1024); err != nil {
		t.Fatal(err)
	}
	err := appendRecordWithLayoutHooks(path, []byte("{\"second\":true}\n"), defaultLayout(path), 1024, appendOpenHooks{
		write: func(writer io.Writer, record []byte) error {
			if _, err := writer.Write(record[:len(record)/2]); err != nil {
				return err
			}
			return errors.New("no space left on device")
		},
	})
	if err == nil {
		t.Fatal("a failed append reported success")
	}
	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(body, []byte("{\"first\":true}\n")) {
		t.Fatalf("failed append left a torn record: %q", body)
	}
	if err := appendRecordWithLayout(path, []byte("{\"third\":true}\n"), defaultLayout(path), 1024); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	if !bytes.Equal(body, []byte("{\"first\":true}\n{\"third\":true}\n")) {
		t.Fatalf("next record did not start on a clean line: %q", body)
	}
}
