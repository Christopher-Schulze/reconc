//go:build !windows

package privatefs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOpenExistingLockReadOnlyDoesNotRepairMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	before := readOnlyLockMetadataForTest(t, path)
	if _, err := OpenExistingLockReadOnly(path); err == nil {
		t.Fatal("read-only lock open accepted insecure mode")
	}
	after := readOnlyLockMetadataForTest(t, path)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected read-only lock changed metadata: before=%#v after=%#v", before, after)
	}
}
