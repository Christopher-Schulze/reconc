//go:build windows

package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreatedArtifactDescriptorReconcilesWindowsWritableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, err := captureCreatedRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := record.file.Chmod(0o400); err != nil {
		_ = record.close()
		t.Fatal(err)
	}
	if err := validateCreatedTarget(&record); err != nil {
		_ = record.close()
		t.Fatal(err)
	}
	if err := record.close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o200 != 0 {
		t.Fatalf("descriptor mode repair left artifact writable: %#o", info.Mode().Perm())
	}
}
