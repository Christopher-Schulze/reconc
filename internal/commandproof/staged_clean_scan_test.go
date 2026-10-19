package commandproof

import (
	"strings"
	"testing"
)

func TestCaptureStagedCleanScansTheWorkingTreeOnceBetweenCaptures(t *testing.T) {
	repo := newProofRepo(t)
	stageFile(t, repo, "candidate.txt", "first\n")
	scans := 0
	previous := checkCleanAgainstIndex
	t.Cleanup(func() { checkCleanAgainstIndex = previous })
	checkCleanAgainstIndex = func(root string) error {
		scans++
		return previous(root)
	}
	if _, err := CaptureStagedClean(repo); err != nil {
		t.Fatal(err)
	}
	if scans != 1 {
		t.Fatalf("working tree scanned %d times, want once", scans)
	}

	checkCleanAgainstIndex = func(root string) error {
		if err := previous(root); err != nil {
			return err
		}
		stageFile(t, repo, "candidate.txt", "raced\n")
		return nil
	}
	_, err := CaptureStagedClean(repo)
	if err == nil || !strings.Contains(err.Error(), "changed while capturing command proof") {
		t.Fatalf("an index change during the scan must be rejected: %v", err)
	}
}
