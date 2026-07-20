//go:build windows

package runtime

import (
	stderrors "errors"
	"os/exec"
	"path/filepath"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
)

func TestNormalizeRejectsDirectoryJunctionEscapingRepo(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# t\n", "", "rules: []\n")
	outside := t.TempDir()
	escape := filepath.Join(repo, "escape")
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", escape, outside)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create directory junction: %v: %s", err, output)
	}

	_, err := CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{filepath.Join(escape, "not-created.txt")},
	})
	if err == nil {
		t.Fatal("expected RepoBoundaryError for directory-junction escape")
	}
	var boundaryError *rerrors.RepoBoundaryError
	if !stderrors.As(err, &boundaryError) {
		t.Fatalf("expected *RepoBoundaryError, got %T: %v", err, err)
	}
}
