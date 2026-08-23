//go:build !windows

package tasklifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTransactionPresenceReportsUnreadableJournal(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, transactionRel, []byte("{}\n"))
	path := filepath.Join(repo, filepath.FromSlash(transactionRel))
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	exists, err := transactionExists(repo)
	if err == nil {
		if os.Geteuid() == 0 {
			t.Skip("root can read a mode-000 journal")
		}
		t.Fatalf("unreadable journal admitted: exists=%v", exists)
	}
	if exists {
		t.Fatal("unreadable journal reported as a valid pending transaction")
	}
}
