//go:build !windows

package actionstate

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestLoadApprovalAuthorityRegistryRejectsFIFOWithoutBlocking(t *testing.T) {
	repository := t.TempDir()
	operatorRoot := privateTestHome(t)
	fifo := filepath.Join(operatorRoot, "approval-authorities.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApprovalAuthorityRegistry(fifo, repository); err == nil {
		t.Fatal("FIFO approval authority registry was accepted")
	}
}
