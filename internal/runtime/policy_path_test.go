package runtime

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
)

func TestEvidenceFileRejectsSymlinkEscape(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: proof\n    kind: require_evidence\n    when_paths: ['src/**']\n    evidence:\n      - file: 'proof/secret.txt'\n        must_contain: ['safe']\n    mode: block\n    message: proof\n")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "proof")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := CheckRepoPolicy(repo, ExecutionInputs{WritePaths: []string{"src/main.go"}})
	var boundary *rerrors.RepoBoundaryError
	if !stderrors.As(err, &boundary) {
		t.Fatalf("symlink escape error = %T %v, want RepoBoundaryError", err, err)
	}
}

func TestEvidenceFileReadIsBounded(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: proof\n    kind: require_evidence\n    when_paths: ['src/**']\n    evidence:\n      - file: 'proof.txt'\n        must_contain: ['safe']\n    mode: block\n    message: proof\n")
	path := filepath.Join(repo, "proof.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxEvidenceFileBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = CheckRepoPolicy(repo, ExecutionInputs{WritePaths: []string{"src/main.go"}})
	if err == nil || !strings.Contains(err.Error(), "exceeds 4194304 bytes") {
		t.Fatalf("oversized evidence error = %v", err)
	}
}
