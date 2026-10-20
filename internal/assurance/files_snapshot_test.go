package assurance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadBoundedChargesOpenedSnapshotOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proof.txt")
	if err := os.WriteFile(path, []byte("proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := newEvaluationState(nil, 1)
	budget := state.budget
	for i := 0; i < 2; i++ {
		body, err := state.read(path)
		if err != nil || string(body) != "proof" {
			t.Fatalf("read %d = %q, %v", i, body, err)
		}
	}
	if len(budget.files) != 1 || len(budget.byteFiles) != 1 || budget.bytes != int64(len("proof")) {
		t.Fatalf("snapshot budget charged more than once: files=%d byteFiles=%d bytes=%d", len(budget.files), len(budget.byteFiles), budget.bytes)
	}
}
